package model

import "github.com/QuantumNous/new-api/common"

// ModerationLog is an operator audit row for prompt and output checks.
// It is not a consume log. Excerpt is a short sample; ContentHash covers the
// screened text. The full prompt or output is not stored.
type ModerationLog struct {
	Id                int    `json:"id" gorm:"primaryKey"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index"`
	UserId            int    `json:"user_id" gorm:"index"`
	TokenId           int    `json:"token_id" gorm:"index"`
	ModelName         string `json:"model_name" gorm:"type:varchar(191);default:''"`
	Stage             string `json:"stage" gorm:"type:varchar(16);index;default:''"`
	Verdict           string `json:"verdict" gorm:"type:varchar(32);index;default:''"`
	ReasonCode        string `json:"reason_code" gorm:"type:varchar(64);default:''"`
	Categories        string `json:"categories" gorm:"type:text"`
	RequestId         string `json:"request_id" gorm:"type:varchar(64);index;default:''"`
	ProviderRequestId string `json:"provider_request_id" gorm:"type:varchar(128);default:''"`
	ContentHash       string `json:"content_hash" gorm:"type:varchar(64);default:''"`
	Excerpt           string `json:"excerpt" gorm:"type:text"`
}

func (ModerationLog) TableName() string {
	return "moderation_logs"
}

func RecordModerationLog(entry *ModerationLog) {
	if DB == nil || entry == nil {
		return
	}
	if entry.CreatedAt == 0 {
		entry.CreatedAt = common.GetTimestamp()
	}
	if err := DB.Create(entry).Error; err != nil {
		common.SysLog(common.LogText("failed to record moderation log"))
	}
}

func ListModerationLogs(start, size int) ([]*ModerationLog, int64, error) {
	var total int64
	if err := DB.Model(&ModerationLog{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []*ModerationLog
	err := DB.Order("id desc").Offset(start).Limit(size).Find(&logs).Error
	return logs, total, err
}
