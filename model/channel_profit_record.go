package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
)

// Costs live in the primary DB, independently of the configured log database.
type ChannelProfitRecord struct {
	ID                string   `json:"id" gorm:"primaryKey;type:varchar(36)"`
	RequestID         string   `json:"-" gorm:"type:varchar(64);index"`
	UpstreamRequestID string   `json:"-" gorm:"type:varchar(128);index:idx_profit_match,priority:2;index:idx_profit_scope_request,priority:2"`
	ChannelID         int      `json:"-" gorm:"index;index:idx_profit_match,priority:1"`
	KeyFingerprint    string   `json:"-" gorm:"type:varchar(64)"`
	Scope             string   `json:"-" gorm:"type:varchar(64);index:idx_profit_scope_request,priority:1"`
	CreatedAt         int64    `json:"-" gorm:"index"`
	RevenueUSD        float64  `json:"revenue_usd"`
	CostUSD           float64  `json:"cost_usd"`
	EstimatedUSD      *float64 `json:"estimated_usd,omitempty"`
	CostFactor        float64  `json:"cost_factor"`
	CostMode          string   `json:"cost_mode" gorm:"type:varchar(16)"`
	UpstreamRatio     *float64 `json:"upstream_ratio,omitempty"`
	Status            string   `json:"status" gorm:"type:varchar(16);index"`
	Source            string   `json:"source" gorm:"type:varchar(32)"`
	Reason            string   `json:"reason,omitempty" gorm:"type:varchar(64)"`
	// Retain legacy columns so upgrades preserve historical financial records.
	Reconciliation string `json:"-" gorm:"type:varchar(32)"`
	MatchedAt      int64  `json:"-"`
}

type ChannelProfitCoverage struct {
	Total      int64   `json:"total"`
	Estimated  int64   `json:"estimated"`
	Matched    int64   `json:"matched"`
	Unknown    int64   `json:"unknown"`
	RevenueUSD float64 `json:"revenue_usd"`
	CostUSD    float64 `json:"cost_usd"`
}

type ChannelProfitPricing struct {
	ID            string `gorm:"primaryKey;type:varchar(64)"`
	PricingJSON   string `gorm:"type:text"`
	QuotaPerUnit  float64
	LastAttemptAt int64
	UpdatedAt     int64  `gorm:"autoUpdateTime:false"`
	LastError     string `gorm:"type:text"`
}

type ChannelProfitKeyState struct {
	ID        string `gorm:"primaryKey;type:varchar(64)"`
	SiteID    string `gorm:"type:varchar(64);index"`
	Ratio     *float64
	UpdatedAt int64  `gorm:"autoUpdateTime:false"`
	LastError string `gorm:"type:text"`
}

type ChannelProfitRequestSnapshot struct {
	Config         ChannelProfitConfig
	SiteID         string
	Scope          string
	KeyFingerprint string
	Pricing        *ChannelProfitPricing
	KeyState       *ChannelProfitKeyState
	Reason         string
}

// The service owns pricing expressions; the model owns persistence. Registered
// at service initialization, before the server can accept any relay requests.
var EstimateChannelProfitCost func(*ChannelProfitRequestSnapshot, *Log, *LogOther) (float64, string, string)

var profitConfigCache sync.Map
var profitPricingCache sync.Map
var profitKeyCache sync.Map

type cachedProfitConfig struct {
	Config  ChannelProfitConfig
	Expires time.Time
}
type cachedProfitPricing struct {
	Pricing ChannelProfitPricing
	Expires time.Time
}
type cachedProfitKey struct {
	State   ChannelProfitKeyState
	Expires time.Time
}

func ChannelProfitCostMode(config ChannelProfitConfig) string {
	if config.CostMode != "" {
		return config.CostMode
	}
	if config.RequestCostUSD != nil {
		return "request"
	}
	return "ratio"
}

func InvalidateChannelProfitConfig(channelID int) { profitConfigCache.Delete(channelID) }

func ProfitCostFingerprint(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func ProfitCostSiteID(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/v1")
	return ProfitCostFingerprint(strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host) + path)
}

// Snapshot config and pricing once per attempt. No upstream HTTP on this path.
func CaptureChannelProfitConfig(c *gin.Context, channelID int) {
	if c == nil {
		return
	}
	snapshot := &ChannelProfitRequestSnapshot{Reason: "configuration_unavailable"}
	c.Set("channel_profit_request_config", snapshot)
	if DB == nil || channelID <= 0 {
		return
	}
	if cached, ok := profitConfigCache.Load(channelID); ok && time.Now().Before(cached.(cachedProfitConfig).Expires) {
		snapshot.Config = cached.(cachedProfitConfig).Config
	} else {
		if err := DB.Where("channel_id = ?", channelID).Limit(1).Find(&snapshot.Config).Error; err != nil {
			common.SysError("failed to load request cost configuration: " + err.Error())
			return
		}
		profitConfigCache.Store(channelID, cachedProfitConfig{snapshot.Config, time.Now().Add(30 * time.Second)})
	}
	if !snapshot.Config.Enabled {
		snapshot.Reason = "monitoring_disabled"
		return
	}
	snapshot.Reason = ""
	snapshot.KeyFingerprint = ProfitCostFingerprint(strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyChannelKey)))
	snapshot.SiteID = ProfitCostSiteID(common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl))
	snapshot.Scope = ProfitCostFingerprint(snapshot.SiteID + ":" + snapshot.KeyFingerprint)
	if snapshot.SiteID == "" || ChannelProfitCostMode(snapshot.Config) == "request" {
		return
	}
	var pricing ChannelProfitPricing
	if cached, ok := profitPricingCache.Load(snapshot.SiteID); ok && time.Now().Before(cached.(cachedProfitPricing).Expires) {
		pricing = cached.(cachedProfitPricing).Pricing
	} else {
		if err := DB.Where("id = ?", snapshot.SiteID).Limit(1).Find(&pricing).Error; err != nil {
			return
		}
		profitPricingCache.Store(snapshot.SiteID, cachedProfitPricing{pricing, time.Now().Add(30 * time.Second)})
	}
	var state ChannelProfitKeyState
	if cached, ok := profitKeyCache.Load(snapshot.Scope); ok && time.Now().Before(cached.(cachedProfitKey).Expires) {
		state = cached.(cachedProfitKey).State
	} else {
		if err := DB.Where("id = ?", snapshot.Scope).Limit(1).Find(&state).Error; err != nil {
			return
		}
		profitKeyCache.Store(snapshot.Scope, cachedProfitKey{state, time.Now().Add(30 * time.Second)})
	}
	// Automatic data expires. A manual ratio stays valid until explicitly changed.
	maxAge := min(max(int64(snapshot.Config.SyncIntervalMinutes*120), 900), 86400)
	now := time.Now().Unix()
	if pricing.UpdatedAt > 0 && now-pricing.UpdatedAt <= maxAge {
		snapshot.Pricing = &pricing
	}
	if state.UpdatedAt > 0 && now-state.UpdatedAt <= maxAge {
		snapshot.KeyState = &state
	}
}

func SaveChannelProfitPricing(pricing *ChannelProfitPricing) error {
	err := DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, UpdateAll: true}).Create(pricing).Error
	if err == nil {
		profitPricingCache.Delete(pricing.ID)
	}
	return err
}
func SaveChannelProfitKeyState(state *ChannelProfitKeyState) error {
	err := DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, UpdateAll: true}).Create(state).Error
	if err == nil {
		profitKeyCache.Delete(state.ID)
	}
	return err
}

func recordChannelProfit(c *gin.Context, log *Log, other *LogOther) {
	if violation, _ := other.Snapshot()["violation_fee"].(bool); violation {
		return
	}
	value, _ := c.Get("channel_profit_request_config")
	snapshot, ok := value.(*ChannelProfitRequestSnapshot)
	if !ok {
		other.SetAdmin("upstream_cost", map[string]any{"status": "unknown", "reason": "configuration_unavailable"})
		return
	}
	if snapshot.Reason != "" || snapshot.Config.ChannelId != log.ChannelId {
		reason := snapshot.Reason
		if reason == "" {
			reason = "channel_changed"
		}
		other.SetAdmin("upstream_cost", map[string]any{"status": "unknown", "reason": reason})
		return
	}
	config := snapshot.Config
	factor := config.CostFactor
	if factor <= 0 {
		factor = 1
	}
	record := &ChannelProfitRecord{
		ID: uuid.NewString(), RequestID: log.RequestId,
		ChannelID: log.ChannelId, KeyFingerprint: snapshot.KeyFingerprint, Scope: snapshot.Scope, CreatedAt: log.CreatedAt,
		RevenueUSD: float64(log.Quota) / common.QuotaPerUnit, CostFactor: factor, CostMode: ChannelProfitCostMode(config), Status: "unknown",
	}
	record.UpstreamRatio = config.ManualRatio
	if record.UpstreamRatio == nil && snapshot.KeyState != nil {
		record.UpstreamRatio = snapshot.KeyState.Ratio
	}
	cost, source, reason := 0.0, "", "pricing_unavailable"
	if record.CostMode == "request" {
		if config.RequestCostUSD != nil {
			cost, source, reason = *config.RequestCostUSD*factor, "fixed_request", ""
		} else {
			reason = "fixed_cost_missing"
		}
	} else if EstimateChannelProfitCost != nil {
		cost, source, reason = EstimateChannelProfitCost(snapshot, log, other)
	}
	if reason == "" && (cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0)) {
		reason = "invalid_cost"
	}
	record.Source, record.Reason = source, reason
	if reason == "" {
		record.CostUSD = cost
		record.EstimatedUSD = &cost
		record.Status = "estimated"
	}
	if err := DB.Create(record).Error; err != nil {
		common.SysError("failed to persist request cost " + record.ID + ": " + err.Error())
		other.SetAdmin("upstream_cost", map[string]any{"status": "unknown", "reason": "recording_failed", "recording_failed": true})
		return
	}
	other.SetAdmin("upstream_cost", record)
}

func RefreshChannelProfitLogCosts(logs []*Log) error {
	// Preserve all existing integer/JSON values when replacing only cost metadata.
	ids := make([]string, 0, len(logs))
	metadata := make([]map[string]json.RawMessage, len(logs))
	admins := make([]map[string]json.RawMessage, len(logs))
	recordIDs := make([]string, len(logs))
	for i, log := range logs {
		if common.UnmarshalJsonStr(log.Other, &metadata[i]) != nil {
			continue
		}
		if common.Unmarshal(metadata[i]["admin_info"], &admins[i]) != nil {
			continue
		}
		var cost struct {
			ID string `json:"id"`
		}
		if common.Unmarshal(admins[i]["upstream_cost"], &cost) == nil && cost.ID != "" {
			ids = append(ids, cost.ID)
			recordIDs[i] = cost.ID
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
		if record, ok := byID[recordIDs[i]]; ok {
			encoded, err := common.Marshal(record)
			if err != nil {
				return err
			}
			admins[i]["upstream_cost"] = encoded
			encoded, err = common.Marshal(admins[i])
			if err != nil {
				return err
			}
			metadata[i]["admin_info"] = encoded
			encoded, err = common.Marshal(metadata[i])
			if err != nil {
				return err
			}
			log.Other = string(encoded)
		}
	}
	return nil
}

func ChannelProfitRequestCoverageByChannel(channelIDs []int, start, end int64) (map[int]ChannelProfitCoverage, error) {
	var rows []struct {
		ChannelID int
		Status    string
		Count     int64
		Revenue   float64
		Cost      float64
	}
	err := DB.Model(&ChannelProfitRecord{}).Select("channel_id, status, COUNT(*) AS count, COALESCE(SUM(revenue_usd),0) AS revenue, COALESCE(SUM(cost_usd),0) AS cost").Where("channel_id IN ? AND created_at >= ? AND created_at < ?", channelIDs, start, end).Group("channel_id, status").Scan(&rows).Error
	coverages := make(map[int]ChannelProfitCoverage, len(rows))
	for _, row := range rows {
		coverage := coverages[row.ChannelID]
		coverage.Total += row.Count
		coverage.RevenueUSD += row.Revenue
		switch row.Status {
		case "matched":
			coverage.Matched += row.Count
		case "estimated":
			coverage.Estimated += row.Count
		default:
			coverage.Unknown += row.Count
			coverages[row.ChannelID] = coverage
			continue
		}
		coverage.CostUSD += row.Cost
		coverages[row.ChannelID] = coverage
	}
	return coverages, err
}
