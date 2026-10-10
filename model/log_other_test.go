package model

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestStructuredLogsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "logs.db"))
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
			logsDB, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			// Keep the primary database separate to exercise LOG_SQL_DSN behavior.
			usersDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "users.db")), &gorm.Config{})
			require.NoError(t, err)
			previousDB, previousLogDB := DB, LOG_DB
			previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
			previousConsume, previousExport, previousRedis := common.LogConsumeEnabled, common.DataExportEnabled, common.RedisEnabled
			DB, LOG_DB = usersDB, logsDB
			common.SetMainDatabaseType(common.DatabaseTypeSQLite)
			common.SetLogDatabaseType(common.DatabaseType(dialect))
			common.LogConsumeEnabled, common.DataExportEnabled, common.RedisEnabled = true, false, false
			initCol()
			t.Cleanup(func() {
				DB, LOG_DB = previousDB, previousLogDB
				common.SetMainDatabaseType(previousMainType)
				common.SetLogDatabaseType(previousLogType)
				common.LogConsumeEnabled, common.DataExportEnabled, common.RedisEnabled = previousConsume, previousExport, previousRedis
				initCol()
				for _, db := range []*gorm.DB{usersDB, logsDB} {
					sqlDB, err := db.DB()
					require.NoError(t, err)
					require.NoError(t, sqlDB.Close())
				}
			})
			var version string
			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			require.NoError(t, logsDB.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)
			require.NoError(t, usersDB.AutoMigrate(&User{}))
			for range 2 {
				require.NoError(t, usersDB.AutoMigrate(&ChannelProfitConfig{}, &ChannelProfitRecord{}))
			}
			enabled, costFactor, requestCost := true, 1.5, 0.2
			_, err = UpdateChannelProfitConfigs([]int{182}, ChannelProfitConfigUpdate{
				Enabled: &enabled, CostFactor: &costFactor, RequestCostUSD: &requestCost,
			})
			require.NoError(t, err)
			require.NoError(t, usersDB.Create(&User{Id: 910001, Username: "merge-log-user", Setting: "{}"}).Error)
			require.NoError(t, logsDB.AutoMigrate(&Log{}))
			legacy := Log{UserId: 910001, Type: LogTypeConsume, Content: "旧日志原文", Other: `{"frt":6600}`, RequestId: "merge-legacy"}
			require.NoError(t, logsDB.Create(&legacy).Error)
			t.Cleanup(func() { require.NoError(t, logsDB.Where("user_id = ?", 910001).Delete(&Log{}).Error) })
			// The persisted schema is unchanged; existing rows survive repeated startup migrations.
			for range 2 {
				require.NoError(t, logsDB.AutoMigrate(&Log{}))
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			c.Set("username", "merge-log-user")
			c.Set(common.RequestIdKey, "merge-structured")
			CaptureChannelProfitConfig(c, 182)
			other := NewLogOther()
			other.SetPublic("frt", 6600)
			other.SetPublic("stream_status", map[string]any{"status": "error", "end_reason": "client_gone", "end_error": "context canceled", "upstream_error": "at capacity"})
			other.SetAdmin("channel_affinity", map[string]any{"frt_optimization": map[string]any{"from_channel_id": 165, "to_channel_id": 182}})
			RecordConsumeLog(c, 910001, RecordConsumeLogParams{ChannelId: 182, ModelName: "test-model", Quota: 12, Other: other,
				Content: []*common.Message{common.NewMessage("Model {{model}}", map[string]any{"model": "test-model"})}})
			var stored Log
			require.NoError(t, logsDB.Where("request_id = ?", "merge-structured").First(&stored).Error)
			assert.Contains(t, stored.Other, "frt_optimization")
			var profitRecord ChannelProfitRecord
			require.NoError(t, usersDB.Where("request_id = ?", "merge-structured").First(&profitRecord).Error)
			assert.Equal(t, "estimated", profitRecord.Status)
			assert.InDelta(t, 0.3, profitRecord.CostUSD, 0.000001)
			coverage, err := ChannelProfitRequestCoverage([]int{182}, stored.CreatedAt, stored.CreatedAt+1)
			require.NoError(t, err)
			assert.EqualValues(t, 1, coverage.Estimated)
			assert.InDelta(t, 0.3, coverage.CostUSD, 0.000001)
			rows, total, err := GetUserLogs(910001, LogTypeUnknown, 0, 0, "", "", 0, 10, "", "", "")
			require.NoError(t, err)
			require.EqualValues(t, 2, total)
			require.Len(t, rows, 2)
			assert.Equal(t, "Model test-model", rows[0].Content)
			assert.Equal(t, 12, rows[0].Quota)
			var public map[string]any
			require.NoError(t, common.UnmarshalJsonStr(rows[0].Other, &public))
			assert.Contains(t, public, "content_parts")
			assert.EqualValues(t, 6600, public["frt"])
			assert.Equal(t, "at capacity", public["stream_status"].(map[string]any)["upstream_error"])
			assert.NotContains(t, public, "admin_info")
			require.NoError(t, RefreshChannelProfitLogCosts([]*Log{&stored}))
			var rootOther map[string]any
			require.NoError(t, common.UnmarshalJsonStr(stored.Other, &rootOther))
			admin := rootOther["admin_info"].(map[string]any)
			assert.Equal(t, "estimated", admin["upstream_cost"].(map[string]any)["status"])
			assert.Equal(t, legacy.Content, rows[1].Content)
			assert.JSONEq(t, legacy.Other, rows[1].Other)
		})
	}
}

func TestLogOtherScopesAndMerges(t *testing.T) {
	var other LogOther

	assert.True(t, other.SetPublic("request_path", "/v1/chat/completions"))
	other.MergePublic(map[string]any{
		"zero": 0,
	})
	assert.True(t, other.SetAdmin("use_channel", []string{"channel-a"}))
	other.MergeAdmin(map[string]any{
		"rejected": false,
	})
	assert.True(t, other.SetRoot("upstream_request_id", "upstream-private"))
	other.MergeRoot(map[string]any{
		"generation": 0,
	})
	assert.True(t, other.SetAudit("method", "POST"))
	other.MergeAudit(map[string]any{
		"success": false,
	})

	require.JSONEq(t, `{
		"request_path": "/v1/chat/completions",
		"zero": 0,
		"admin_info": {
			"use_channel": ["channel-a"],
			"rejected": false
		},
		"root_info": {
			"upstream_request_id": "upstream-private",
			"generation": 0
		},
		"audit_info": {
			"method": "POST",
			"success": false
		}
	}`, other.JSONString())
}

func TestLogOtherRejectsSensitivePublicFields(t *testing.T) {
	other := NewLogOther()

	for _, key := range []string{
		"admin_info",
		"root_info",
		"audit_info",
		"channel_id",
		"channel_name",
		"channel_type",
		"reject_reason",
	} {
		assert.False(t, other.SetPublic(key, "must-not-leak"), key)
	}
	other.MergePublic(map[string]any{
		"request_path": "/v1/responses",
		"channel_name": "still-must-not-leak",
		"admin_info":   map[string]any{"secret": true},
	})

	require.JSONEq(t, `{"request_path":"/v1/responses"}`, other.JSONString())
	require.JSONEq(t, `{}`, NewLogOther().JSONString())
}

func TestLogOtherJSONStringDoesNotMutateReceiver(t *testing.T) {
	other := NewLogOther()
	require.True(t, other.SetPublic("request_path", "/v1/chat/completions"))
	require.True(t, other.SetAdmin("rejected", false))

	before := other.Snapshot()
	first := other.JSONString()
	after := other.Snapshot()
	second := other.JSONString()

	require.Equal(t, before, after)
	require.Equal(t, first, second)
}
