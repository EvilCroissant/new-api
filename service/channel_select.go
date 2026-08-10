package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type RetryParam struct {
	Ctx                 *gin.Context
	TokenGroup          string
	ModelName           string
	RequestPath         string
	Retry               *int
	failedChannelIDs    map[int]struct{}
	lastFailedChannelID int
	lastFailedPriority  int64
	lastFailedGroup     string
	resetNextTry        bool
}

func (p *RetryParam) GetRetry() int {
	if p.Retry == nil {
		return 0
	}
	return *p.Retry
}

func (p *RetryParam) getPriorityRetry(group string) (int, *int64) {
	retry := p.GetRetry()
	selection, ok := getChannelAffinitySelection(p.Ctx)
	if !ok {
		return retry, nil
	}
	if retry > 0 {
		retry--
	}
	if selection.Group != group {
		return retry, nil
	}
	return retry, &selection.Priority
}

func (p *RetryParam) SetRetry(retry int) {
	p.Retry = &retry
}

func (p *RetryParam) IncreaseRetry() {
	if p.resetNextTry {
		p.resetNextTry = false
		return
	}
	if p.Retry == nil {
		p.Retry = new(int)
	}
	*p.Retry++
}

func (p *RetryParam) ResetRetryNextTry() {
	p.resetNextTry = true
}

func (p *RetryParam) MarkChannelFailed(channel *model.Channel) {
	if channel == nil {
		return
	}
	if p.failedChannelIDs == nil {
		p.failedChannelIDs = make(map[int]struct{})
	}
	p.failedChannelIDs[channel.Id] = struct{}{}
	p.lastFailedChannelID = channel.Id
	p.lastFailedPriority = channel.GetPriority()
	p.lastFailedGroup = p.TokenGroup
	if p.TokenGroup == "auto" {
		p.lastFailedGroup = common.GetContextKeyString(p.Ctx, constant.ContextKeyAutoGroup)
	}
}

func (p *RetryParam) selectRetryChannel(group string) (*model.Channel, bool, error) {
	if p.lastFailedChannelID <= 0 || p.lastFailedGroup != group {
		return nil, false, nil
	}
	// RetryTimes is the highest retry index accepted by the relay loop. The
	// current selection therefore still has one attempt when retry == RetryTimes.
	remainingRetries := common.RetryTimes - p.GetRetry() + 1
	channel, err := model.GetRandomSatisfiedChannelForRetry(
		group,
		p.ModelName,
		p.lastFailedPriority,
		remainingRetries,
		p.RequestPath,
		p.failedChannelIDs,
	)
	return channel, true, err
}

// CacheGetRandomSatisfiedChannel selects the initial channel or the next retry
// channel. Once a retry leaves a priority, it only moves to the immediately
// next lower available priority; it never skips an intermediate priority.
func CacheGetRandomSatisfiedChannel(param *RetryParam) (*model.Channel, string, error) {
	var channel *model.Channel
	var err error
	selectGroup := param.TokenGroup
	userGroup := common.GetContextKeyString(param.Ctx, constant.ContextKeyUserGroup)
	if param.TokenGroup == "auto" {
		autoGroups := GetRequestAutoGroups(param.Ctx, userGroup)
		if len(autoGroups) == 0 {
			return nil, selectGroup, errors.New("auto groups is not enabled")
		}

		// startGroupIndex: the group index to start searching from
		// startGroupIndex: 开始搜索的分组索引
		startGroupIndex := 0
		crossGroupRetry := common.GetContextKeyBool(param.Ctx, constant.ContextKeyTokenCrossGroupRetry)

		if lastGroupIndex, exists := common.GetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex); exists {
			if idx, ok := lastGroupIndex.(int); ok {
				startGroupIndex = idx
			}
		}

		for i := startGroupIndex; i < len(autoGroups); i++ {
			autoGroup := autoGroups[i]
			retryBudgetIndex := param.GetRetry()
			retrySelection := false
			channel, retrySelection, _ = param.selectRetryChannel(autoGroup)
			if !retrySelection {
				priorityRetry, skippedPriority := param.getPriorityRetry(autoGroup)
				if i > startGroupIndex {
					priorityRetry = 0
					retryBudgetIndex = 0
				}
				channel, _ = model.GetRandomSatisfiedChannelSkippingPriorityAndChannels(
					autoGroup,
					param.ModelName,
					priorityRetry,
					param.RequestPath,
					skippedPriority,
					param.failedChannelIDs,
				)
			}
			logger.LogDebug(param.Ctx, "Auto selecting group: %s, retry: %d", autoGroup, param.GetRetry())

			if channel == nil {
				logger.LogDebug(param.Ctx, "No untried channel in group %s for model %s, trying next group", autoGroup, param.ModelName)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex, 0)
				param.SetRetry(0)
				continue
			}
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
			selectGroup = autoGroup
			logger.LogDebug(param.Ctx, "Auto selected group: %s", autoGroup)

			if crossGroupRetry && retryBudgetIndex >= common.RetryTimes {
				logger.LogDebug(param.Ctx, "Current group %s retries exhausted (retry=%d >= RetryTimes=%d), preparing switch to next group", autoGroup, retryBudgetIndex, common.RetryTimes)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				param.SetRetry(0)
				param.ResetRetryNextTry()
			} else {
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i)
			}
			break
		}
	} else {
		var retrySelection bool
		channel, retrySelection, err = param.selectRetryChannel(param.TokenGroup)
		if !retrySelection {
			priorityRetry, skippedPriority := param.getPriorityRetry(param.TokenGroup)
			channel, err = model.GetRandomSatisfiedChannelSkippingPriorityAndChannels(param.TokenGroup, param.ModelName, priorityRetry, param.RequestPath, skippedPriority, param.failedChannelIDs)
		}
		if err != nil {
			return nil, param.TokenGroup, err
		}
	}
	return channel, selectGroup, nil
}
