package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryParamPriorityRetry(t *testing.T) {
	tests := []struct {
		name          string
		retry         int
		affinityMatch bool
		affinityUsed  bool
		group         string
		priority      int64
		wantRetry     int
		wantSkipped   *int64
	}{
		{name: "initial selection", retry: 0, group: "default", wantRetry: 0},
		{name: "ordinary retry", retry: 1, group: "default", wantRetry: 1},
		{name: "affinity cache miss", retry: 1, group: "default", affinityMatch: true, wantRetry: 1},
		{name: "first retry after affinity hit", retry: 1, group: "default", priority: 2, affinityMatch: true, affinityUsed: true, wantRetry: 0, wantSkipped: common.GetPointer(int64(2))},
		{name: "second retry after affinity hit", retry: 2, group: "default", priority: 2, affinityMatch: true, affinityUsed: true, wantRetry: 1, wantSkipped: common.GetPointer(int64(2))},
		{name: "third retry after affinity hit", retry: 3, group: "default", priority: 2, affinityMatch: true, affinityUsed: true, wantRetry: 2, wantSkipped: common.GetPointer(int64(2))},
		{name: "different auto group restarts at highest priority", retry: 1, group: "other", priority: 2, affinityMatch: true, affinityUsed: true, wantRetry: 0},
		{name: "affinity group remains skipped after auto retry reset", retry: 0, group: "default", priority: 2, affinityMatch: true, affinityUsed: true, wantRetry: 0, wantSkipped: common.GetPointer(int64(2))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			if tt.affinityMatch {
				setChannelAffinityContext(ctx, channelAffinityMeta{
					RuleName:   "test-rule",
					UsingGroup: "default",
					ModelName:  "test-model",
				})
			}
			if tt.affinityUsed {
				MarkChannelAffinityUsed(ctx, "default", 123, tt.priority)
			}

			param := &RetryParam{
				Ctx:   ctx,
				Retry: &tt.retry,
			}

			gotRetry, gotSkipped := param.getPriorityRetry(tt.group)
			require.Equal(t, tt.wantRetry, gotRetry)
			require.Equal(t, tt.wantSkipped, gotSkipped)
		})
	}
}

func TestAutoGroupRetryAfterAffinityHit(t *testing.T) {
	const (
		firstGroup    = "affinity-auto-first"
		affinityGroup = "affinity-auto-second"
		restartModel  = "affinity-auto-restart-model"
		fallbackModel = "affinity-auto-fallback-model"
	)
	channelIDs := []int{930001, 930002, 930003, 930004}
	previousAutoGroups := setting.AutoGroups2JsonString()
	previousUsableGroups := setting.UserUsableGroups2JSONString()
	previousGroupRatios := ratio_setting.GroupRatio2JSONString()
	previousMemoryCacheEnabled := common.MemoryCacheEnabled

	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	require.NoError(t, model.DB.Where("id IN ?", channelIDs).Delete(&model.Channel{}).Error)
	require.NoError(t, model.DB.Where("channel_id IN ?", channelIDs).Delete(&model.Ability{}).Error)
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroupRatios))
		require.NoError(t, model.DB.Where("channel_id IN ?", channelIDs).Delete(&model.Ability{}).Error)
		require.NoError(t, model.DB.Where("id IN ?", channelIDs).Delete(&model.Channel{}).Error)
		model.InitChannelCache()
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
	})

	common.MemoryCacheEnabled = true
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["affinity-auto-first","affinity-auto-second"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"affinity-auto-first":"first","affinity-auto-second":"second"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"affinity-auto-first":1,"affinity-auto-second":1}`))

	priorityHigh := int64(2)
	priorityNext := int64(1)
	weight := uint(100)
	channels := []model.Channel{
		{Id: 930001, Name: "auto-first-high", Key: "test", Status: common.ChannelStatusEnabled, Models: restartModel, Group: firstGroup, Priority: &priorityHigh, Weight: &weight},
		{Id: 930002, Name: "auto-affinity-high", Key: "test", Status: common.ChannelStatusEnabled, Models: restartModel, Group: affinityGroup, Priority: &priorityHigh, Weight: &weight},
		{Id: 930003, Name: "auto-fallback-high", Key: "test", Status: common.ChannelStatusEnabled, Models: fallbackModel, Group: affinityGroup, Priority: &priorityHigh, Weight: &weight},
		{Id: 930004, Name: "auto-fallback-next", Key: "test", Status: common.ChannelStatusEnabled, Models: fallbackModel, Group: affinityGroup, Priority: &priorityNext, Weight: &weight},
	}
	require.NoError(t, model.DB.Create(&channels).Error)
	abilities := []model.Ability{
		{Group: firstGroup, Model: restartModel, ChannelId: 930001, Enabled: true, Priority: &priorityHigh, Weight: weight},
		{Group: affinityGroup, Model: restartModel, ChannelId: 930002, Enabled: true, Priority: &priorityHigh, Weight: weight},
		{Group: affinityGroup, Model: fallbackModel, ChannelId: 930003, Enabled: true, Priority: &priorityHigh, Weight: weight},
		{Group: affinityGroup, Model: fallbackModel, ChannelId: 930004, Enabled: true, Priority: &priorityNext, Weight: weight},
	}
	require.NoError(t, model.DB.Create(&abilities).Error)
	model.InitChannelCache()

	t.Run("restarts from highest priority in first auto group", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		common.SetContextKey(ctx, constant.ContextKeyUserGroup, firstGroup)
		setChannelAffinityContext(ctx, channelAffinityMeta{RuleName: "test-rule", UsingGroup: "auto", ModelName: restartModel})
		MarkChannelAffinityUsed(ctx, affinityGroup, 930002, priorityHigh)
		retry := 1

		channel, selectedGroup, err := CacheGetRandomSatisfiedChannel(&RetryParam{
			Ctx: ctx, TokenGroup: "auto", ModelName: restartModel, Retry: &retry,
		})

		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, firstGroup, selectedGroup)
		assert.Equal(t, 930001, channel.Id)
	})

	t.Run("skips affinity priority after falling through to its group", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		common.SetContextKey(ctx, constant.ContextKeyUserGroup, firstGroup)
		setChannelAffinityContext(ctx, channelAffinityMeta{RuleName: "test-rule", UsingGroup: "auto", ModelName: fallbackModel})
		MarkChannelAffinityUsed(ctx, affinityGroup, 930003, priorityHigh)
		retry := 1

		channel, selectedGroup, err := CacheGetRandomSatisfiedChannel(&RetryParam{
			Ctx: ctx, TokenGroup: "auto", ModelName: fallbackModel, Retry: &retry,
		})

		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, affinityGroup, selectedGroup)
		assert.Equal(t, 930004, channel.Id)
	})
}
