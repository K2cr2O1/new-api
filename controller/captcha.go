package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

// GetEncryptedSceneId 返回加密模式所需的 EncryptedSceneId。
// 前端在初始化阿里云验证码前调用，用于加密模式（防 sceneId 泄漏盗刷）。
func GetEncryptedSceneId(c *gin.Context) {
	encryptedId, err := common.GenerateEncryptedSceneId()
	if err != nil {
		common.SysLog("generate encrypted scene id error: " + err.Error())
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "验证码初始化失败，请稍后重试",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"EncryptedSceneId": encryptedId,
		},
	})
}
