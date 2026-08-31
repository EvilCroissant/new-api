package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
)

func GetChannelConstraints(c *gin.Context) *dto.ChannelConstraints {
	if c == nil {
		return &dto.ChannelConstraints{}
	}
	if existing, ok := common.GetContextKeyType[*dto.ChannelConstraints](c, constant.ContextKeyChannelConstraints); ok && existing != nil {
		return existing
	}
	constraints := &dto.ChannelConstraints{}
	common.SetContextKey(c, constant.ContextKeyChannelConstraints, constraints)
	return constraints
}

func AppendTaskPluginIdentityFilter(c *gin.Context, pluginKey string) {
	if c == nil {
		return
	}
	GetChannelConstraints(c).AddFilter(dto.ChannelFilter{
		Kind:                   dto.FilterTaskPluginIdentity,
		TaskPluginKey:          pluginKey,
		TaskPluginChannelTypes: pinnedTaskPluginChannelTypes(c, pluginKey),
	})
}

func channelSelectionFilters(c *gin.Context, requestPath string) []dto.ChannelFilter {
	filters := append([]dto.ChannelFilter(nil), GetChannelConstraints(c).Filters...)
	for _, filter := range filters {
		if filter.Kind == dto.FilterRequestPath {
			return filters
		}
	}
	if requestPath != "" {
		filters = append(filters, dto.ChannelFilter{
			Kind:        dto.FilterRequestPath,
			RequestPath: requestPath,
		})
	}
	return filters
}

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

func (p *RetryParam) selectRetryChannel(group string, filters []dto.ChannelFilter) (*model.Channel, bool, error) {
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
		filters,
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
	filters := channelSelectionFilters(param.Ctx, param.RequestPath)
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
			channel, retrySelection, _ = param.selectRetryChannel(autoGroup, filters)
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
					filters,
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
		channel, retrySelection, err = param.selectRetryChannel(param.TokenGroup, filters)
		if !retrySelection {
			priorityRetry, skippedPriority := param.getPriorityRetry(param.TokenGroup)
			channel, err = model.GetRandomSatisfiedChannelSkippingPriorityAndChannels(param.TokenGroup, param.ModelName, priorityRetry, filters, skippedPriority, param.failedChannelIDs)
		}
		if err != nil {
			return nil, param.TokenGroup, err
		}
	}
	return channel, selectGroup, nil
}

func pinnedTaskPluginChannelTypes(c *gin.Context, expected string) []int {
	if c == nil || expected == "" {
		return nil
	}
	if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
		pinned, ok := value.(jsplugin.PinnedEndpoint)
		if ok && pinned.Generation != nil && len(pinned.Candidates) > 1 {
			expectedFound := false
			channelTypes := make([]int, 0, len(pinned.Candidates))
			seen := make(map[int]struct{}, len(pinned.Candidates))
			for _, candidate := range pinned.Candidates {
				if candidate.Plugin == nil {
					continue
				}
				if candidate.Plugin.Meta.Key == expected {
					expectedFound = true
				}
				for _, channelType := range candidate.Plugin.Meta.ChannelTypes {
					if channelType == 0 || channelType == constant.ChannelTypeTaskPlugin {
						continue
					}
					if _, duplicate := seen[channelType]; duplicate {
						continue
					}
					if plugin, indexed := pinned.Generation.GetByChannelType(channelType); indexed && plugin == candidate.Plugin {
						seen[channelType] = struct{}{}
						channelTypes = append(channelTypes, channelType)
					}
				}
			}
			if expectedFound {
				return channelTypes
			}
		}
	}
	value, exists := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, ok := value.(jsplugin.PinnedPlugin)
	if !exists || !ok || pinned.Generation == nil || pinned.Plugin == nil || pinned.Plugin.Meta.Key != expected {
		return nil
	}
	channelTypes := make([]int, 0, len(pinned.Plugin.Meta.ChannelTypes))
	for _, channelType := range pinned.Plugin.Meta.ChannelTypes {
		if channelType == 0 || channelType == constant.ChannelTypeTaskPlugin {
			continue
		}
		channelTypes = append(channelTypes, channelType)
	}
	if len(channelTypes) == 0 {
		return nil
	}
	return channelTypes
}
