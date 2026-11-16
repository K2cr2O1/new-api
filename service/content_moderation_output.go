package service

import (
	"bytes"
	"mime"
	"net/http"
	"strings"
	"sync"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

type blockTerm struct {
	Category string
	Term     string
}

// Built-in seed used when the operator has not saved a blocklist.
// Terms are matched case-insensitively as substrings of normalized text.
var seedBlocklist = []blockTerm{
	{Category: "child_sexual_content", Term: "child sexual"},
	{Category: "child_sexual_content", Term: "child porn"},
	{Category: "child_sexual_content", Term: "childporn"},
	{Category: "child_sexual_content", Term: "csam"},
	{Category: "child_sexual_content", Term: "underage sex"},
	{Category: "child_sexual_content", Term: "preteen nude"},
	{Category: "child_sexual_content", Term: "loli porn"},
	{Category: "child_sexual_content", Term: "shota porn"},
	{Category: "child_sexual_content", Term: "儿童色情"},
	{Category: "child_sexual_content", Term: "幼童色情"},
	{Category: "child_sexual_content", Term: "未成年人性"},
	{Category: "child_sexual_content", Term: "child sex abuse"},
	{Category: "scam", Term: "phishing kit"},
	{Category: "scam", Term: "steal credit card"},
	{Category: "scam", Term: "carding tutorial"},
	{Category: "scam", Term: "银行卡盗刷"},
	{Category: "scam", Term: "romance scam script"},
	{Category: "violent_crime", Term: "how to make a bomb"},
	{Category: "violent_crime", Term: "pipe bomb"},
	{Category: "violent_crime", Term: "制作炸弹"},
	{Category: "violent_crime", Term: "school shooting plan"},
	{Category: "deepfake", Term: "nudify"},
	{Category: "deepfake", Term: "deepnude"},
}

var blocklistState struct {
	sync.Mutex
	raw   string
	terms []blockTerm
	dict  []string
}

// EffectiveBlocklistText is the list operators edit. An empty saved list
// shows the built-in seed so the seed is visible and can be changed.
func EffectiveBlocklistText() string {
	if strings.TrimSpace(setting.ContentModerationBlocklist) == "" {
		return formatBlocklist(seedBlocklist)
	}
	return setting.ContentModerationBlocklist
}

func formatBlocklist(terms []blockTerm) string {
	var b strings.Builder
	for _, term := range terms {
		b.WriteString(term.Category)
		b.WriteByte('|')
		b.WriteString(term.Term)
		b.WriteByte('\n')
	}
	return b.String()
}

func currentBlocklist() ([]blockTerm, []string) {
	raw := setting.ContentModerationBlocklist
	blocklistState.Lock()
	defer blocklistState.Unlock()
	if blocklistState.terms != nil && blocklistState.raw == raw {
		return blocklistState.terms, blocklistState.dict
	}
	terms := parseBlocklist(raw)
	if len(terms) == 0 {
		terms = append([]blockTerm(nil), seedBlocklist...)
	}
	dict := make([]string, len(terms))
	for i, term := range terms {
		dict[i] = normalizeForMatch(term.Term)
	}
	blocklistState.raw = raw
	blocklistState.terms = terms
	blocklistState.dict = dict
	return terms, dict
}

func parseBlocklist(raw string) []blockTerm {
	var terms []blockTerm
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		category := "custom"
		term := line
		if cat, rest, ok := strings.Cut(line, "|"); ok {
			cat = strings.TrimSpace(cat)
			rest = strings.TrimSpace(rest)
			if cat != "" && rest != "" {
				category = cat
				term = rest
			}
		}
		term = trimRunes(term, 200)
		if term == "" {
			continue
		}
		terms = append(terms, blockTerm{Category: category, Term: term})
	}
	return terms
}

func matchBlocklist(text string) []string {
	terms, dict := currentBlocklist()
	if len(dict) == 0 || strings.TrimSpace(text) == "" {
		return nil
	}
	normalized := normalizeForMatch(text)
	hit, words := AcSearch(normalized, dict, false)
	if !hit {
		return nil
	}
	seen := map[string]struct{}{}
	var categories []string
	for _, word := range words {
		needle := normalizeForMatch(word)
		for _, term := range terms {
			if normalizeForMatch(term.Term) != needle {
				continue
			}
			if _, ok := seen[term.Category]; ok {
				continue
			}
			seen[term.Category] = struct{}{}
			categories = append(categories, term.Category)
		}
	}
	return categories
}

func normalizeForMatch(text string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(text) {
		switch r {
		case '\u200b', '\u200c', '\u200d', '\ufeff':
			continue
		}
		if unicode.IsSpace(r) {
			if !space && b.Len() > 0 {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// OutputGuard checks text chunks from a websocket or realtime stream.
// A nil guard allows the chunk. Binary audio and image bytes are not inspected.
type OutputGuard struct {
	mu       sync.Mutex
	c        *gin.Context
	model    string
	rolling  strings.Builder
	blocked  bool
	recorded bool
}

// NewOutputGuard returns nil when moderation is off.
func NewOutputGuard(c *gin.Context, modelName string) *OutputGuard {
	if !ModerationActive() {
		return nil
	}
	return &OutputGuard{c: c, model: modelName}
}

// Allow reports whether the chunk may be delivered. After a violation, later
// chunks are refused and are not recorded again.
func (g *OutputGuard) Allow(chunk []byte) bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blocked {
		return false
	}
	text := extractShortStrings(chunk)
	if text == "" {
		return true
	}
	g.rolling.WriteString(text)
	g.rolling.WriteByte('\n')
	if g.rolling.Len() > 16000 {
		kept := g.rolling.String()
		g.rolling.Reset()
		if len(kept) > 8000 {
			kept = kept[len(kept)-8000:]
		}
		g.rolling.WriteString(kept)
	}
	categories := matchBlocklist(g.rolling.String())
	if len(categories) == 0 {
		return true
	}
	g.blocked = true
	if !g.recorded {
		g.recorded = true
		recordModeration(g.c, g.model, stageOutput, "block", "blocklist", categories, "", g.rolling.String())
	}
	return false
}

// Rejected reports whether a previous chunk was blocked.
func (g *OutputGuard) Rejected() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.blocked
}

// OutputViolationError is the client error for a blocked output chunk.
func OutputViolationError(c *gin.Context) *types.NewAPIError {
	return moderationClientError(c, http.StatusBadRequest, i18n.MsgContentSafetyOutput, "")
}

func extractShortStrings(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		trimmed = bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
	}
	if len(trimmed) == 0 || trimmed[0] == ':' {
		return ""
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		text, _, _ := scanJSONPrompt(bytes.NewReader(trimmed), false)
		return text
	}
	if isScreenableText(string(trimmed)) && len(trimmed) < 4000 {
		return string(trimmed)
	}
	return ""
}

func isBinaryMedia(contentType string) bool {
	if strings.TrimSpace(contentType) == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return strings.HasPrefix(mediaType, "image/") || strings.HasPrefix(mediaType, "video/") || strings.HasPrefix(mediaType, "audio/") || mediaType == "application/octet-stream"
}

// ArmOutputModeration installs the output checker without a prompt scan.
// Fetch responses use this for text fields. Binary media is passed through.
func ArmOutputModeration(c *gin.Context, modelName string, stream bool) {
	if !ModerationActive() || c == nil || c.Writer == nil {
		return
	}
	message := i18n.T(c, i18n.MsgContentSafetyOutput)
	installOutputModerator(c, modelName, message, stream)
}

// FinishOutputModeration releases text that was held for the blocklist check.
func FinishOutputModeration(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	if moderator := findOutputModerator(c.Writer); moderator != nil {
		moderator.finish()
	}
}

func installOutputModerator(c *gin.Context, modelName, message string, stream bool) {
	if c == nil || c.Writer == nil || findOutputModerator(c.Writer) != nil {
		return
	}
	c.Writer = &outputModerator{
		ResponseWriter: c.Writer,
		c:              c,
		model:          modelName,
		message:        message,
		stream:         stream,
	}
}

func findOutputModerator(w http.ResponseWriter) *outputModerator {
	for range 8 {
		if w == nil {
			return nil
		}
		if moderator, ok := w.(*outputModerator); ok {
			return moderator
		}
		unwrap, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil
		}
		w = unwrap.Unwrap()
	}
	return nil
}

type outputModerator struct {
	gin.ResponseWriter
	mu          sync.Mutex
	c           *gin.Context
	model       string
	message     string
	stream      bool
	blocked     bool
	finished    bool
	released    bool
	passthrough bool
	statusSet   bool
	pending     int
	raw         bytes.Buffer
	line        []byte
	held        []byte
	rolling     strings.Builder
	logged      bool
}

func (w *outputModerator) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.noteStatusLocked(code)
}

func (w *outputModerator) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.statusSet {
		w.noteStatusLocked(http.StatusOK)
	}
}

func (w *outputModerator) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.blocked {
		return len(p), nil
	}
	if w.passthrough || isBinaryMedia(w.Header().Get("Content-Type")) {
		w.passthrough = true
		w.flushRawLocked()
		n, err := w.ResponseWriter.Write(p)
		w.released = true
		return n, err
	}
	if w.stream {
		w.writeStreamLocked(p)
		return len(p), nil
	}
	w.raw.Write(p)
	if w.raw.Len() > 2<<20 {
		w.flushNonStreamPrefixLocked()
	}
	return len(p), nil
}

func (w *outputModerator) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *outputModerator) Flush() {
	w.mu.Lock()
	released := w.released
	w.mu.Unlock()
	if !released {
		return
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *outputModerator) finish() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished {
		return
	}
	w.finished = true
	if w.blocked || w.passthrough {
		return
	}
	if w.stream {
		if len(w.line) > 0 {
			if w.blockLineLocked(w.line) {
				return
			}
			w.held = append(w.held, w.line...)
			w.line = nil
		}
		if len(w.held) > 0 {
			w.writeThroughLocked(w.held)
			w.held = nil
		}
		return
	}
	if w.blockTextLocked(extractShortStrings(w.raw.Bytes())) {
		return
	}
	w.writeThroughLocked(w.raw.Bytes())
	w.raw.Reset()
}

func (w *outputModerator) noteStatusLocked(code int) {
	if isBinaryMedia(w.Header().Get("Content-Type")) {
		w.passthrough = true
		w.flushRawLocked()
		w.ResponseWriter.WriteHeader(code)
		w.released = true
		return
	}
	if !w.statusSet {
		w.pending = code
		w.statusSet = true
	}
}

func (w *outputModerator) writeStreamLocked(p []byte) {
	w.line = append(w.line, p...)
	for {
		index := bytes.IndexByte(w.line, '\n')
		if index < 0 {
			break
		}
		line := append([]byte(nil), w.line[:index+1]...)
		w.line = append([]byte(nil), w.line[index+1:]...)
		// Keep the previous data line until the next one is scanned, so a
		// blocklist phrase split across two SSE events is not delivered.
		// Blank separators and SSE comments are not text and do not release it.
		if isSSEComment(line) || len(bytes.TrimSpace(line)) == 0 {
			if !w.released {
				w.held = append(w.held, line...)
				continue
			}
			w.writeThroughLocked(line)
			continue
		}
		if w.blockLineLocked(line) {
			return
		}
		if len(w.held) > 0 {
			w.writeThroughLocked(w.held)
		}
		w.held = line
	}
	if len(w.line) > 8192 {
		if w.blockLineLocked(w.line) {
			return
		}
		if len(w.held) > 0 {
			w.writeThroughLocked(w.held)
			w.held = nil
		}
		w.writeThroughLocked(w.line[:len(w.line)-512])
		w.line = append([]byte(nil), w.line[len(w.line)-512:]...)
	}
}

func (w *outputModerator) flushNonStreamPrefixLocked() {
	all := append([]byte(nil), w.raw.Bytes()...)
	if len(all) <= 8192 {
		return
	}
	if w.blockTextLocked(extractShortStrings(all)) {
		return
	}
	tail := all[len(all)-8192:]
	w.writeThroughLocked(all[:len(all)-8192])
	w.raw.Reset()
	w.raw.Write(tail)
}

func (w *outputModerator) flushRawLocked() {
	if w.raw.Len() > 0 {
		w.writeThroughLocked(w.raw.Bytes())
		w.raw.Reset()
	}
	if len(w.held) > 0 {
		w.writeThroughLocked(w.held)
		w.held = nil
	}
	if len(w.line) > 0 {
		w.writeThroughLocked(w.line)
		w.line = nil
	}
}

func (w *outputModerator) blockLineLocked(line []byte) bool {
	text := extractShortStrings(line)
	if text == "" {
		return false
	}
	w.rolling.WriteString(text)
	w.rolling.WriteByte('\n')
	if w.rolling.Len() > 16000 {
		kept := w.rolling.String()
		w.rolling.Reset()
		if len(kept) > 8000 {
			kept = kept[len(kept)-8000:]
		}
		w.rolling.WriteString(kept)
	}
	return w.blockTextLocked(w.rolling.String())
}

func (w *outputModerator) blockTextLocked(text string) bool {
	if text == "" || w.blocked {
		return w.blocked
	}
	categories := matchBlocklist(text)
	if len(categories) == 0 {
		return false
	}
	if !w.logged {
		w.logged = true
		recordModeration(w.c, w.model, stageOutput, "block", "blocklist", categories, "", text)
	}
	w.rejectLocked()
	return true
}

func (w *outputModerator) rejectLocked() {
	w.blocked = true
	w.raw.Reset()
	w.held = nil
	w.line = nil
	if !w.released {
		payload, err := common.Marshal(map[string]any{
			"error": map[string]any{
				"message": w.message,
				"type":    "content_policy_violation",
				"code":    "content_policy_violation",
			},
		})
		if err != nil {
			payload = []byte(`{"error":{"message":"The generated content doesn't meet our usage guidelines.","type":"content_policy_violation","code":"content_policy_violation"}}`)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.ResponseWriter.WriteHeader(http.StatusBadRequest)
		w.released = true
		_, _ = w.ResponseWriter.Write(payload)
		return
	}
	payload, err := common.Marshal(map[string]any{
		"error": map[string]any{
			"message": w.message,
			"type":    "content_policy_violation",
			"code":    "content_policy_violation",
		},
	})
	if err != nil {
		return
	}
	_, _ = w.ResponseWriter.Write(append(append([]byte("\ndata: "), payload...), '\n', '\n'))
}

func (w *outputModerator) writeThroughLocked(p []byte) {
	if len(p) == 0 || w.blocked {
		return
	}
	if !w.released {
		status := http.StatusOK
		if w.statusSet && w.pending != 0 {
			status = w.pending
		}
		w.ResponseWriter.WriteHeader(status)
		w.released = true
	}
	_, _ = w.ResponseWriter.Write(p)
}

func isSSEComment(line []byte) bool {
	trimmed := bytes.TrimSpace(line)
	return len(trimmed) > 0 && trimmed[0] == ':'
}
