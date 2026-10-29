package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerfMetricsRespectUserGroupsAndRecentModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initialDB, path := newAuditTestDatabase(t, "sqlite", "")
	require.NoError(t, initialDB.AutoMigrate(&model.Option{}))
	initialSQLDB, err := initialDB.DB()
	require.NoError(t, err)
	require.NoError(t, initialSQLDB.Close())
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousPath, previousMaster := common.SQLitePath, common.IsMasterNode
	t.Setenv("SQL_DSN", "local")
	t.Setenv("LOG_SQL_DSN", "")
	common.SQLitePath, common.IsMasterNode = path, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.SQLitePath, common.IsMasterNode = previousPath, previousMaster
	})
	require.NoError(t, model.InitDB())
	db := model.DB
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.PerfMetric{}))
	previousUsable := setting.UserUsableGroups2JSONString()
	previousRatios := ratio_setting.GroupRatio2JSONString()
	previousAuto := setting.AutoGroups2JsonString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAuto))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"merge-visible":"Visible"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"merge-visible":1,"merge-hidden":1}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	hour := time.Now().Unix() / 3600 * 3600
	for _, row := range []model.PerfMetric{
		{ModelName: "merge-current", Group: "merge-visible", BucketTs: hour, RequestCount: 1, SuccessCount: 1},
		{ModelName: "merge-current", Group: "merge-hidden", BucketTs: hour, RequestCount: 1},
		{ModelName: "merge-history", Group: "merge-visible", BucketTs: hour - 72*3600, RequestCount: 1, SuccessCount: 1},
		{ModelName: "merge-history", Group: "merge-hidden", BucketTs: hour - 72*3600, RequestCount: 1},
		{ModelName: "merge-hidden-only", Group: "merge-hidden", BucketTs: hour - 72*3600, RequestCount: 1},
	} {
		require.NoError(t, db.Create(&row).Error)
	}

	t.Run("summary history excludes inaccessible groups", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Set("user_group", "merge-visible")
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/perf_metrics/summary?recent_model=merge-history&recent_model=merge-hidden-only", nil)
		GetPerfMetricsSummary(ctx)
		require.Equal(t, http.StatusOK, recorder.Code)
		var response struct {
			Success bool                         `json:"success"`
			Data    perfmetrics.SummaryAllResult `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		require.True(t, response.Success)
		require.NotNil(t, response.Data.Summary)
		assert.Equal(t, 100.0, response.Data.Summary.SuccessRate)
		require.Len(t, response.Data.Models, 2)
		modelNames := make([]string, 0, len(response.Data.Models))
		for _, summary := range response.Data.Models {
			modelNames = append(modelNames, summary.ModelName)
			assert.Equal(t, 100.0, summary.SuccessRate)
		}
		assert.ElementsMatch(t, []string{"merge-current", "merge-history"}, modelNames)
		require.Len(t, response.Data.RecentSuccessSeries["merge-history"], 1)
		assert.Equal(t, 100.0, response.Data.RecentSuccessSeries["merge-history"][0].SuccessRate)
		assert.NotContains(t, response.Data.RecentSuccessSeries, "merge-hidden-only")
	})

	t.Run("detail summary and series exclude inaccessible groups", func(t *testing.T) {
		for _, group := range []string{"", "merge-hidden"} {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Set("user_group", "merge-visible")
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/perf_metrics?model=merge-current&group="+url.QueryEscape(group), nil)
			GetPerfMetrics(ctx)
			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool                    `json:"success"`
				Data    perfmetrics.QueryResult `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.True(t, response.Success)
			if group != "" {
				assert.Nil(t, response.Data.Summary)
				assert.Empty(t, response.Data.Series)
				assert.Empty(t, response.Data.Groups)
				continue
			}
			require.NotNil(t, response.Data.Summary)
			assert.Equal(t, 100.0, response.Data.Summary.SuccessRate)
			require.Len(t, response.Data.Series, 1)
			assert.Equal(t, 100.0, response.Data.Series[0].SuccessRate)
			require.Len(t, response.Data.Groups, 1)
			assert.Equal(t, "merge-visible", response.Data.Groups[0].Group)
		}
	})

	t.Run("oversized recent model list is rejected", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/perf_metrics/summary?"+strings.Repeat("recent_model=merge-history&", perfmetrics.MaxRecentModels+1), nil)
		GetPerfMetricsSummary(ctx)
		assert.Equal(t, http.StatusBadRequest, recorder.Code)
	})
}
