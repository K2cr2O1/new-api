package common

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"time"
)

// 阿里云验证码 2.0（Captcha）配置
// 通过环境变量注入，避免改动 options 表结构
var (
	AliyunCaptchaEnabled         = false
	AliyunCaptchaSceneId         = ""
	AliyunCaptchaAccessKeyId     = ""
	AliyunCaptchaAccessKeySecret = ""
	AliyunCaptchaRegion          = "cn-shanghai"
	AliyunCaptchaEndpoint        = "captcha.cn-shanghai.aliyuncs.com"
	AliyunCaptchaPrefix          = ""
	AliyunCaptchaEKey            = ""
	// FailOpen: 验证服务异常（网络/签名等）时是否放行。默认 false（失败即拒绝）
	AliyunCaptchaFailOpen = false
)

func init() {
	AliyunCaptchaEnabled = GetEnvOrDefaultBool("ALIYUN_CAPTCHA_ENABLED", false)
	AliyunCaptchaSceneId = os.Getenv("ALIYUN_CAPTCHA_SCENE_ID")
	AliyunCaptchaAccessKeyId = os.Getenv("ALIYUN_CAPTCHA_ACCESS_KEY_ID")
	AliyunCaptchaAccessKeySecret = os.Getenv("ALIYUN_CAPTCHA_ACCESS_KEY_SECRET")
	AliyunCaptchaPrefix = os.Getenv("ALIYUN_CAPTCHA_PREFIX")
	AliyunCaptchaEKey = os.Getenv("ALIYUN_CAPTCHA_EKEY")
	if v := os.Getenv("ALIYUN_CAPTCHA_REGION"); v != "" {
		AliyunCaptchaRegion = v
	}
	if v := os.Getenv("ALIYUN_CAPTCHA_ENDPOINT"); v != "" {
		AliyunCaptchaEndpoint = v
	}
	AliyunCaptchaFailOpen = GetEnvOrDefaultBool("ALIYUN_CAPTCHA_FAIL_OPEN", false)
}

// GenerateEncryptedSceneId 生成加密模式所需的 EncryptedSceneId。
// 明文格式：sceneId&timestamp&expireTime；AES-256-CBC（PKCS7）加密；
// 结果：IV(16字节) + 密文 拼接后 Base64 标准编码。
// 文档：https://help.aliyun.com/zh/captcha/captcha2-0/...（V3 架构加密模式接入）
func GenerateEncryptedSceneId() (string, error) {
	if AliyunCaptchaSceneId == "" || AliyunCaptchaEKey == "" {
		return "", fmt.Errorf("aliyun captcha sceneId or ekey not configured")
	}

	keyBytes, err := base64.StdEncoding.DecodeString(AliyunCaptchaEKey)
	if err != nil {
		return "", fmt.Errorf("invalid ekey (base64): %w", err)
	}
	if len(keyBytes) != 32 {
		return "", fmt.Errorf("ekey must decode to 32 bytes, got %d", len(keyBytes))
	}

	// 有效期固定 1 小时（文档范围 1~86400 秒）
	const expireTime = 3600
	timestamp := time.Now().Unix()
	plaintext := fmt.Sprintf("%s&%d&%d", AliyunCaptchaSceneId, timestamp, expireTime)

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return "", err
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}

	padded := pkcs7Pad([]byte(plaintext), aes.BlockSize)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)

	result := append(iv, encrypted...)
	return base64.StdEncoding.EncodeToString(result), nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	return append(data, bytes.Repeat([]byte{byte(padding)}, padding)...)
}
