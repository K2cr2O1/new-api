package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

const (
	promptSiftPath         = "/v1/actions/verification/scan-prompt"
	promptSiftMaxRunes     = 10000
	promptSiftMaxWindows   = 8
	promptSiftMaxBodyBytes = 64 << 20
	contextModerationDone  = "content_moderation_prompt_done"
	contextModerationRun   = "content_moderation_running"

	stagePrompt = "prompt"
	stageOutput = "output"
)

// GenerationSpec describes one user-driven generation request.
type GenerationSpec struct {
	RelayMode   int
	Model       string
	Body        []byte
	ContentType string
	IsStream    bool
}

var (
	promptSiftBaseURL = "https://api.waffo.ai"
	promptSiftClient  = &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	promptSiftLimit       = 100
	promptSiftWindow      = 60 * time.Second
	promptSiftMaxAttempts = 3
	promptSiftMaxWait     = 2 * time.Second
	promptSiftNow         = time.Now
	promptSiftSleep       = func(d time.Duration) {
		if d > 0 {
			time.Sleep(d)
		}
	}

	promptSiftMu    sync.Mutex
	promptSiftTimes []time.Time

	privateKeyCache sync.Mutex
	privateKeyPEM   string
	privateKeyRSA   *rsa.PrivateKey
	privateKeyErr   error
)

var promptKeys = map[string]struct{}{
	"prompt":          {},
	"text":            {},
	"content":         {},
	"input":           {},
	"instructions":    {},
	"negative_prompt": {},
	"caption":         {},
	"system":          {},
	"transcript":      {},
	"delta":           {},
	"revised_prompt":  {},
}

// ModerationActive reports whether Prompt Sift and output checks run.
// "auto" enables them when the Waffo Pancake merchant credentials exist.
func ModerationActive() bool {
	switch strings.ToLower(strings.TrimSpace(setting.ContentModerationEnabled)) {
	case "false", "0", "off":
		return false
	case "true", "1", "on":
		return true
	default:
		return WaffoPromptCredentialsPresent()
	}
}

// WaffoPromptCredentialsPresent is true when the payment merchant id and
// private key used to sign Prompt Sift are both set. The values are not returned.
func WaffoPromptCredentialsPresent() bool {
	return strings.TrimSpace(setting.WaffoPancakeMerchantID) != "" && strings.TrimSpace(setting.WaffoPancakePrivateKey) != ""
}

// ScreenGeneration calls Prompt Sift before any upstream generation.
// A non-nil error means the caller must not call upstream.
func ScreenGeneration(c *gin.Context, spec GenerationSpec) *types.NewAPIError {
	if c != nil && c.GetBool(contextModerationDone) {
		return nil
	}
	if !ModerationActive() {
		return nil
	}
	mode := spec.RelayMode
	if c != nil {
		if v, ok := c.Get("relay_mode"); ok {
			if m, ok := v.(int); ok {
				mode = m
			}
		}
	}
	if !generationShouldScreen(c, mode) {
		return nil
	}
	if c != nil && c.GetBool(contextModerationRun) {
		return moderationClientError(c, http.StatusServiceUnavailable, i18n.MsgContentSafetyUnavailable, "")
	}
	if c != nil {
		c.Set(contextModerationRun, true)
		defer c.Set(contextModerationRun, false)
	}

	text, tooLong, err := promptTextForSpec(c, spec)
	if err != nil || tooLong {
		if tooLong {
			recordPrompt(c, spec.Model, "reject", "too_long", nil, "", text)
			return moderationClientError(c, http.StatusBadRequest, i18n.MsgContentSafetyTooLong, "")
		}
		common.SysLog(common.LogText("content moderation could not read the generation request"))
		recordPrompt(c, spec.Model, "reject", "unreadable", nil, "", "")
		return moderationClientError(c, http.StatusServiceUnavailable, i18n.MsgContentSafetyUnavailable, "")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		recordPrompt(c, spec.Model, "reject", "no_text", nil, "", "")
		return moderationClientError(c, http.StatusBadRequest, i18n.MsgContentSafetyNoText, "")
	}
	if apiErr := screenText(c, spec.Model, text); apiErr != nil {
		return apiErr
	}
	if c != nil {
		c.Set(contextModerationDone, true)
		message := i18n.T(c, i18n.MsgContentSafetyOutput)
		installOutputModerator(c, spec.Model, message, spec.IsStream)
	}
	return nil
}

// ScreenPromptText screens one text fragment, such as a realtime event.
// Empty text is not a prompt and is left to the caller; binary audio is not scanned.
func ScreenPromptText(c *gin.Context, modelName, text string) *types.NewAPIError {
	if !ModerationActive() {
		return nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return screenText(c, modelName, text)
}

// ExtractPromptText returns the user-visible prompt text from a JSON or form body.
func ExtractPromptText(body []byte, contentType string) string {
	text, _, _ := collectPrompt(bytes.NewReader(body), contentType, true)
	return text
}

func screenText(c *gin.Context, modelName, text string) *types.NewAPIError {
	windows, tooLong := promptWindows(text)
	if tooLong {
		recordPrompt(c, modelName, "reject", "too_long", nil, "", text)
		return moderationClientError(c, http.StatusBadRequest, i18n.MsgContentSafetyTooLong, "")
	}
	locale := promptLocale(text)
	var providerIDs []string
	var categories []string
	for _, window := range windows {
		verdict, apiErr := scanPromptWindow(c, window, locale)
		if apiErr != nil {
			verdictName := "reject"
			reason := "error"
			status := http.StatusServiceUnavailable
			if apiErr.StatusCode == http.StatusTooManyRequests {
				verdictName = "rate_limited"
				reason = "rate_limited"
				status = http.StatusTooManyRequests
			}
			if verdict.Action == "review" && verdict.ReasonCode != "service_degraded" {
				verdictName = "review"
				reason = verdict.ReasonCode
				status = http.StatusBadRequest
			}
			if verdict.Action == "block" {
				verdictName = "block"
				reason = verdict.ReasonCode
				status = http.StatusBadRequest
			}
			recordPrompt(c, modelName, verdictName, reason, verdict.Categories, verdict.RequestID, window)
			if status == http.StatusTooManyRequests && verdict.RetryAfter > 0 && c != nil {
				c.Header("Retry-After", strconv.Itoa(int(verdict.RetryAfter.Seconds())))
			}
			return apiErr
		}
		if verdict.RequestID != "" {
			providerIDs = append(providerIDs, verdict.RequestID)
		}
		categories = append(categories, verdict.Categories...)
	}
	if cats := matchBlocklist(text); len(cats) > 0 {
		recordPrompt(c, modelName, "block", "blocklist", cats, strings.Join(providerIDs, ","), text)
		return moderationClientError(c, http.StatusBadRequest, i18n.MsgContentSafetyGuidelines, "")
	}
	recordPrompt(c, modelName, "allow", "allowed", categories, strings.Join(providerIDs, ","), text)
	return nil
}

type siftVerdict struct {
	Action     string
	ReasonCode string
	Categories []string
	RequestID  string
	RetryAfter time.Duration
}

func scanPromptWindow(c *gin.Context, prompt, locale string) (siftVerdict, *types.NewAPIError) {
	var last siftVerdict
	for attempt := range promptSiftMaxAttempts {
		if attempt > 0 {
			delay, ok := retryDelay(attempt-1, last.RetryAfter)
			if !ok {
				break
			}
			promptSiftSleep(delay)
		}
		verdict, status, retryAfter, callErr := siftOnce(requestContext(c), prompt, locale)
		verdict.RetryAfter = retryAfter
		last = verdict
		if callErr == nil && verdict.Action == "allow" {
			return verdict, nil
		}
		if callErr == nil && verdict.Action == "block" {
			return verdict, moderationClientError(c, http.StatusBadRequest, i18n.MsgContentSafetyGuidelines, "")
		}
		if callErr == nil && verdict.Action == "review" && verdict.ReasonCode != "service_degraded" {
			return verdict, moderationClientError(c, http.StatusBadRequest, i18n.MsgContentSafetyReview, "")
		}
		if status == http.StatusTooManyRequests && retryAfter > promptSiftMaxWait {
			return verdict, moderationClientError(c, http.StatusTooManyRequests, i18n.MsgContentSafetyRateLimit, "")
		}
		if callErr == errSiftLocalLimit {
			return verdict, moderationClientError(c, http.StatusTooManyRequests, i18n.MsgContentSafetyRateLimit, "")
		}
		retryable := status >= 500 || status == http.StatusTooManyRequests || (verdict.Action == "review" && verdict.ReasonCode == "service_degraded") || isTimeout(callErr)
		if !retryable {
			break
		}
	}
	if last.Action == "review" && last.ReasonCode != "service_degraded" {
		return last, moderationClientError(c, http.StatusBadRequest, i18n.MsgContentSafetyReview, "")
	}
	if last.RetryAfter > promptSiftMaxWait {
		return last, moderationClientError(c, http.StatusTooManyRequests, i18n.MsgContentSafetyRateLimit, "")
	}
	return last, moderationClientError(c, http.StatusServiceUnavailable, i18n.MsgContentSafetyUnavailable, "")
}

var errSiftLocalLimit = errors.New("content safety rate limit")

func siftOnce(ctx context.Context, prompt, locale string) (siftVerdict, int, time.Duration, error) {
	var verdict siftVerdict
	merchantID := strings.TrimSpace(setting.WaffoPancakeMerchantID)
	key, err := cachedPrivateKey(setting.WaffoPancakePrivateKey)
	if err != nil || merchantID == "" {
		return verdict, 0, 0, errors.New("content safety credentials are unavailable")
	}
	if !acquirePromptSiftSlot(promptSiftNow()) {
		return verdict, http.StatusTooManyRequests, promptSiftWindow, errSiftLocalLimit
	}
	body, err := common.Marshal(map[string]string{
		"prompt":   prompt,
		"locale":   locale,
		"semantic": "enforce",
	})
	if err != nil {
		return verdict, 0, 0, err
	}
	timestamp := strconv.FormatInt(promptSiftNow().Unix(), 10)
	sum := sha256.Sum256(body)
	bodyHash := base64.StdEncoding.EncodeToString(sum[:])
	canonical := "POST\n" + promptSiftPath + "\n" + timestamp + "\n" + bodyHash
	digest := sha256.Sum256([]byte(canonical))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return verdict, 0, 0, errors.New("content safety credentials are unavailable")
	}
	endpoint := strings.TrimRight(promptSiftBaseURL, "/") + promptSiftPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return verdict, 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Merchant-Id", merchantID)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(signature))

	resp, err := promptSiftClient.Do(req)
	if err != nil {
		return verdict, 0, 0, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return verdict, resp.StatusCode, 0, err
	}
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
	var envelope struct {
		Data *struct {
			Action            string   `json:"action"`
			ReasonCode        string   `json:"reasonCode"`
			MatchedCategories []string `json:"matchedCategories"`
			RequestID         string   `json:"requestId"`
		} `json:"data"`
	}
	if err := common.Unmarshal(payload, &envelope); err != nil || envelope.Data == nil {
		common.SysLog(common.LogText("prompt sift response was not a verdict status=%d", resp.StatusCode))
		return verdict, resp.StatusCode, retryAfter, errors.New("content safety response was not a verdict")
	}
	verdict.Action = envelope.Data.Action
	verdict.ReasonCode = envelope.Data.ReasonCode
	verdict.Categories = envelope.Data.MatchedCategories
	verdict.RequestID = envelope.Data.RequestID
	if verdict.Action == "" {
		return verdict, resp.StatusCode, retryAfter, errors.New("content safety response was not a verdict")
	}
	return verdict, resp.StatusCode, retryAfter, nil
}

func acquirePromptSiftSlot(now time.Time) bool {
	promptSiftMu.Lock()
	defer promptSiftMu.Unlock()
	cutoff := now.Add(-promptSiftWindow)
	kept := promptSiftTimes[:0]
	for _, ts := range promptSiftTimes {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	promptSiftTimes = kept
	if len(promptSiftTimes) >= promptSiftLimit {
		return false
	}
	promptSiftTimes = append(promptSiftTimes, now)
	return true
}

func retryDelay(attempt int, retryAfter time.Duration) (time.Duration, bool) {
	if retryAfter > promptSiftMaxWait {
		return 0, false
	}
	if retryAfter > 0 {
		return retryAfter, true
	}
	delay := 200 * time.Millisecond * time.Duration(1<<min(attempt, 3))
	if delay > promptSiftMaxWait {
		delay = promptSiftMaxWait
	}
	return delay, true
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay < 0 {
			return 0
		}
		return delay
	}
	return 0
}

func cachedPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	pemText = strings.TrimSpace(pemText)
	privateKeyCache.Lock()
	defer privateKeyCache.Unlock()
	if pemText == privateKeyPEM {
		return privateKeyRSA, privateKeyErr
	}
	key, err := parseRSAPrivateKey(pemText)
	privateKeyPEM = pemText
	privateKeyRSA = key
	if err != nil {
		privateKeyErr = errors.New("content safety credentials are unavailable")
	} else {
		privateKeyErr = nil
	}
	return privateKeyRSA, privateKeyErr
}

func parseRSAPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("invalid private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid private key")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("invalid private key")
	}
	return key, nil
}

func generationShouldScreen(c *gin.Context, mode int) bool {
	if c != nil && c.Request != nil {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return false
		}
		path := c.Request.URL.Path
		if strings.Contains(path, "/fetch") || strings.HasSuffix(path, "/content") {
			return false
		}
	}
	switch mode {
	case relayconstant.RelayModeEmbeddings,
		relayconstant.RelayModeModerations,
		relayconstant.RelayModeRerank,
		relayconstant.RelayModeAudioTranscription,
		relayconstant.RelayModeAudioTranslation,
		relayconstant.RelayModeToolsSearch,
		relayconstant.RelayModeToolsFetch,
		relayconstant.RelayModeAlphaSearch,
		relayconstant.RelayModeMidjourneyNotify,
		relayconstant.RelayModeMidjourneyTaskFetch,
		relayconstant.RelayModeMidjourneyTaskFetchByCondition,
		relayconstant.RelayModeMidjourneyTaskImageSeed,
		relayconstant.RelayModeMidjourneyUpload,
		relayconstant.RelayModeVideoFetchByID,
		relayconstant.RelayModeRealtime:
		return false
	default:
		return true
	}
}

func promptWindows(text string) ([]string, bool) {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil, false
	}
	if len(runes) > promptSiftMaxRunes*promptSiftMaxWindows {
		return nil, true
	}
	windows := make([]string, 0, (len(runes)+promptSiftMaxRunes-1)/promptSiftMaxRunes)
	for start := 0; start < len(runes); start += promptSiftMaxRunes {
		end := min(start+promptSiftMaxRunes, len(runes))
		windows = append(windows, string(runes[start:end]))
	}
	return windows, false
}

func promptLocale(text string) string {
	han, kana := 0, 0
	for _, r := range text {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana) {
			kana++
			continue
		}
		if unicode.Is(unicode.Han, r) {
			han++
		}
	}
	if kana > 0 && kana >= han {
		return "ja"
	}
	if han > 0 {
		return "zh"
	}
	return "en"
}

func promptTextForSpec(c *gin.Context, spec GenerationSpec) (string, bool, error) {
	contentType := spec.ContentType
	if spec.Body != nil {
		if contentType == "" {
			contentType = "application/json"
		}
		return collectPrompt(bytes.NewReader(spec.Body), contentType, true)
	}
	if c == nil || c.Request == nil {
		return "", false, errors.New("missing request")
	}
	if contentType == "" {
		contentType = c.GetHeader("Content-Type")
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return "", false, err
	}
	reader, err := storage.NewReader()
	if err != nil {
		return "", false, err
	}
	defer reader.Close()
	return collectPrompt(reader, contentType, true)
}

func collectPrompt(r io.Reader, contentType string, keysOnly bool) (string, bool, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err == nil && strings.HasPrefix(mediaType, "multipart/") {
		return collectMultipartPrompt(r, params["boundary"])
	}
	if err == nil && mediaType == "application/x-www-form-urlencoded" {
		body, readErr := io.ReadAll(io.LimitReader(r, promptSiftMaxBodyBytes+1))
		if readErr != nil {
			return "", false, readErr
		}
		if len(body) > promptSiftMaxBodyBytes {
			return "", true, nil
		}
		values, parseErr := url.ParseQuery(string(body))
		if parseErr != nil {
			return "", false, parseErr
		}
		var parts []string
		for key, items := range values {
			if _, ok := promptKeys[strings.ToLower(key)]; !ok {
				continue
			}
			for _, item := range items {
				if isScreenableText(item) {
					parts = append(parts, item)
				}
			}
		}
		return strings.Join(parts, "\n"), false, nil
	}
	return scanJSONPrompt(r, keysOnly)
}

func collectMultipartPrompt(r io.Reader, boundary string) (string, bool, error) {
	if boundary == "" {
		return "", false, errors.New("missing multipart boundary")
	}
	reader := multipart.NewReader(r, boundary)
	var parts []string
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, err
		}
		name := strings.ToLower(part.FormName())
		if part.FileName() != "" {
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		if _, ok := promptKeys[name]; !ok {
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		buf, err := io.ReadAll(io.LimitReader(part, promptSiftMaxRunes*promptSiftMaxWindows+1))
		if err != nil {
			return "", false, err
		}
		if len(buf) > promptSiftMaxRunes*promptSiftMaxWindows {
			return "", true, nil
		}
		if text := strings.TrimSpace(string(buf)); isScreenableText(text) {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n"), false, nil
}

func scanJSONPrompt(r io.Reader, keysOnly bool) (string, bool, error) {
	br := bufio.NewReaderSize(r, 32*1024)
	var (
		inString      bool
		escaped       bool
		unicodeHex    []byte
		buf           []byte
		overflow      bool
		prefix        []byte
		pending       []byte
		pendingPrefix []byte
		pendingBad    bool
		have          bool
		key           string
		keyDepth      = -1
		depth         int
		parts         []string
		total         int
		read          int
		tooLong       bool
	)
	take := func() {
		if !have {
			return
		}
		have = false
		value := string(pending)
		pending = nil
		use := !keysOnly || promptKey(key)
		if !use {
			return
		}
		if pendingBad {
			if keysOnly && isScreenableText(string(pendingPrefix)) {
				tooLong = true
			}
			return
		}
		if !isScreenableText(value) {
			return
		}
		runes := utf8.RuneCountInString(value)
		if total+runes > promptSiftMaxRunes*promptSiftMaxWindows {
			tooLong = true
			return
		}
		parts = append(parts, value)
		total += runes
	}
	for {
		b, err := br.ReadByte()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, err
		}
		read++
		if read > promptSiftMaxBodyBytes {
			return strings.Join(parts, "\n"), true, nil
		}
		if inString {
			if len(unicodeHex) > 0 || (escaped && b == 'u') {
				if escaped && b == 'u' {
					escaped = false
					unicodeHex = unicodeHex[:0]
					continue
				}
				unicodeHex = append(unicodeHex, b)
				if len(unicodeHex) == 4 {
					r, decodeErr := strconv.ParseUint(string(unicodeHex), 16, 32)
					unicodeHex = nil
					if decodeErr == nil && !overflow {
						buf = utf8.AppendRune(buf, rune(r))
					}
				}
				continue
			}
			if escaped {
				escaped = false
				if overflow {
					continue
				}
				switch b {
				case 'n':
					buf = append(buf, '\n')
				case 'r':
					buf = append(buf, '\r')
				case 't':
					buf = append(buf, '\t')
				case 'b':
					buf = append(buf, '\b')
				case 'f':
					buf = append(buf, '\f')
				default:
					buf = append(buf, b)
				}
				continue
			}
			if b == '\\' {
				escaped = true
				continue
			}
			if b == '"' {
				inString = false
				pendingBad = overflow
				if overflow {
					pendingPrefix = append([]byte(nil), prefix...)
					pending = nil
				} else {
					pending = append([]byte(nil), buf...)
					pendingPrefix = nil
				}
				have = true
				buf = buf[:0]
				overflow = false
				prefix = nil
				continue
			}
			if !overflow {
				buf = append(buf, b)
				if len(prefix) < 300 {
					prefix = append(prefix, b)
				}
				if len(buf) > promptSiftMaxRunes*promptSiftMaxWindows {
					overflow = true
					buf = buf[:0]
				}
			}
			continue
		}
		switch b {
		case '"':
			inString = true
			buf = buf[:0]
			overflow = false
			prefix = prefix[:0]
		case '{', '[':
			depth++
		case '}', ']':
			take()
			depth--
			if depth < keyDepth {
				key = ""
				keyDepth = -1
			}
		case ':':
			if have {
				key = string(pending)
				keyDepth = depth
				have = false
				pending = nil
			}
		case ',':
			take()
		default:
			if have && !unicode.IsSpace(rune(b)) {
				take()
			}
		}
	}
	take()
	return strings.Join(parts, "\n"), tooLong, nil
}

func promptKey(key string) bool {
	_, ok := promptKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

func isScreenableText(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "data:") {
		return false
	}
	if len(value) > 300 && !strings.ContainsAny(value, " \n\t") && !containsCJK(value) {
		return false
	}
	return true
}

func containsCJK(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) || unicode.In(r, unicode.Hiragana, unicode.Katakana) {
			return true
		}
	}
	return false
}

func moderationClientError(c *gin.Context, status int, key, retryAfter string) *types.NewAPIError {
	message := key
	if c != nil {
		message = i18n.T(c, key)
	}
	if retryAfter != "" && c != nil {
		c.Header("Retry-After", retryAfter)
	}
	return types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodePromptBlocked, status, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
}

func recordPrompt(c *gin.Context, modelName, verdict, reason string, categories []string, providerID, text string) {
	recordModeration(c, modelName, stagePrompt, verdict, reason, categories, providerID, text)
}

func recordModeration(c *gin.Context, modelName, stage, verdict, reason string, categories []string, providerID, text string) {
	if categories == nil {
		categories = []string{}
	}
	encoded, err := common.Marshal(categories)
	if err != nil {
		encoded = []byte("[]")
	}
	sum := sha256.Sum256([]byte(text))
	entry := &model.ModerationLog{
		ModelName:         modelName,
		Stage:             stage,
		Verdict:           verdict,
		ReasonCode:        reason,
		Categories:        string(encoded),
		ProviderRequestId: trimRunes(providerID, 128),
		ContentHash:       hex.EncodeToString(sum[:]),
		Excerpt:           moderationExcerpt(text),
	}
	if c != nil {
		entry.UserId = c.GetInt("id")
		entry.TokenId = c.GetInt("token_id")
		entry.RequestId = c.GetString(common.RequestIdKey)
		if entry.ModelName == "" {
			entry.ModelName = c.GetString("original_model")
		}
	}
	model.RecordModerationLog(entry)
}

func moderationExcerpt(text string) string {
	if strings.Contains(text, "PRIVATE KEY") || strings.Contains(text, "BEGIN RSA") {
		return "[redacted]"
	}
	text = strings.Join(strings.Fields(text), " ")
	return trimRunes(text, 120)
}

func trimRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func requestContext(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return context.Background()
	}
	return c.Request.Context()
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
