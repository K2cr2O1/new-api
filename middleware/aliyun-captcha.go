package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// ===== 阿里云验证码 2.0（Captcha）服务端校验 =====
// 接口: VerifyIntelligentCaptcha  Version: 2023-03-05
// 签名: ACS3-HMAC-SHA256（自实现，避免引入新依赖）

type aliyunCaptchaResult struct {
	VerifyResult bool   `json:"VerifyResult"`
	VerifyCode  string `json:"VerifyCode"`
	CertifyId   string `json:"CertifyId"`
}

type aliyunCaptchaResp struct {
	RequestId string              `json:"RequestId"`
	Success   bool                `json:"Success"`
	Code      string              `json:"Code"`
	Message   string              `json:"Message"`
	Result    aliyunCaptchaResult `json:"Result"`
}

const upperhex = "0123456789ABCDEF"

// percentEncode 按 RFC3986 编码（阿里云要求，空格为 %20 而非 +）
func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&15])
		}
	}
	return b.String()
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSha256Hex(key, msg string) string {
	m := hmac.New(sha256.New, []byte(key))
	_, _ = m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

func randomNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var aliyunCaptchaHttpClient = &http.Client{Timeout: 8 * time.Second}

// verifyAliyunCaptcha 调用阿里云智能验证接口，返回是否通过
func verifyAliyunCaptcha(captchaVerifyParam string) (bool, error) {
	if common.AliyunCaptchaSceneId == "" || common.AliyunCaptchaAccessKeyId == "" || common.AliyunCaptchaAccessKeySecret == "" {
		return false, fmt.Errorf("aliyun captcha not configured (sceneId/ak/sk missing)")
	}

	params := map[string]string{
		"Action":             "VerifyIntelligentCaptcha",
		"Version":            "2023-03-05",
		"SceneId":            common.AliyunCaptchaSceneId,
		"CaptchaVerifyParam": captchaVerifyParam,
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	qparts := make([]string, 0, len(keys))
	for _, k := range keys {
		qparts = append(qparts, percentEncode(k)+"="+percentEncode(params[k]))
	}
	canonicalQuery := strings.Join(qparts, "&")

	host := common.AliyunCaptchaEndpoint
	date := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	nonce := randomNonce()
	payloadHash := sha256Hex([]byte("")) // POST 空 body

	hdr := map[string]string{
		"host":                  host,
		"x-acs-action":          "VerifyIntelligentCaptcha",
		"x-acs-version":         "2023-03-05",
		"x-acs-date":            date,
		"x-acs-signature-nonce": nonce,
		"x-acs-content-sha256":  payloadHash,
	}
	hkeys := make([]string, 0, len(hdr))
	for k := range hdr {
		hkeys = append(hkeys, k)
	}
	sort.Strings(hkeys)
	var ch strings.Builder
	for _, k := range hkeys {
		ch.WriteString(k)
		ch.WriteString(":")
		ch.WriteString(hdr[k])
		ch.WriteString("\n")
	}
	signedHeaders := strings.Join(hkeys, ";")

	canonicalRequest := "POST\n/\n" + canonicalQuery + "\n" + ch.String() + "\n" + signedHeaders + "\n" + payloadHash
	stringToSign := "ACS3-HMAC-SHA256\n" + sha256Hex([]byte(canonicalRequest))
	signature := hmacSha256Hex(common.AliyunCaptchaAccessKeySecret, stringToSign)
	authorization := "ACS3-HMAC-SHA256 Credential=" + common.AliyunCaptchaAccessKeyId +
		",SignedHeaders=" + signedHeaders + ",Signature=" + signature

	req, err := http.NewRequest("POST", "https://"+host+"/?"+canonicalQuery, nil)
	if err != nil {
		return false, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	res, err := aliyunCaptchaHttpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	if err != nil {
		return false, err
	}

	var r aliyunCaptchaResp
	if err := json.Unmarshal(body, &r); err != nil {
		return false, fmt.Errorf("invalid response (http %d): %s", res.StatusCode, string(body))
	}
	if !r.Success {
		return false, fmt.Errorf("aliyun api error code=%s message=%s http=%d", r.Code, r.Message, res.StatusCode)
	}
	if !r.Result.VerifyResult {
		// 拒验原因原先完全静默（服务端只看到 ok=false），线上排障只能靠猜。
		// F008=重复提交 F010=同IP频控 F015=交互失败 F014=初始化记录过期
		common.SysLog("aliyun captcha rejected: verify_code=" + r.Result.VerifyCode + " certify_id=" + r.Result.CertifyId)
	}
	return r.Result.VerifyResult, nil
}

// AliyunCaptchaCheck 阿里云人机验证中间件
// 校验来源优先级: query 参数 captchaVerifyParam > 请求头 X-Captcha-Param
func AliyunCaptchaCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !common.AliyunCaptchaEnabled {
			c.Next()
			return
		}
		param := c.Query("captchaVerifyParam")
		if param == "" {
			param = c.GetHeader("X-Captcha-Param")
		}
		if param == "" {
			common.ApiErrorT(c, "请完成人机验证后再提交")
			c.Abort()
			return
		}
		ok, err := verifyAliyunCaptcha(param)
		if err != nil {
			common.SysLog("aliyun captcha verify error: " + err.Error())
			if common.AliyunCaptchaFailOpen {
				c.Next()
				return
			}
			common.ApiErrorT(c, "人机验证服务异常，请稍后重试")
			c.Abort()
			return
		}
		if !ok {
			common.ApiErrorT(c, "人机验证未通过，请重试")
			c.Abort()
			return
		}
		c.Next()
	}
}
