package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestChannelProfitUsageEstimates(t *testing.T) {
	ratio := 0.07
	config := model.ChannelProfitConfig{ManualRatio: &ratio, CostFactor: 1}
	for _, tc := range []struct {
		name          string
		pricing       model.Pricing
		input, output int
		values        map[string]any
		want          float64
		reason        string
	}{
		{name: "anthropic cache is additional to input", pricing: model.Pricing{BillingMode: "tiered_expr", BillingExpr: `tier("base", p*4+c*20+cr*0.2+cc*5+cc1h*8)`}, input: 30461, output: 44, values: map[string]any{"usage_semantic": "anthropic", "cache_tokens": 30709, "cache_creation_tokens": 248, "cache_creation_tokens_5m": 248}, want: 0.009107406},
		{name: "openai input includes cache", pricing: model.Pricing{BillingMode: "tiered_expr", BillingExpr: `tier("base", p*4+c*20+cr*0.2)`}, input: 1000, output: 100, values: map[string]any{"cache_tokens": 900}, want: 0.0001806},
		{name: "tier uses full context including cache", pricing: model.Pricing{BillingMode: "tiered_expr", BillingExpr: `len>1000 ? tier("long",p*8+cr*0.4) : tier("short",p*4+cr*0.2)`}, input: 100, values: map[string]any{"usage_semantic": "anthropic", "cache_tokens": 1000}, want: 0.000084},
		{name: "cache ttl split not double counted", pricing: model.Pricing{BillingMode: "tiered_expr", BillingExpr: `tier("base",p*4+cc*5+cc1h*8)`}, input: 100, values: map[string]any{"usage_semantic": "anthropic", "cache_creation_tokens": 1000, "cache_write_tokens": 1000, "cache_creation_tokens_5m": 200, "cache_creation_tokens_1h": 800}, want: 0.000546},
		{name: "legacy anthropic input preserved", pricing: model.Pricing{ModelRatio: 2, CompletionRatio: 5}, input: 100, output: 10, values: map[string]any{"usage_semantic": "anthropic", "cache_tokens": 200}, want: 0.000098},
		{name: "request probes cannot be guessed", pricing: model.Pricing{BillingMode: "tiered_expr", BillingExpr: `tier("x",p*4)|||when(header("x") has "fast")*2`}, input: 100, reason: "pricing_unsupported"},
		{name: "tasks are not token prices", pricing: model.Pricing{BillingMode: "tiered_expr", BillingExpr: `tier("base",p*4)`}, values: map[string]any{"is_task": true}, reason: "usage_unsupported"},
		{name: "image batches cannot use a single request price", pricing: model.Pricing{QuotaType: 1, ModelPrice: 0.04}, values: map[string]any{"image_count": 2}, reason: "usage_unsupported"},
		{name: "negative usage is unknown", pricing: model.Pricing{ModelRatio: 2}, input: -1, reason: "invalid_usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := common.Marshal(map[string]model.Pricing{"upstream-model": tc.pricing})
			require.NoError(t, err)
			snapshot := &model.ChannelProfitRequestSnapshot{Config: config, Pricing: &model.ChannelProfitPricing{PricingJSON: string(data), QuotaPerUnit: 500000}}
			other := model.NewLogOther()
			other.MergePublic(tc.values)
			other.SetPublic("upstream_model_name", "upstream-model")
			cost, _, reason := estimateChannelProfitCost(snapshot, &model.Log{ModelName: "alias", PromptTokens: tc.input, CompletionTokens: tc.output}, other)
			assert.Equal(t, tc.reason, reason)
			assert.InDelta(t, tc.want, cost, 1e-12)
		})
	}
	t.Run("fallback uses actual special ratio", func(t *testing.T) {
		other := model.NewLogOther()
		other.SetPublic("group_ratio", 0.5)
		other.SetPublic("user_group_ratio", 0.1)
		cost, source, reason := estimateChannelProfitCost(&model.ChannelProfitRequestSnapshot{Config: config}, &model.Log{Quota: 5000}, other)
		assert.Equal(t, "", reason)
		assert.Equal(t, "local_ratio_fallback", source)
		assert.InDelta(t, 0.007, cost, 1e-12)
		zero := 0.0
		config.ManualRatio = &zero
		cost, _, reason = estimateChannelProfitCost(&model.ChannelProfitRequestSnapshot{Config: config}, &model.Log{Quota: 5000}, other)
		assert.Equal(t, "", reason)
		assert.Zero(t, cost)
		config.ManualRatio = nil
		_, _, reason = estimateChannelProfitCost(&model.ChannelProfitRequestSnapshot{Config: config}, &model.Log{}, other)
		assert.Equal(t, "ratio_unavailable", reason)
	})
}

// These are the persisted shapes shipped in v1.0.0-rc.42.2. Upgrade tests create
// representative data through that schema before running current migrations.
type profitReleasedConfig struct {
	CostFactor          float64
	RequestCostUSD      *float64
	Id                  int
	ChannelId           int `gorm:"uniqueIndex;not null"`
	Enabled             bool
	DisplayName         string `gorm:"type:varchar(100)"`
	SyncIntervalMinutes int
	LastSyncAttemptAt   int64  `gorm:"bigint;index"`
	AccessToken         string `gorm:"type:text"`
	CreatedAt           int64  `gorm:"autoCreateTime"`
	UpdatedAt           int64  `gorm:"autoUpdateTime"`
}
type profitReleasedRecord struct {
	ID                string `gorm:"primaryKey;type:varchar(36)"`
	RequestID         string `gorm:"type:varchar(64);index"`
	UpstreamRequestID string `gorm:"type:varchar(128);index:idx_profit_match,priority:2"`
	ChannelID         int    `gorm:"index;index:idx_profit_match,priority:1"`
	KeyFingerprint    string `gorm:"type:varchar(64)"`
	CreatedAt         int64  `gorm:"index"`
	RevenueUSD        float64
	CostUSD           float64
	CostFactor        float64
	Status            string `gorm:"type:varchar(16);index"`
}

func TestChannelProfitDatabaseAndReconciliation(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, upgrade := range []bool{false, true} {
				t.Run(fmt.Sprintf("upgrade_%t", upgrade), func(t *testing.T) {
					var driver gorm.Dialector
					switch dialect {
					case "sqlite":
						driver = sqlite.Open(filepath.Join(t.TempDir(), "profit.db"))
					case "mysql":
						dsn := os.Getenv("TEST_MYSQL_DSN")
						if dsn == "" {
							t.Skip("TEST_MYSQL_DSN not configured")
						}
						driver = mysql.Open(dsn)
					case "postgres":
						dsn := os.Getenv("TEST_POSTGRES_DSN")
						if dsn == "" {
							t.Skip("TEST_POSTGRES_DSN not configured")
						}
						driver = postgres.Open(dsn)
					}
					prefix := fmt.Sprintf("profit_%d_", time.Now().UnixNano())
					db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: prefix}})
					require.NoError(t, err)
					sqlDB, err := db.DB()
					require.NoError(t, err)
					sqlDB.SetMaxOpenConns(1)
					logDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "separate-logs.db")), &gorm.Config{})
					require.NoError(t, err)
					previousDB, previousLogDB := model.DB, model.LOG_DB
					previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
					previousConsume, previousRedis, previousExport := common.LogConsumeEnabled, common.RedisEnabled, common.DataExportEnabled
					model.DB, model.LOG_DB = db, logDB
					common.SetMainDatabaseType(common.DatabaseType(dialect))
					common.SetLogDatabaseType(common.DatabaseTypeSQLite)
					common.LogConsumeEnabled, common.RedisEnabled, common.DataExportEnabled = true, false, false
					tables := []any{&model.ChannelProfitConfig{}, &model.ChannelProfitRecord{}, &model.ChannelProfitPricing{}, &model.ChannelProfitKeyState{}, &model.User{}}
					t.Cleanup(func() {
						for _, table := range tables {
							require.NoError(t, db.Migrator().DropTable(table))
						}
						model.DB, model.LOG_DB = previousDB, previousLogDB
						common.SetMainDatabaseType(previousMain)
						common.SetLogDatabaseType(previousLog)
						common.LogConsumeEnabled, common.RedisEnabled, common.DataExportEnabled = previousConsume, previousRedis, previousExport
						require.NoError(t, sqlDB.Close())
						logsSQL, e := logDB.DB()
						require.NoError(t, e)
						require.NoError(t, logsSQL.Close())
					})
					var version string
					query := "SELECT version()"
					if dialect == "sqlite" {
						query = "SELECT sqlite_version()"
					}
					require.NoError(t, db.Raw(query).Scan(&version).Error)
					t.Log(version)
					if upgrade {
						require.NoError(t, db.Table(prefix+"channel_profit_configs").AutoMigrate(&profitReleasedConfig{}))
						require.NoError(t, db.Table(prefix+"channel_profit_records").AutoMigrate(&profitReleasedRecord{}))
						fixed := 0.2
						require.NoError(t, db.Table(prefix+"channel_profit_configs").Create(&profitReleasedConfig{ChannelId: 920183, Enabled: true, CostFactor: 1.5, RequestCostUSD: &fixed}).Error)
						require.NoError(t, db.Table(prefix+"channel_profit_records").Create(&profitReleasedRecord{ID: "legacy", ChannelID: 920183, RevenueUSD: 1, CostUSD: 0.3, CostFactor: 1.5, Status: "estimated"}).Error)
					}
					for range 2 {
						require.NoError(t, db.AutoMigrate(tables...))
					}
					if upgrade {
						var record model.ChannelProfitRecord
						require.NoError(t, db.First(&record, "id = ?", "legacy").Error)
						assert.Equal(t, 0.3, record.CostUSD)
						var config model.ChannelProfitConfig
						require.NoError(t, db.Where("channel_id = ?", 920183).First(&config).Error)
						assert.Equal(t, "request", model.ChannelProfitCostMode(config))
					}
					require.NoError(t, logDB.AutoMigrate(&model.Log{}))
					require.NoError(t, db.Create(&model.User{Id: 920140, Username: "profit-test", Setting: "{}"}).Error)
					upstreamCost := 1000.0
					logFailed, conflicting := false, false
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						switch r.URL.Path {
						case "/api/status":
							fmt.Fprint(w, `{"success":true,"data":{"quota_per_unit":500000}}`)
						case "/api/pricing":
							fmt.Fprint(w, `{"success":true,"data":[{"model_name":"test-model","billing_mode":"tiered_expr","billing_expr":"tier(\"base\",p*4+c*20+cr*0.2)"}]}`)
						case "/api/log/token":
							if logFailed {
								w.WriteHeader(http.StatusBadGateway)
								return
							}
							if conflicting {
								fmt.Fprint(w, `{"success":true,"data":[{"type":2,"quota":1,"request_id":"upstream-one"},{"type":2,"quota":2,"request_id":"upstream-one"}]}`)
								return
							}
							if r.Header.Get("Authorization") != "Bearer test-key" {
								w.WriteHeader(401)
								return
							}
							fmt.Fprintf(w, `{"success":true,"data":[{"type":2,"quota":%g,"request_id":"upstream-one","created_at":%d,"other":"{\"group_ratio\":0.1}"}]}`, upstreamCost, time.Now().Unix())
						default:
							w.WriteHeader(404)
						}
					}))
					defer upstream.Close()
					enabled, ratio, factor, mode := true, 0.1, 2.0, "ratio"
					_, err = model.UpdateChannelProfitConfigs([]int{920182}, model.ChannelProfitConfigUpdate{Enabled: &enabled, CostMode: &mode, ManualRatio: &ratio, CostFactor: &factor})
					require.NoError(t, err)
					require.Error(t, db.Create(&model.ChannelProfitConfig{ChannelId: 920182}).Error, "channel uniqueness must survive migration")
					group := &channelProfitGroup{BaseURL: upstream.URL, Keys: []*channelProfitGroupKey{{Value: "test-key"}}}
					require.NoError(t, syncChannelProfitRequestCosts(context.Background(), group, upstream.Client()))
					ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
					ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
					ctx.Set("username", "profit-test")
					ctx.Set(common.RequestIdKey, "local-one")
					ctx.Set(common.UpstreamRequestIdKey, "upstream-one")
					common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, upstream.URL)
					common.SetContextKey(ctx, constant.ContextKeyChannelKey, "test-key")
					model.CaptureChannelProfitConfig(ctx, 920182)
					// Changing config while a request is in flight must not reprice it.
					changedFactor := 5.0
					_, err = model.UpdateChannelProfitConfigs([]int{920182}, model.ChannelProfitConfigUpdate{CostFactor: &changedFactor})
					require.NoError(t, err)
					other := model.NewLogOther()
					other.SetPublic("group_ratio", 0.13)
					other.SetPublic("cache_tokens", 900)
					model.RecordConsumeLog(ctx, 920140, model.RecordConsumeLogParams{ChannelId: 920182, ModelName: "test-model", PromptTokens: 1000, CompletionTokens: 100, Quota: 5000, Other: other})
					var stored model.Log
					require.NoError(t, logDB.Where("request_id = ?", "local-one").First(&stored).Error)
					var record model.ChannelProfitRecord
					require.NoError(t, db.Where("request_id = ?", "local-one").First(&record).Error)
					assert.Equal(t, "estimated", record.Status)
					assert.Equal(t, "upstream_pricing", record.Source)
					assert.InDelta(t, 0.000516, record.CostUSD, 1e-12)
					assert.Equal(t, 2.0, record.CostFactor)
					require.NoError(t, syncChannelProfitRequestCosts(context.Background(), group, upstream.Client()))
					require.NoError(t, syncChannelProfitRequestCosts(context.Background(), group, upstream.Client()))
					require.NoError(t, db.First(&record, "id = ?", record.ID).Error)
					assert.Equal(t, "matched", record.Status)
					assert.InDelta(t, 0.004, record.CostUSD, 1e-12)
					stored.Other = fmt.Sprintf(`{"large_number":9007199254740993,"admin_info":{"upstream_cost":{"id":%q}}}`, record.ID)
					require.NoError(t, model.RefreshChannelProfitLogCosts([]*model.Log{&stored}))
					assert.Contains(t, stored.Other, `"large_number":9007199254740993`)
					assert.Contains(t, stored.Other, `"status":"matched"`)
					// A late sibling splits the single upstream charge instead of charging it twice.
					sibling := record
					sibling.ID = "late"
					sibling.RequestID = "local-two"
					sibling.Status = "estimated"
					sibling.CostUSD = 0
					require.NoError(t, db.Create(&sibling).Error)
					// Same upstream ID under another key must never be matched.
					unrelated := record
					unrelated.ID = "other-key"
					unrelated.Scope = "another-key"
					unrelated.Status = "unknown"
					unrelated.CostUSD = 0
					require.NoError(t, db.Create(&unrelated).Error)
					require.NoError(t, syncChannelProfitRequestCosts(context.Background(), group, upstream.Client()))
					require.NoError(t, db.First(&record, "id = ?", record.ID).Error)
					assert.InDelta(t, 0.002, record.CostUSD, 1e-12)
					require.NoError(t, db.First(&sibling, "id = ?", "late").Error)
					assert.InDelta(t, 0.002, sibling.CostUSD, 1e-12)
					require.NoError(t, db.First(&unrelated, "id = ?", "other-key").Error)
					assert.Equal(t, "unknown", unrelated.Status)
					coverages, err := model.ChannelProfitRequestCoverageByChannel([]int{920182}, 0, time.Now().Unix()+1)
					require.NoError(t, err)
					coverage := coverages[920182]
					assert.EqualValues(t, 3, coverage.Total)
					assert.EqualValues(t, 2, coverage.Matched)
					assert.EqualValues(t, 1, coverage.Unknown)
					assert.InDelta(t, 0.004, coverage.CostUSD, 1e-12)
					upstreamCost = 0
					require.NoError(t, syncChannelProfitRequestCosts(context.Background(), group, upstream.Client()))
					require.NoError(t, db.First(&record, "id = ?", record.ID).Error)
					assert.Equal(t, "matched", record.Status)
					assert.Zero(t, record.CostUSD)
					zero := 0.0
					_, err = model.UpdateChannelProfitConfigs([]int{920182}, model.ChannelProfitConfigUpdate{ManualRatio: &zero})
					require.NoError(t, err)
					var config model.ChannelProfitConfig
					require.NoError(t, db.Where("channel_id = ?", 920182).First(&config).Error)
					require.NotNil(t, config.ManualRatio)
					assert.Zero(t, *config.ManualRatio)
					_, err = model.UpdateChannelProfitConfigs([]int{920182}, model.ChannelProfitConfigUpdate{ClearManualRatio: true})
					require.NoError(t, err)
					config = model.ChannelProfitConfig{}
					require.NoError(t, db.Where("channel_id = ?", 920182).First(&config).Error)
					assert.Nil(t, config.ManualRatio)
					// Neither an upstream failure nor conflicting IDs can overwrite a matched charge.
					logFailed = true
					require.Error(t, syncChannelProfitRequestCosts(context.Background(), group, upstream.Client()))
					logFailed, conflicting = false, true
					require.Error(t, syncChannelProfitRequestCosts(context.Background(), group, upstream.Client()))
					require.NoError(t, db.First(&record, "id = ?", record.ID).Error)
					assert.Equal(t, "matched", record.Status)
					assert.Zero(t, record.CostUSD)
					conflicting = false
					var pricing model.ChannelProfitPricing
					require.NoError(t, db.First(&pricing, "id = ?", model.ProfitCostSiteID(upstream.URL)).Error)
					assert.Equal(t, "conflicting upstream request costs", pricing.LastError)
					var keyState model.ChannelProfitKeyState
					require.NoError(t, db.First(&keyState, "id = ?", record.Scope).Error)
					keyState.UpdatedAt = time.Now().Add(-48 * time.Hour).Unix()
					pricing.UpdatedAt = keyState.UpdatedAt
					require.NoError(t, model.SaveChannelProfitKeyState(&keyState))
					require.NoError(t, model.SaveChannelProfitPricing(&pricing))
					ctx.Set(common.RequestIdKey, "stale-no-id")
					ctx.Set(common.UpstreamRequestIdKey, "")
					model.CaptureChannelProfitConfig(ctx, 920182)
					model.RecordConsumeLog(ctx, 920140, model.RecordConsumeLogParams{ChannelId: 920182, ModelName: "test-model", Quota: 5000, Other: model.NewLogOther()})
					var unknown model.ChannelProfitRecord
					require.NoError(t, db.Where("request_id = ?", "stale-no-id").First(&unknown).Error)
					assert.Equal(t, "unknown", unknown.Status)
					assert.Equal(t, "ratio_unavailable", unknown.Reason)
					assert.Equal(t, "missing_request_id", unknown.Reconciliation)
					assert.Nil(t, unknown.EstimatedUSD)
					// A fixed zero is an explicit configuration and never waits for reconciliation.
					fixedMode := "request"
					_, err = model.UpdateChannelProfitConfigs([]int{920182}, model.ChannelProfitConfigUpdate{CostMode: &fixedMode, RequestCostUSD: &zero})
					require.NoError(t, err)
					ctx.Set(common.RequestIdKey, "fixed-zero")
					model.CaptureChannelProfitConfig(ctx, 920182)
					model.RecordConsumeLog(ctx, 920140, model.RecordConsumeLogParams{ChannelId: 920182, Quota: 5000, Other: model.NewLogOther()})
					var fixed model.ChannelProfitRecord
					require.NoError(t, db.Where("request_id = ?", "fixed-zero").First(&fixed).Error)
					assert.Equal(t, "estimated", fixed.Status)
					assert.Equal(t, "fixed_request", fixed.Source)
					assert.Equal(t, "not_applicable", fixed.Reconciliation)
					require.NotNil(t, fixed.EstimatedUSD)
					assert.Zero(t, *fixed.EstimatedUSD)
				})
			}
		})
	}
}
