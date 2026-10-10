package model

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ChannelProfitRecord lives in the main database, so reconciling costs never
// requires updates to ClickHouse or a join across the main and log databases.
type ChannelProfitRecord struct {
	ID                string  `json:"id" gorm:"primaryKey;type:varchar(36)"`
	RequestID         string  `json:"-" gorm:"type:varchar(64);index"`
	UpstreamRequestID string  `json:"-" gorm:"type:varchar(128);index:idx_profit_match,priority:2"`
	ChannelID         int     `json:"-" gorm:"index;index:idx_profit_match,priority:1"`
	KeyFingerprint    string  `json:"-" gorm:"type:varchar(64)"`
	CreatedAt         int64   `json:"-" gorm:"index"`
	RevenueUSD        float64 `json:"revenue_usd"`
	CostUSD           float64 `json:"cost_usd"`
	CostFactor        float64 `json:"cost_factor"`
	Status            string  `json:"status" gorm:"type:varchar(16);index"`
}

type ChannelProfitCoverage struct {
	Total      int64   `json:"total"`
	Estimated  int64   `json:"estimated"`
	Unknown    int64   `json:"unknown"`
	RevenueUSD float64 `json:"revenue_usd"`
	CostUSD    float64 `json:"cost_usd"`
}

var profitConfigCache sync.Map

type cachedProfitConfig struct {
	Config  ChannelProfitConfig
	Expires time.Time
}

func InvalidateChannelProfitConfig(channelID int) { profitConfigCache.Delete(channelID) }

// CaptureChannelProfitConfig freezes the configured purchase price for this
// attempt. No upstream I/O is performed on the request path.
func CaptureChannelProfitConfig(c *gin.Context, channelID int) {
	if c == nil || DB == nil || channelID <= 0 {
		return
	}
	var config ChannelProfitConfig
	if cached, ok := profitConfigCache.Load(channelID); ok && time.Now().Before(cached.(cachedProfitConfig).Expires) {
		config = cached.(cachedProfitConfig).Config
	} else {
		result := DB.Where("channel_id = ?", channelID).Limit(1).Find(&config)
		if result.Error != nil {
			common.SysError("failed to load request cost configuration: " + result.Error.Error())
			return
		}
		profitConfigCache.Store(channelID, cachedProfitConfig{config, time.Now().Add(30 * time.Second)})
	}
	c.Set("channel_profit_request_config", config)
}

func recordChannelProfit(c *gin.Context, log *Log, other *LogOther) {
	value, ok := c.Get("channel_profit_request_config")
	config, valid := value.(ChannelProfitConfig)
	if !ok || !valid || !config.Enabled || config.ChannelId != log.ChannelId {
		return
	}
	if violation, _ := other.Snapshot()["violation_fee"].(bool); violation {
		return
	}
	factor := config.CostFactor
	if factor <= 0 {
		factor = 1
	}
	key := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	hash := sha256.Sum256([]byte(key))
	record := &ChannelProfitRecord{
		ID: uuid.NewString(), RequestID: log.RequestId, UpstreamRequestID: log.UpstreamRequestId,
		ChannelID: log.ChannelId, KeyFingerprint: hex.EncodeToString(hash[:]), CreatedAt: log.CreatedAt,
		RevenueUSD: float64(log.Quota) / common.QuotaPerUnit, CostFactor: factor, Status: "unknown",
	}
	if config.RequestCostUSD != nil {
		record.CostUSD = *config.RequestCostUSD * factor
		if record.CostUSD >= 0 && !math.IsNaN(record.CostUSD) && !math.IsInf(record.CostUSD, 0) {
			record.Status = "estimated"
		} else {
			record.CostUSD = 0
		}
	}
	// Persist synchronously instead of dropping accounting records in a bounded
	// in-memory queue. A failed write is explicitly represented in the log.
	if err := DB.Create(record).Error; err != nil {
		common.SysError("failed to persist request cost " + record.ID + ": " + err.Error())
		other.SetAdmin("upstream_cost", map[string]any{"status": "unknown", "recording_failed": true})
		return
	}
	other.SetAdmin("upstream_cost", record)
}

func RefreshChannelProfitLogCosts(logs []*Log) error {
	ids := make([]string, 0, len(logs))
	metadata := make([]map[string]any, len(logs))
	for i, log := range logs {
		if common.UnmarshalJsonStr(log.Other, &metadata[i]) != nil {
			continue
		}
		admin, _ := metadata[i]["admin_info"].(map[string]any)
		cost, _ := admin["upstream_cost"].(map[string]any)
		id, _ := cost["id"].(string)
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var records []ChannelProfitRecord
	if err := DB.Where("id IN ?", ids).Find(&records).Error; err != nil {
		return err
	}
	byID := make(map[string]ChannelProfitRecord, len(records))
	for _, record := range records {
		byID[record.ID] = record
	}
	for i, log := range logs {
		admin, _ := metadata[i]["admin_info"].(map[string]any)
		cost, _ := admin["upstream_cost"].(map[string]any)
		id, _ := cost["id"].(string)
		if record, ok := byID[id]; ok {
			admin["upstream_cost"] = record
			encoded, err := common.Marshal(metadata[i])
			if err != nil {
				return err
			}
			log.Other = string(encoded)
		}
	}
	return nil
}

func ChannelProfitRequestCoverage(channelIDs []int, start, end int64) (ChannelProfitCoverage, error) {
	var rows []struct {
		Status  string
		Count   int64
		Revenue float64
		Cost    float64
	}
	err := DB.Model(&ChannelProfitRecord{}).Select("status, COUNT(*) AS count, COALESCE(SUM(revenue_usd),0) AS revenue, COALESCE(SUM(cost_usd),0) AS cost").Where("channel_id IN ? AND created_at >= ? AND created_at < ?", channelIDs, start, end).Group("status").Scan(&rows).Error
	var coverage ChannelProfitCoverage
	for _, row := range rows {
		coverage.Total += row.Count
		switch row.Status {
		case "estimated":
			coverage.Estimated += row.Count
		default:
			coverage.Unknown += row.Count
			continue
		}
		coverage.RevenueUSD += row.Revenue
		coverage.CostUSD += row.Cost
	}
	return coverage, err
}
