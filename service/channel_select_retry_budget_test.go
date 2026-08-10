package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type retryBudgetChannel struct {
	id       int
	priority int64
}

func seedRetryBudgetChannels(t *testing.T, group, modelName string, channels []retryBudgetChannel) {
	t.Helper()
	weight := uint(100)
	for _, candidate := range channels {
		priority := candidate.priority
		channel := &model.Channel{
			Id:       candidate.id,
			Type:     constant.ChannelTypeOpenAI,
			Name:     "retry-budget-test",
			Key:      "test",
			Status:   common.ChannelStatusEnabled,
			Models:   modelName,
			Group:    group,
			Priority: &priority,
			Weight:   &weight,
		}
		require.NoError(t, model.DB.Create(channel).Error)
		require.NoError(t, model.DB.Create(&model.Ability{
			Group:     group,
			Model:     modelName,
			ChannelId: candidate.id,
			Enabled:   true,
			Priority:  &priority,
			Weight:    weight,
		}).Error)
	}
}

func runRetryBudgetSequence(t *testing.T, group, modelName string, firstFailedID int, retryTimes int) []*model.Channel {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	// The relay loop advances from the initial attempt (retry 0) to retry 1
	// before asking the selector for a replacement channel.
	retry := 1
	param := &RetryParam{
		Ctx:         ctx,
		TokenGroup:  group,
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Retry:       &retry,
	}
	firstFailed, err := model.CacheGetChannel(firstFailedID)
	require.NoError(t, err)
	param.MarkChannelFailed(firstFailed)

	selected := make([]*model.Channel, 0, retryTimes)
	for retry < retryTimes {
		channel, selectedGroup, err := CacheGetRandomSatisfiedChannel(param)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, group, selectedGroup)
		selected = append(selected, channel)
		param.MarkChannelFailed(channel)
		param.IncreaseRetry()
	}
	return selected
}

func TestRetryBudgetReservesLowerPriorities(t *testing.T) {
	const (
		groupName = "retry-budget-priority-test"
		modelName = "retry-budget-priority-model"
	)
	channels := []retryBudgetChannel{
		{id: 970001, priority: 10},
		{id: 970002, priority: 10},
		{id: 970003, priority: 9},
		{id: 970004, priority: 8},
	}

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	require.NoError(t, model.DB.Where("channel_id IN ?", []int{970001, 970002, 970003, 970004}).Delete(&model.Ability{}).Error)
	require.NoError(t, model.DB.Where("id IN ?", []int{970001, 970002, 970003, 970004}).Delete(&model.Channel{}).Error)
	seedRetryBudgetChannels(t, groupName, modelName, channels)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id IN ?", []int{970001, 970002, 970003, 970004}).Delete(&model.Ability{}).Error)
		require.NoError(t, model.DB.Where("id IN ?", []int{970001, 970002, 970003, 970004}).Delete(&model.Channel{}).Error)
		common.RetryTimes = originalRetryTimes
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		if originalMemoryCacheEnabled {
			model.InitChannelCache()
		}
	})
	common.RetryTimes = 3

	for _, memoryCacheEnabled := range []bool{true, false} {
		modeName := "database"
		if memoryCacheEnabled {
			modeName = "memory-cache"
		}
		t.Run(modeName, func(t *testing.T) {
			common.MemoryCacheEnabled = memoryCacheEnabled
			if memoryCacheEnabled {
				model.InitChannelCache()
			}
			selected := runRetryBudgetSequence(t, groupName, modelName, 970001, common.RetryTimes)
			require.Len(t, selected, 3)
			assert.Equal(t, []int64{10, 9, 8}, []int64{
				selected[0].GetPriority(),
				selected[1].GetPriority(),
				selected[2].GetPriority(),
			})
			assert.Equal(t, 970002, selected[0].Id)
		})
	}
}

func TestRetryBudgetKeepsPriorityOrderWhenBudgetRunsOut(t *testing.T) {
	const (
		groupName = "retry-budget-order-test"
		modelName = "retry-budget-order-model"
	)
	channelIDs := []int{974001, 974002, 974003, 974004}
	channels := []retryBudgetChannel{
		{id: 974001, priority: 10},
		{id: 974002, priority: 9},
		{id: 974003, priority: 8},
		{id: 974004, priority: 7},
	}

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	require.NoError(t, model.DB.Where("channel_id IN ?", channelIDs).Delete(&model.Ability{}).Error)
	require.NoError(t, model.DB.Where("id IN ?", channelIDs).Delete(&model.Channel{}).Error)
	seedRetryBudgetChannels(t, groupName, modelName, channels)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id IN ?", channelIDs).Delete(&model.Ability{}).Error)
		require.NoError(t, model.DB.Where("id IN ?", channelIDs).Delete(&model.Channel{}).Error)
		common.RetryTimes = originalRetryTimes
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		if originalMemoryCacheEnabled {
			model.InitChannelCache()
		}
	})
	common.RetryTimes = 2

	for _, memoryCacheEnabled := range []bool{true, false} {
		modeName := "database"
		if memoryCacheEnabled {
			modeName = "memory-cache"
		}
		t.Run(modeName, func(t *testing.T) {
			common.MemoryCacheEnabled = memoryCacheEnabled
			if memoryCacheEnabled {
				model.InitChannelCache()
			}
			selected := runRetryBudgetSequence(t, groupName, modelName, 974001, common.RetryTimes)
			require.Len(t, selected, 2)
			assert.Equal(t, []int64{9, 8}, []int64{
				selected[0].GetPriority(),
				selected[1].GetPriority(),
			})
		})
	}
}

func TestRetryBudgetAffinityOnlyMovesDown(t *testing.T) {
	const (
		groupName = "retry-budget-affinity-test"
		modelName = "retry-budget-affinity-model"
	)
	channelIDs := []int{971001, 971002, 971003, 971004, 971005}
	channels := []retryBudgetChannel{
		{id: 971001, priority: 10},
		{id: 971002, priority: 9},
		{id: 971003, priority: 9},
		{id: 971004, priority: 9},
		{id: 971005, priority: 8},
	}

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	require.NoError(t, model.DB.Where("channel_id IN ?", channelIDs).Delete(&model.Ability{}).Error)
	require.NoError(t, model.DB.Where("id IN ?", channelIDs).Delete(&model.Channel{}).Error)
	seedRetryBudgetChannels(t, groupName, modelName, channels)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id IN ?", channelIDs).Delete(&model.Ability{}).Error)
		require.NoError(t, model.DB.Where("id IN ?", channelIDs).Delete(&model.Channel{}).Error)
		common.RetryTimes = originalRetryTimes
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		if originalMemoryCacheEnabled {
			model.InitChannelCache()
		}
	})
	common.RetryTimes = 3

	for _, memoryCacheEnabled := range []bool{true, false} {
		modeName := "database"
		if memoryCacheEnabled {
			modeName = "memory-cache"
		}
		t.Run(modeName, func(t *testing.T) {
			common.MemoryCacheEnabled = memoryCacheEnabled
			if memoryCacheEnabled {
				model.InitChannelCache()
			}
			selected := runRetryBudgetSequence(t, groupName, modelName, 971002, common.RetryTimes)
			require.Len(t, selected, 3)
			assert.Equal(t, []int64{9, 9, 8}, []int64{
				selected[0].GetPriority(),
				selected[1].GetPriority(),
				selected[2].GetPriority(),
			})
			assert.NotEqual(t, selected[0].Id, selected[1].Id)
			assert.NotEqual(t, 971001, selected[0].Id)
			assert.NotEqual(t, 971001, selected[1].Id)
		})
	}
}

func TestRetryBudgetFlowsUnusedCapacityDown(t *testing.T) {
	const (
		groupName = "retry-budget-flow-test"
		modelName = "retry-budget-flow-model"
	)
	channelIDs := []int{972001, 972002, 972003, 972004}
	channels := []retryBudgetChannel{
		{id: 972001, priority: 9},
		{id: 972002, priority: 9},
		{id: 972003, priority: 8},
		{id: 972004, priority: 8},
	}

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	require.NoError(t, model.DB.Where("channel_id IN ?", channelIDs).Delete(&model.Ability{}).Error)
	require.NoError(t, model.DB.Where("id IN ?", channelIDs).Delete(&model.Channel{}).Error)
	seedRetryBudgetChannels(t, groupName, modelName, channels)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id IN ?", channelIDs).Delete(&model.Ability{}).Error)
		require.NoError(t, model.DB.Where("id IN ?", channelIDs).Delete(&model.Channel{}).Error)
		common.RetryTimes = originalRetryTimes
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		if originalMemoryCacheEnabled {
			model.InitChannelCache()
		}
	})
	common.RetryTimes = 3

	for _, memoryCacheEnabled := range []bool{true, false} {
		modeName := "database"
		if memoryCacheEnabled {
			modeName = "memory-cache"
		}
		t.Run(modeName, func(t *testing.T) {
			common.MemoryCacheEnabled = memoryCacheEnabled
			if memoryCacheEnabled {
				model.InitChannelCache()
			}
			selected := runRetryBudgetSequence(t, groupName, modelName, 972001, common.RetryTimes)
			require.Len(t, selected, 3)
			assert.Equal(t, []int64{9, 8, 8}, []int64{
				selected[0].GetPriority(),
				selected[1].GetPriority(),
				selected[2].GetPriority(),
			})
			assert.NotEqual(t, selected[1].Id, selected[2].Id)
		})
	}
}

func TestRetryBudgetStopsWhenEveryCandidateFailed(t *testing.T) {
	const (
		groupName = "retry-budget-exhausted-test"
		modelName = "retry-budget-exhausted-model"
	)
	channelIDs := []int{973001, 973002}
	channels := []retryBudgetChannel{
		{id: 973001, priority: 8},
		{id: 973002, priority: 8},
	}

	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	require.NoError(t, model.DB.Where("channel_id IN ?", channelIDs).Delete(&model.Ability{}).Error)
	require.NoError(t, model.DB.Where("id IN ?", channelIDs).Delete(&model.Channel{}).Error)
	seedRetryBudgetChannels(t, groupName, modelName, channels)
	t.Cleanup(func() {
		require.NoError(t, model.DB.Where("channel_id IN ?", channelIDs).Delete(&model.Ability{}).Error)
		require.NoError(t, model.DB.Where("id IN ?", channelIDs).Delete(&model.Channel{}).Error)
		common.RetryTimes = originalRetryTimes
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		if originalMemoryCacheEnabled {
			model.InitChannelCache()
		}
	})
	common.RetryTimes = 5

	for _, memoryCacheEnabled := range []bool{true, false} {
		modeName := "database"
		if memoryCacheEnabled {
			modeName = "memory-cache"
		}
		t.Run(modeName, func(t *testing.T) {
			common.MemoryCacheEnabled = memoryCacheEnabled
			if memoryCacheEnabled {
				model.InitChannelCache()
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			retry := 0
			param := &RetryParam{Ctx: ctx, TokenGroup: groupName, ModelName: modelName, Retry: &retry}
			firstFailed, err := model.CacheGetChannel(973001)
			require.NoError(t, err)
			param.MarkChannelFailed(firstFailed)

			selected, _, err := CacheGetRandomSatisfiedChannel(param)
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, 973002, selected.Id)
			param.MarkChannelFailed(selected)
			param.IncreaseRetry()

			selected, _, err = CacheGetRandomSatisfiedChannel(param)
			require.NoError(t, err)
			assert.Nil(t, selected)
		})
	}
}
