package service

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExtractPromptTextUsesUserVisibleFields(t *testing.T) {
	body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":[{"type":"text","text":"draw a cat"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},{"role":"user","content":"and a dog"}]}`)
	text := ExtractPromptText(body, "application/json")
	assert.Contains(t, text, "draw a cat")
	assert.Contains(t, text, "and a dog")
	assert.NotContains(t, text, "gpt-test")
	assert.NotContains(t, text, "base64")
}

func TestExtractPromptTextReadsMultipartPromptOnly(t *testing.T) {
	body := strings.Join([]string{
		"--bound",
		`Content-Disposition: form-data; name="prompt"`,
		"",
		"a calm lake",
		"--bound",
		`Content-Disposition: form-data; name="image"; filename="a.png"`,
		"Content-Type: image/png",
		"",
		"not-a-prompt",
		"--bound--",
		"",
	}, "\r\n")
	text := ExtractPromptText([]byte(body), "multipart/form-data; boundary=bound")
	assert.Equal(t, "a calm lake", text)
}

func TestScreenGenerationAllowReviewBlockAndUpstreamGate(t *testing.T) {
	keyPEM := testPrivateKeyPEM(t)
	cases := []struct {
		name       string
		action     string
		reason     string
		wantErr    bool
		wantStatus int
		wantText   string
	}{
		{name: "allow", action: "allow", reason: "allowed", wantErr: false},
		{name: "review", action: "review", reason: "review_required", wantErr: true, wantStatus: http.StatusBadRequest, wantText: "held for review"},
		{name: "block", action: "block", reason: "restricted_content", wantErr: true, wantStatus: http.StatusBadRequest, wantText: "usage guidelines"},
		{name: "degraded", action: "review", reason: "service_degraded", wantErr: true, wantStatus: http.StatusServiceUnavailable, wantText: "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, promptSiftPath, r.URL.Path)
				require.Equal(t, "MER_TEST", r.Header.Get("X-Merchant-Id"))
				require.NotEmpty(t, r.Header.Get("X-Signature"))
				require.NotContains(t, r.Header.Get("X-Signature"), "PRIVATE KEY")
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var payload map[string]string
				require.NoError(t, common.Unmarshal(body, &payload))
				assert.Equal(t, "enforce", payload["semantic"])
				assert.Contains(t, payload["prompt"], "hello")
				w.Header().Set("Content-Type", "application/json")
				raw, err := common.Marshal(map[string]any{"data": map[string]any{
					"action": tc.action, "reasonCode": tc.reason, "matchedCategories": []string{}, "requestId": "REQ_TEST",
				}})
				require.NoError(t, err)
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			usePromptSiftServer(t, server.URL, keyPEM)

			upstream := 0
			c := moderationContext(t, `{"messages":[{"role":"user","content":"hello"}]}`)
			err := ScreenGeneration(c, GenerationSpec{RelayMode: relayconstant.RelayModeChatCompletions, Model: "gpt-test"})
			if err == nil {
				upstream++
			}
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantStatus, err.StatusCode)
				assert.Contains(t, strings.ToLower(err.Error()), tc.wantText)
				assert.NotContains(t, err.Error(), "MER_TEST")
				assert.NotContains(t, err.Error(), "PRIVATE KEY")
				assert.Zero(t, upstream)
			} else {
				require.Nil(t, err)
				assert.Equal(t, 1, upstream)
				assert.Equal(t, 1, calls)
			}
			if tc.name == "degraded" {
				assert.Equal(t, promptSiftMaxAttempts, calls)
			}
		})
	}
}

func TestScreenGenerationRetries429ThenAllows(t *testing.T) {
	keyPEM := testPrivateKeyPEM(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errors":[{"message":"Too many requests"}]}`))
			return
		}
		raw, err := common.Marshal(map[string]any{"data": map[string]any{"action": "allow", "reasonCode": "allowed", "matchedCategories": []string{}, "requestId": "REQ_OK"}})
		require.NoError(t, err)
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	usePromptSiftServer(t, server.URL, keyPEM)
	c := moderationContext(t, `{"prompt":"a mountain"}`)
	upstream := 0
	err := ScreenGeneration(c, GenerationSpec{RelayMode: relayconstant.RelayModeImagesGenerations, Model: "image-test"})
	if err == nil {
		upstream++
	}
	require.Nil(t, err)
	assert.Equal(t, 1, upstream)
	assert.Equal(t, 2, calls)
}

func TestScreenGenerationHonorsLongRetryAfterWithoutForwarding(t *testing.T) {
	keyPEM := testPrivateKeyPEM(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Too many requests"}]}`))
	}))
	defer server.Close()
	usePromptSiftServer(t, server.URL, keyPEM)
	c := moderationContext(t, `{"input":"speak this"}`)
	upstream := 0
	err := ScreenGeneration(c, GenerationSpec{RelayMode: relayconstant.RelayModeAudioSpeech, Model: "tts"})
	if err == nil {
		upstream++
	}
	require.Error(t, err)
	assert.Equal(t, http.StatusTooManyRequests, err.StatusCode)
	assert.Equal(t, "30", c.Writer.Header().Get("Retry-After"))
	assert.Zero(t, upstream)
	assert.Equal(t, 1, calls)
	assert.NotContains(t, err.Error(), "MER_TEST")
}

func TestScreenGenerationLocalLimiterDoesNotSkipScan(t *testing.T) {
	keyPEM := testPrivateKeyPEM(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, err := common.Marshal(map[string]any{"data": map[string]any{"action": "allow", "reasonCode": "allowed", "matchedCategories": []string{}, "requestId": "REQ_OK"}})
		require.NoError(t, err)
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	usePromptSiftServer(t, server.URL, keyPEM)
	promptSiftLimit = 1
	c1 := moderationContext(t, `{"prompt":"first"}`)
	require.Nil(t, ScreenGeneration(c1, GenerationSpec{RelayMode: relayconstant.RelayModeChatCompletions, Model: "m"}))
	upstream := 0
	c2 := moderationContext(t, `{"prompt":"second"}`)
	err := ScreenGeneration(c2, GenerationSpec{RelayMode: relayconstant.RelayModeChatCompletions, Model: "m"})
	if err == nil {
		upstream++
	}
	require.Error(t, err)
	assert.Equal(t, http.StatusTooManyRequests, err.StatusCode)
	assert.Zero(t, upstream)
	assert.Equal(t, 1, calls)
}

func TestScreenGenerationTimeoutDoesNotForward(t *testing.T) {
	keyPEM := testPrivateKeyPEM(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	usePromptSiftServer(t, server.URL, keyPEM)
	promptSiftClient.Timeout = 30 * time.Millisecond
	upstream := 0
	err := ScreenGeneration(moderationContext(t, `{"prompt":"slow"}`), GenerationSpec{RelayMode: relayconstant.RelayModeChatCompletions, Model: "m"})
	if err == nil {
		upstream++
	}
	require.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
	assert.Zero(t, upstream)
	assert.NotContains(t, err.Error(), "MER_TEST")
}

func TestScreenGenerationSignatureMatchesDocumentedCanonicalRequest(t *testing.T) {
	key := generateTestKey(t)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		sum := sha256.Sum256(body)
		canonical := "POST\n" + r.URL.Path + "\n" + r.Header.Get("X-Timestamp") + "\n" + base64.StdEncoding.EncodeToString(sum[:])
		digest := sha256.Sum256([]byte(canonical))
		signature, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Signature"))
		require.NoError(t, err)
		require.NoError(t, rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature))
		got = r.Header.Get("X-Merchant-Id")
		raw, err := common.Marshal(map[string]any{"data": map[string]any{"action": "allow", "reasonCode": "allowed", "matchedCategories": []string{}, "requestId": "REQ_SIG"}})
		require.NoError(t, err)
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	usePromptSiftServer(t, server.URL, string(keyPEM))
	require.Nil(t, ScreenGeneration(moderationContext(t, `{"prompt":"signed"}`), GenerationSpec{RelayMode: relayconstant.RelayModeResponses, Model: "m"}))
	assert.Equal(t, "MER_TEST", got)
}

func TestOutputBlocklistCutsStreamAndBlocksFinalText(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	moderator := &outputModerator{
		ResponseWriter: c.Writer,
		c:              c,
		model:          "gpt-test",
		message:        "The generated content doesn't meet our usage guidelines.",
		stream:         true,
	}
	_, err := moderator.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
	require.NoError(t, err)
	_, err = moderator.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\" child porn\"}}]}\n\n"))
	require.NoError(t, err)
	moderator.finish()
	body := recorder.Body.String()
	assert.NotContains(t, body, "child porn")
	assert.Contains(t, body, "usage guidelines")
	assert.Equal(t, http.StatusBadRequest, recorder.Code)

	blocked := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(blocked)
	plain := &outputModerator{
		ResponseWriter: ctx.Writer,
		c:              ctx,
		model:          "gpt-test",
		message:        "The generated content doesn't meet our usage guidelines.",
	}
	_, err = plain.Write([]byte(`{"choices":[{"message":{"content":"see child porn now"}}]}`))
	require.NoError(t, err)
	plain.finish()
	assert.Equal(t, http.StatusBadRequest, blocked.Code)
	assert.NotContains(t, blocked.Body.String(), "child porn")
	assert.Contains(t, blocked.Body.String(), "usage guidelines")
}

func TestModerationStaysOffWithoutCredentialsAndFailsClosedWhenForced(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	usePromptSiftServer(t, server.URL, "")
	setting.ContentModerationEnabled = "auto"
	setting.WaffoPancakeMerchantID = ""
	setting.WaffoPancakePrivateKey = ""
	require.Nil(t, ScreenGeneration(moderationContext(t, `{"prompt":"hi"}`), GenerationSpec{RelayMode: relayconstant.RelayModeChatCompletions, Model: "m"}))
	assert.Zero(t, calls)

	setting.ContentModerationEnabled = "true"
	upstream := 0
	err := ScreenGeneration(moderationContext(t, `{"prompt":"hi"}`), GenerationSpec{RelayMode: relayconstant.RelayModeChatCompletions, Model: "m"})
	if err == nil {
		upstream++
	}
	require.Error(t, err)
	assert.Zero(t, upstream)
	assert.Zero(t, calls)
	assert.NotContains(t, err.Error(), "PRIVATE KEY")
}

func TestModerationLogMigratesOnSQLite(t *testing.T) {
	previous := model.DB
	t.Cleanup(func() { model.DB = previous })
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "moderation.db")), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.ModerationLog{}))
	require.NoError(t, db.AutoMigrate(&model.ModerationLog{}))
	model.RecordModerationLog(&model.ModerationLog{
		UserId: 3, TokenId: 4, ModelName: "m", Stage: stagePrompt, Verdict: "block",
		ReasonCode: "blocklist", Categories: `["child_sexual_content"]`, RequestId: "req",
		ContentHash: "abc", Excerpt: "short",
	})
	logs, total, err := model.ListModerationLogs(0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	assert.Equal(t, "block", logs[0].Verdict)
	assert.Equal(t, "prompt", logs[0].Stage)
	assert.NotContains(t, logs[0].Excerpt, "PRIVATE KEY")
}

func TestFetchRoutesAreNotPromptScanned(t *testing.T) {
	keyPEM := testPrivateKeyPEM(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	defer server.Close()
	usePromptSiftServer(t, server.URL, keyPEM)
	c := moderationContext(t, `{"prompt":"should not be scanned"}`)
	c.Request.Method = http.MethodGet
	require.Nil(t, ScreenGeneration(c, GenerationSpec{RelayMode: relayconstant.RelayModeVideoFetchByID, Model: "video"}))
	assert.Zero(t, calls)
}

func usePromptSiftServer(t *testing.T, serverURL, keyPEM string) {
	t.Helper()
	previousID := setting.WaffoPancakeMerchantID
	previousKey := setting.WaffoPancakePrivateKey
	previousMode := setting.ContentModerationEnabled
	previousList := setting.ContentModerationBlocklist
	previousURL := promptSiftBaseURL
	previousClient := promptSiftClient
	previousSleep := promptSiftSleep
	previousLimit := promptSiftLimit
	previousAttempts := promptSiftMaxAttempts
	t.Cleanup(func() {
		setting.WaffoPancakeMerchantID = previousID
		setting.WaffoPancakePrivateKey = previousKey
		setting.ContentModerationEnabled = previousMode
		setting.ContentModerationBlocklist = previousList
		promptSiftBaseURL = previousURL
		promptSiftClient = previousClient
		promptSiftSleep = previousSleep
		promptSiftLimit = previousLimit
		promptSiftMaxAttempts = previousAttempts
		privateKeyCache.Lock()
		privateKeyPEM = ""
		privateKeyRSA = nil
		privateKeyErr = nil
		privateKeyCache.Unlock()
		promptSiftMu.Lock()
		promptSiftTimes = nil
		promptSiftMu.Unlock()
		blocklistState.Lock()
		blocklistState.raw = "\x00"
		blocklistState.terms = nil
		blocklistState.Unlock()
	})
	setting.WaffoPancakeMerchantID = "MER_TEST"
	setting.WaffoPancakePrivateKey = keyPEM
	setting.ContentModerationEnabled = "true"
	setting.ContentModerationBlocklist = ""
	promptSiftBaseURL = serverURL
	promptSiftClient = &http.Client{Timeout: 8 * time.Second}
	promptSiftSleep = func(time.Duration) {}
	promptSiftLimit = 100
	promptSiftMu.Lock()
	promptSiftTimes = nil
	promptSiftMu.Unlock()
	privateKeyCache.Lock()
	privateKeyPEM = ""
	privateKeyRSA = nil
	privateKeyErr = nil
	privateKeyCache.Unlock()
}

func moderationContext(t *testing.T, body string) *gin.Context {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(common.RequestIdKey, "req-test")
	c.Set("id", 7)
	c.Set("token_id", 9)
	return c
}

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key := generateTestKey(t)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func generateTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}
