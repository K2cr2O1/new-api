// Package epayv2 implements the Epay V2 (RSA / SHA256WithRSA) payment protocol.
//
// V2 differs from V1 in three ways:
//   - requests and callbacks are signed with the merchant RSA private key
//     (SHA256WithRSA) and verified with the platform RSA public key,
//   - the unified order endpoint is POST /api/pay/create instead of submit.php,
//   - every payload carries a timestamp and sign_type=RSA.
//
// Configuration is environment based so that no secret has to be stored in the
// options table:
//
//	EPAY_V2_ENABLED=true
//	EPAY_V2_PRIVATE_KEY_FILE=/data/epay/merchant_private.pem
//	EPAY_V2_PUBLIC_KEY_FILE=/data/epay/platform_public.pem
//
// PayAddress / EpayId are reused from the existing Epay settings.
package epayv2

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const (
	envEnabled     = "EPAY_V2_ENABLED"
	envPrivateKey  = "EPAY_V2_PRIVATE_KEY"
	envPrivateFile = "EPAY_V2_PRIVATE_KEY_FILE"
	envPublicKey   = "EPAY_V2_PUBLIC_KEY"
	envPublicFile  = "EPAY_V2_PUBLIC_KEY_FILE"

	createPath  = "/api/pay/create"
	signTypeRSA = "RSA"
	maxBodySize = 1 << 20
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Enabled reports whether the Epay V2 integration is switched on. The admin
// console option takes precedence; the environment variable remains a fallback
// for deployments that configure everything in docker-compose.
func Enabled() bool {
	// V1 (MD5 / submit.php) has been removed, so V2 is the only protocol. The
	// flag is kept so callers can still express intent, but it always reports
	// true; disabling it would leave no payment path at all.
	return true
}

// IsRSA reports whether a callback payload declares an RSA signature (V2).
func IsRSA(params map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(params["sign_type"]), signTypeRSA)
}

func readSecret(valueEnv, fileEnv string) ([]byte, error) {
	if v := strings.TrimSpace(os.Getenv(valueEnv)); v != "" {
		return []byte(v), nil
	}
	if p := strings.TrimSpace(os.Getenv(fileEnv)); p != "" {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", fileEnv, err)
		}
		return raw, nil
	}
	return nil, fmt.Errorf("neither %s nor %s is configured", valueEnv, fileEnv)
}

func merchantPrivateKey() (*rsa.PrivateKey, error) {
	raw, err := merchantPrivateKeyPEM()
	if err != nil {
		return nil, err
	}
	der, err := decodeKeyMaterial(raw)
	if err != nil {
		return nil, fmt.Errorf("merchant private key: %w", err)
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse merchant private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("merchant private key is not an RSA key")
	}
	return key, nil
}

func platformPublicKey() (*rsa.PublicKey, error) {
	raw, err := platformPublicKeyPEM()
	if err != nil {
		return nil, err
	}
	der, err := decodeKeyMaterial(raw)
	if err != nil {
		return nil, fmt.Errorf("platform public key: %w", err)
	}
	if key, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("parse platform public key: %w", err)
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("platform public key is not an RSA key")
	}
	return key, nil
}

// decodeKeyMaterial accepts a PEM block or a bare base64 DER payload. The Epay
// merchant panel hands out keys as a single base64 line without PEM headers, so
// both shapes have to work. Literal "\n" sequences pasted from JSON are also
// normalised before decoding.
func decodeKeyMaterial(raw []byte) ([]byte, error) {
	text := strings.TrimSpace(string(raw))
	text = strings.ReplaceAll(text, `\n`, "\n")
	if block, _ := pem.Decode([]byte(text)); block != nil {
		return block.Bytes, nil
	}
	compact := strings.Join(strings.Fields(text), "")
	if compact == "" {
		return nil, errors.New("key is empty")
	}
	if der, err := base64.StdEncoding.DecodeString(compact); err == nil {
		return der, nil
	}
	if der, err := base64.RawStdEncoding.DecodeString(compact); err == nil {
		return der, nil
	}
	return nil, errors.New("key is neither PEM nor base64 encoded DER")
}

func merchantPrivateKeyPEM() ([]byte, error) {
	if v := strings.TrimSpace(operation_setting.EpayMerchantPrivateKey); v != "" {
		return []byte(v), nil
	}
	return readSecret(envPrivateKey, envPrivateFile)
}

func platformPublicKeyPEM() ([]byte, error) {
	if v := strings.TrimSpace(operation_setting.EpayPlatformPublicKey); v != "" {
		return []byte(v), nil
	}
	return readSecret(envPublicKey, envPublicFile)
}

// canonicalString builds the string to sign: non-empty parameters except sign
// and sign_type, sorted by ASCII key order, joined as k=v&k=v.
func canonicalString(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || strings.TrimSpace(v) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	return strings.Join(parts, "&")
}

func signParams(params map[string]string) (string, error) {
	key, err := merchantPrivateKey()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(canonicalString(params)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign params: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func verifyParams(params map[string]string) error {
	sign := strings.TrimSpace(params["sign"])
	if sign == "" {
		return errors.New("signature is empty")
	}
	key, err := platformPublicKey()
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(sign)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	digest := sha256.Sum256([]byte(canonicalString(params)))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return fmt.Errorf("signature mismatch: %w", err)
	}
	return nil
}

// CreateArgs describes one unified order request.
type CreateArgs struct {
	Type       string
	OutTradeNo string
	Name       string
	Money      string
	NotifyURL  string
	ReturnURL  string
	ClientIP   string
}

// CreateResult is the usable part of a successful create response.
type CreateResult struct {
	TradeNo string
	PayType string
	PayInfo string
}

// Create submits a V2 unified order and returns the hosted payment URL.
func Create(ctx context.Context, args *CreateArgs) (*CreateResult, error) {
	base := strings.TrimRight(strings.TrimSpace(operation_setting.PayAddress), "/")
	if base == "" || strings.TrimSpace(operation_setting.EpayId) == "" {
		return nil, errors.New("epay address or merchant id is not configured")
	}
	params := map[string]string{
		"pid":          operation_setting.EpayId,
		"method":       "jump",
		"device":       "pc",
		"type":         args.Type,
		"out_trade_no": args.OutTradeNo,
		"notify_url":   args.NotifyURL,
		"return_url":   args.ReturnURL,
		"name":         args.Name,
		"money":        args.Money,
		"clientip":     args.ClientIP,
		"timestamp":    strconv.FormatInt(time.Now().Unix(), 10),
		"sign_type":    signTypeRSA,
	}
	sign, err := signParams(params)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	form.Set("sign", sign)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+createPath, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request epay v2 create: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("read epay v2 create response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("epay v2 create http %d: %s", resp.StatusCode, truncate(body))
	}

	fields, err := decodeJSONFields(body)
	if err != nil {
		return nil, err
	}
	// Most Epay deployments still return an unsigned create response. When a
	// signature is present it must verify; when it is absent we rely on TLS and
	// log the fact instead of breaking every checkout.
	if strings.TrimSpace(fields["sign"]) != "" {
		if err := verifyParams(fields); err != nil {
			return nil, fmt.Errorf("epay v2 create response rejected: %w", err)
		}
	} else {
		logger.LogWarn(ctx, fmt.Sprintf("epay v2 create response is not signed trade_no=%s pay_type=%s", fields["trade_no"], fields["pay_type"]))
	}
	code, _ := strconv.Atoi(strings.TrimSpace(fields["code"]))
	if code != 0 {
		return nil, fmt.Errorf("epay v2 create failed: code=%d msg=%s", code, fields["msg"])
	}
	if strings.TrimSpace(fields["pay_info"]) == "" {
		return nil, errors.New("epay v2 create returned empty pay_info")
	}
	return &CreateResult{
		TradeNo: fields["trade_no"],
		PayType: fields["pay_type"],
		PayInfo: fields["pay_info"],
	}, nil
}

// Callback is a verified V2 payment notification.
type Callback struct {
	Type        string
	TradeNo     string
	OutTradeNo  string
	Money       string
	TradeStatus string
	Name        string
	Timestamp   string
	Raw         map[string]string
}

// ParseCallback verifies a V2 notification with the platform public key.
func ParseCallback(params map[string]string) (*Callback, error) {
	if !IsRSA(params) {
		return nil, errors.New("callback is not RSA signed")
	}
	if err := verifyParams(params); err != nil {
		return nil, err
	}
	return &Callback{
		Type:        params["type"],
		TradeNo:     params["trade_no"],
		OutTradeNo:  params["out_trade_no"],
		Money:       params["money"],
		TradeStatus: params["trade_status"],
		Name:        params["name"],
		Timestamp:   params["timestamp"],
		Raw:         params,
	}, nil
}

func decodeJSONFields(body []byte) (map[string]string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("epay v2 response is not JSON: %w", err)
	}
	fields := make(map[string]string, len(raw))
	for k, v := range raw {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			fields[k] = s
			continue
		}
		fields[k] = strings.TrimSpace(string(v))
	}
	return fields, nil
}

func truncate(body []byte) string {
	const limit = 200
	if len(body) > limit {
		return string(body[:limit])
	}
	return string(body)
}
