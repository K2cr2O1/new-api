package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

type moderationSettingsRequest struct {
	Enabled   string `json:"enabled"`
	Blocklist string `json:"blocklist"`
}

// GetModerationSettings returns the moderation toggle, whether merchant
// credentials are present, and the blocklist operators can edit.
// Credential values are never included.
func GetModerationSettings(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"enabled":              normalizeModerationMode(setting.ContentModerationEnabled),
		"effective_enabled":    service.ModerationActive(),
		"credentials_present":  service.WaffoPromptCredentialsPresent(),
		"blocklist":            service.EffectiveBlocklistText(),
		"blocklist_is_default": strings.TrimSpace(setting.ContentModerationBlocklist) == "",
	})
}

// UpdateModerationSettings stores the toggle and the output blocklist.
func UpdateModerationSettings(c *gin.Context) {
	var request moderationSettingsRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, common.NewMessage("Invalid parameters"))
		return
	}
	mode := normalizeModerationMode(request.Enabled)
	if mode == "" {
		common.ApiError(c, common.NewMessage("Content moderation mode must be auto, true, or false"))
		return
	}
	if len(request.Blocklist) > 200_000 {
		common.ApiError(c, common.NewMessage("Content moderation blocklist is too long"))
		return
	}
	if err := model.UpdateOption("ContentModerationEnabled", mode); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOption("ContentModerationBlocklist", request.Blocklist); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"enabled":             mode,
		"effective_enabled":   service.ModerationActive(),
		"credentials_present": service.WaffoPromptCredentialsPresent(),
	})
}

// GetModerationLogs lists prompt and output moderation audits.
func GetModerationLogs(c *gin.Context) {
	page := common.GetPageQuery(c)
	logs, total, err := model.ListModerationLogs(page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(logs)
	common.ApiSuccess(c, page)
}

func normalizeModerationMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto"
	case "true", "on", "1":
		return "true"
	case "false", "off", "0":
		return "false"
	default:
		return ""
	}
}
