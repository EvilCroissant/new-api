package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShouldRetryHonorsPinRetryMode(t *testing.T) {
	openaiErr := types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)

	c := newPinRetryContext()
	assert.True(t, shouldRetry(c, openaiErr, 1))

	origin := newPinRetryContext()
	service.GetChannelConstraints(origin).AddPin(dto.ChannelPin{
		ChannelId: 2,
		Source:    dto.PinSourceOriginTask,
		Rank:      dto.PinRankOriginTask,
		RetryMode: dto.PinRetrySameChannel,
	})
	assert.True(t, shouldRetry(origin, openaiErr, 1), "origin pin retries on the same channel")

	token := newPinRetryContext()
	service.GetChannelConstraints(token).AddPin(dto.ChannelPin{
		ChannelId: 1,
		Source:    dto.PinSourceToken,
		Rank:      dto.PinRankToken,
		RetryMode: dto.PinRetrySingleAttempt,
	})
	assert.False(t, shouldRetry(token, openaiErr, 1), "token pin suppresses retry")
}

func TestShouldRetryTaskRelayHonorsPinRetryMode(t *testing.T) {
	taskErr := &dto.TaskError{StatusCode: http.StatusInternalServerError}

	c := newPinRetryContext()
	assert.True(t, shouldRetryTaskRelay(c, 1, taskErr, 1))

	origin := newPinRetryContext()
	service.GetChannelConstraints(origin).AddPin(dto.ChannelPin{
		ChannelId: 2,
		Source:    dto.PinSourceOriginTask,
		Rank:      dto.PinRankOriginTask,
		RetryMode: dto.PinRetrySameChannel,
	})
	assert.True(t, shouldRetryTaskRelay(origin, 2, taskErr, 1))

	token := newPinRetryContext()
	service.GetChannelConstraints(token).AddPin(dto.ChannelPin{
		ChannelId: 1,
		Source:    dto.PinSourceToken,
		Rank:      dto.PinRankToken,
		RetryMode: dto.PinRetrySingleAttempt,
	})
	assert.False(t, shouldRetryTaskRelay(token, 1, taskErr, 1))
}

func TestSameChannelPinsMergeToStricterRetryMode(t *testing.T) {
	c := newPinRetryContext()
	constraints := service.GetChannelConstraints(c)
	constraints.AddPin(dto.ChannelPin{
		ChannelId: 7,
		Source:    dto.PinSourceOriginTask,
		Rank:      dto.PinRankOriginTask,
		RetryMode: dto.PinRetrySameChannel,
	})
	constraints.AddPin(dto.ChannelPin{
		ChannelId: 7,
		Source:    dto.PinSourceToken,
		Rank:      dto.PinRankToken,
		RetryMode: dto.PinRetrySingleAttempt,
	})
	pin, found, overridden := constraints.ResolvedPin()
	require.True(t, found)
	assert.Equal(t, 7, pin.ChannelId)
	assert.Equal(t, dto.PinRetrySingleAttempt, pin.RetryMode)
	assert.Empty(t, overridden)
	assert.False(t, shouldRetry(c, types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError), 1))
}

func TestMarkFailedChannelForRetryExcludesFailedChannel(t *testing.T) {
	const (
		groupName = "controller-retry-channel-test"
		modelName = "controller-retry-channel-model"
	)
	channelIDs := []int{996001, 996002}
	priority := int64(10)
	weight := uint(100)
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = database
	model.LOG_DB = database

	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))
	for _, channelID := range channelIDs {
		require.NoError(t, model.DB.Create(&model.Channel{
			Id:       channelID,
			Type:     constant.ChannelTypeOpenAI,
			Name:     "retry-channel-test",
			Key:      "test",
			Status:   common.ChannelStatusEnabled,
			Models:   modelName,
			Group:    groupName,
			Priority: &priority,
			Weight:   &weight,
		}).Error)
		require.NoError(t, model.DB.Create(&model.Ability{
			Group:     groupName,
			Model:     modelName,
			ChannelId: channelID,
			Enabled:   true,
			Priority:  &priority,
			Weight:    weight,
		}).Error)
	}
	t.Cleanup(func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.RetryTimes = originalRetryTimes
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		sqlDB, closeErr := database.DB()
		if closeErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	common.MemoryCacheEnabled = false
	common.RetryTimes = 1

	c := newPinRetryContext()
	retry := 1
	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  groupName,
		ModelName:   modelName,
		RequestPath: c.Request.URL.Path,
		Retry:       &retry,
	}
	failed, err := model.GetChannelById(channelIDs[0], true)
	require.NoError(t, err)
	markFailedChannelForRetry(c, retryParam, failed, false)

	selected, selectedGroup, err := service.CacheGetRandomSatisfiedChannel(retryParam)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, groupName, selectedGroup)
	assert.Equal(t, channelIDs[1], selected.Id)
}

func newPinRetryContext() *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}
