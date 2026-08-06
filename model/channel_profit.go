package model

import (
	"errors"

	"gorm.io/gorm"
)

type ChannelProfitConfig struct {
	Id        int   `json:"id"`
	ChannelId int   `json:"channel_id" gorm:"uniqueIndex;not null"`
	Enabled   bool  `json:"enabled"`
	CreatedAt int64 `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt int64 `json:"updated_at" gorm:"autoUpdateTime"`
}

type ChannelProfitSnapshot struct {
	Id                   int64   `json:"id"`
	ChannelId            int     `json:"channel_id" gorm:"uniqueIndex:idx_profit_snapshot_key,priority:1;index;not null"`
	UsageDate            string  `json:"usage_date" gorm:"type:varchar(10);uniqueIndex:idx_profit_snapshot_key,priority:2;index;not null"`
	KeyFingerprint       string  `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_profit_snapshot_key,priority:3;not null"`
	KeyHint              string  `json:"key_hint" gorm:"type:varchar(32);not null"`
	Provider             string  `json:"provider" gorm:"type:varchar(16)"`
	BaselineQuota        int64   `json:"baseline_quota" gorm:"not null"`
	CurrentQuota         int64   `json:"current_quota" gorm:"not null"`
	UpstreamQuotaPerUnit float64 `json:"upstream_quota_per_unit" gorm:"not null"`
	DirectCostUSD        float64 `json:"direct_cost_usd"`
	DirectCostAvailable  bool    `json:"direct_cost_available"`
	UpstreamGroup        string  `json:"upstream_group" gorm:"type:varchar(64)"`
	UpstreamGroupRatio   float64 `json:"upstream_group_ratio"`
	RatioAvailable       bool    `json:"ratio_available"`
	Partial              bool    `json:"partial"`
	LastSyncedAt         int64   `json:"last_synced_at" gorm:"index"`
	LastError            string  `json:"last_error" gorm:"type:text"`
	CreatedAt            int64   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt            int64   `json:"updated_at" gorm:"autoUpdateTime"`
}

func ListChannelProfitConfigs() ([]*ChannelProfitConfig, error) {
	configs := make([]*ChannelProfitConfig, 0)
	err := DB.Order("channel_id ASC").Find(&configs).Error
	return configs, err
}

func ListEnabledChannelProfitConfigs() ([]*ChannelProfitConfig, error) {
	configs := make([]*ChannelProfitConfig, 0)
	err := DB.Where("enabled = ?", true).Order("channel_id ASC").Find(&configs).Error
	return configs, err
}

func CountEnabledChannelProfitConfigs() (int64, error) {
	var count int64
	err := DB.Model(&ChannelProfitConfig{}).Where("enabled = ?", true).Count(&count).Error
	return count, err
}

func SetChannelProfitConfig(channelId int, enabled bool) (*ChannelProfitConfig, error) {
	config := &ChannelProfitConfig{}
	err := DB.Where("channel_id = ?", channelId).First(config).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		config.ChannelId = channelId
		config.Enabled = enabled
		if err := DB.Create(config).Error; err != nil {
			return nil, err
		}
		return config, nil
	}
	if err != nil {
		return nil, err
	}
	if err := DB.Model(config).Update("enabled", enabled).Error; err != nil {
		return nil, err
	}
	config.Enabled = enabled
	return config, nil
}

func GetChannelProfitSnapshot(channelId int, usageDate string, keyFingerprint string) (*ChannelProfitSnapshot, error) {
	snapshot := &ChannelProfitSnapshot{}
	err := DB.Where(
		"channel_id = ? AND usage_date = ? AND key_fingerprint = ?",
		channelId,
		usageDate,
		keyFingerprint,
	).First(snapshot).Error
	return snapshot, err
}

func GetLatestChannelProfitSnapshot(channelId int, keyFingerprint string) (*ChannelProfitSnapshot, error) {
	snapshot := &ChannelProfitSnapshot{}
	err := DB.Where("channel_id = ? AND key_fingerprint = ?", channelId, keyFingerprint).
		Order("usage_date DESC").
		First(snapshot).Error
	return snapshot, err
}

func SaveChannelProfitSnapshot(snapshot *ChannelProfitSnapshot) error {
	return DB.Save(snapshot).Error
}

func ListChannelProfitSnapshots(usageDate string) ([]*ChannelProfitSnapshot, error) {
	snapshots := make([]*ChannelProfitSnapshot, 0)
	err := DB.Where("usage_date = ?", usageDate).
		Order("channel_id ASC, id ASC").
		Find(&snapshots).Error
	return snapshots, err
}

func SumChannelNetQuota(startTime int64, endTime int64) (map[int]int64, error) {
	type channelQuota struct {
		ChannelId int   `gorm:"column:channel_id"`
		Quota     int64 `gorm:"column:quota"`
	}

	rows := make([]channelQuota, 0)
	err := LOG_DB.Model(&Log{}).
		Select(
			"channel_id, COALESCE(sum(CASE WHEN type = ? THEN quota ELSE -quota END), 0) AS quota",
			LogTypeConsume,
		).
		Where("type IN ? AND created_at >= ? AND created_at < ?", []int{LogTypeConsume, LogTypeRefund}, startTime, endTime).
		Where("channel_id <> 0").
		Group("channel_id").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	quotaByChannel := make(map[int]int64, len(rows))
	for _, row := range rows {
		quotaByChannel[row.ChannelId] = row.Quota
	}
	return quotaByChannel, nil
}
