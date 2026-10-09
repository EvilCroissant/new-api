package service

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
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
	channelTypes, pluginKeys := pinnedTaskPluginIdentities(c, pluginKey)
	GetChannelConstraints(c).AddFilter(dto.ChannelFilter{
		Kind:                   dto.FilterTaskPluginIdentity,
		TaskPluginKey:          pluginKey,
		TaskPluginChannelTypes: channelTypes,
		TaskPluginKeys:         pluginKeys,
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
	// Origin-task pins deliberately retry the same channel.
	if _, pinned, _ := GetChannelConstraints(p.Ctx).ResolvedPin(); pinned {
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

func (p *RetryParam) selectFRTRetryChannel() (*model.Channel, string, bool, error) {
	groups := []string{p.TokenGroup}
	startIndex := 0
	if p.TokenGroup == "auto" {
		groups = GetRequestAutoGroups(p.Ctx, common.GetContextKeyString(p.Ctx, constant.ContextKeyUserGroup))
		if index, exists := common.GetContextKey(p.Ctx, constant.ContextKeyAutoGroupIndex); exists {
			if index, ok := index.(int); ok && index >= 0 && index < len(groups) {
				startIndex = index
				groups = groups[index:]
			}
		}
	}
	for i, group := range groups {
		candidates, err := model.GetSatisfiedChannels(group, p.ModelName, channelSelectionFilters(p.Ctx, p.RequestPath))
		if err != nil {
			return nil, group, false, err
		}
		available := make([]*model.Channel, 0, len(candidates))
		for _, candidate := range candidates {
			if _, failed := p.failedChannelIDs[candidate.Id]; failed {
				continue
			}
			available = append(available, candidate)
		}
		if len(available) == 0 {
			continue
		}
		scope := channelAffinityFRTScopeForSelection(p.Ctx, group, p.ModelName)
		fast, unknown, slow := channelAffinityFRTGlobalCandidates(scope, available, time.Now())
		if chosen := channelAffinityFRTChooseLowest(fast); chosen != nil {
			p.setFRTRetryAutoGroup(group, startIndex+i)
			return chosen.channel, group, true, nil
		}
		if len(unknown) > 0 {
			chosen, err := chooseChannelAffinityFRTUnknown(p.Ctx, scope, candidates, unknown)
			if chosen != nil {
				p.setFRTRetryAutoGroup(group, startIndex+i)
			}
			return chosen, group, chosen != nil, err
		}
		if chosen := channelAffinityFRTChooseLowest(slow); chosen != nil {
			p.setFRTRetryAutoGroup(group, startIndex+i)
			return chosen.channel, group, true, nil
		}
	}
	return nil, p.TokenGroup, false, nil
}

func (p *RetryParam) setFRTRetryAutoGroup(group string, index int) {
	if p.TokenGroup != "auto" {
		return
	}
	common.SetContextKey(p.Ctx, constant.ContextKeyAutoGroup, group)
	common.SetContextKey(p.Ctx, constant.ContextKeyAutoGroupIndex, index)
}

// CacheGetRandomSatisfiedChannel selects the initial channel or the next retry
// channel. Once a retry leaves a priority, it only moves to the immediately
// next lower available priority; it never skips an intermediate priority.
func CacheGetRandomSatisfiedChannel(param *RetryParam) (*model.Channel, string, error) {
	if param != nil && len(param.failedChannelIDs) > 0 {
		setting := operation_setting.GetChannelAffinitySetting()
		if setting != nil && setting.Enabled && setting.FRTOptimizationEnabled {
			channel, group, selected, err := param.selectFRTRetryChannel()
			if selected || err != nil {
				return channel, group, err
			}
		}
	}
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

func pinnedTaskPluginIdentities(c *gin.Context, expected string) ([]int, []string) {
	if c == nil || expected == "" {
		return nil, nil
	}
	if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
		pinned, ok := value.(jsplugin.PinnedEndpoint)
		if ok && pinned.Generation != nil && len(pinned.Candidates) > 1 {
			expectedFound := false
			channelTypes := make([]int, 0, len(pinned.Candidates))
			pluginKeys := make([]string, 0, len(pinned.Candidates))
			seen := make(map[int]struct{}, len(pinned.Candidates))
			for _, candidate := range pinned.Candidates {
				if candidate.Plugin == nil {
					continue
				}
				if candidate.Plugin.Meta.Key == expected {
					expectedFound = true
				}
				pluginKeys = append(pluginKeys, candidate.Plugin.Meta.Key)
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
				return channelTypes, pluginKeys
			}
		}
	}
	value, exists := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, ok := value.(jsplugin.PinnedPlugin)
	if !exists || !ok || pinned.Generation == nil || pinned.Plugin == nil || pinned.Plugin.Meta.Key != expected {
		return nil, nil
	}
	channelTypes := make([]int, 0, len(pinned.Plugin.Meta.ChannelTypes))
	for _, channelType := range pinned.Plugin.Meta.ChannelTypes {
		if channelType == 0 || channelType == constant.ChannelTypeTaskPlugin {
			continue
		}
		channelTypes = append(channelTypes, channelType)
	}
	return channelTypes, []string{expected}
}

// ChannelSelectError explains why SelectChannelForRequest found no channel.
// Callers render it for their transport: the HTTP distributor localizes
// MessageID with its own helpers and the Responses WebSocket relay wraps it in
// a NewAPIError. Message is set instead of MessageID when the text is a fixed
// error code that clients match on.
type ChannelSelectError struct {
	StatusCode int
	Code       types.ErrorCode
	MessageID  string
	Params     map[string]any
	Message    string
	// FilterKind and Channel identify a candidate rejected by request filters.
	FilterKind dto.ChannelFilterKind
	Channel    *model.Channel
	// NoAvailableChannel marks the "no channel for this group and model"
	// outcome so the distributor can name the claiming task plugin.
	NoAvailableChannel bool
}

// SelectChannelForRequest resolves the channel for one attempt with the rules
// shared by the HTTP distributor and the Responses WebSocket relay: a pinned
// channel wins, then session affinity (first attempt only), then a random
// eligible channel; every candidate must satisfy the request's channel
// filters. The group the channel was chosen from is returned for auto-group
// callers. The caller still applies SetupContextForSelectedChannel.
func SelectChannelForRequest(c *gin.Context, modelName string, retry *RetryParam) (*model.Channel, string, *ChannelSelectError) {
	constraints := GetChannelConstraints(c)
	if pin, found, overridden := constraints.ResolvedPin(); found {
		for _, lost := range overridden {
			logger.LogWarn(c, fmt.Sprintf(
				"channel pin overridden: winning_source=%s winning_channel_id=%d overridden_source=%s overridden_channel_id=%d",
				pin.Source, pin.ChannelId, lost.Source, lost.ChannelId,
			))
		}
		channel, err := model.CacheGetChannel(pin.ChannelId)
		if err != nil {
			return nil, "", pinnedChannelUnavailable(pin, http.StatusBadRequest, i18n.MsgDistributorInvalidChannelId)
		}
		if channel.Status != common.ChannelStatusEnabled {
			return nil, "", pinnedChannelUnavailable(pin, http.StatusForbidden, i18n.MsgDistributorChannelDisabled)
		}
		if ok, kind := model.ChannelSatisfiesFilters(channel, modelName, constraints.Filters); !ok {
			return nil, "", &ChannelSelectError{
				StatusCode: http.StatusBadRequest, Code: types.ErrorCode(kind), MessageID: i18n.MsgDistributorNoAvailableChannel,
				Params:     map[string]any{"Group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup), "Model": modelName},
				FilterKind: kind, Channel: channel,
			}
		}
		return channel, "", nil
	}

	usingGroup := retry.TokenGroup
	var channel *model.Channel
	var selectGroup string
	if retry.GetRetry() == 0 {
		if preferredChannelID, found := GetPreferredChannelByAffinity(c, modelName, usingGroup); found {
			affinityUsable := false
			preferred, err := model.CacheGetChannel(preferredChannelID)
			affinitySatisfied := false
			if err == nil && preferred != nil && preferred.Status == common.ChannelStatusEnabled {
				affinitySatisfied, _ = model.ChannelSatisfiesFilters(preferred, modelName, constraints.Filters)
			}
			if affinitySatisfied {
				if usingGroup == "auto" {
					userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
					for _, g := range GetRequestAutoGroups(c, userGroup) {
						if model.IsChannelEnabledForGroupModel(g, modelName, preferred.Id) {
							selectGroup = g
							common.SetContextKey(c, constant.ContextKeyAutoGroup, g)
							channel = preferred
							affinityUsable = true
							if probe := TryClaimHigherPriorityAffinityProbe(c, g, preferred); probe != nil {
								channel = probe
							}
							break
						}
					}
				} else if model.IsChannelEnabledForGroupModel(usingGroup, modelName, preferred.Id) {
					channel = preferred
					selectGroup = usingGroup
					affinityUsable = true
					if probe := TryClaimHigherPriorityAffinityProbe(c, usingGroup, preferred); probe != nil {
						channel = probe
					}
				}
			}
			if !affinityUsable && !ShouldKeepChannelAffinityOnChannelDisabled() {
				ClearCurrentChannelAffinityCache(c)
			}
			if !affinityUsable && RequestPolicy(c).SessionMode == "strict" {
				return nil, "", &ChannelSelectError{StatusCode: http.StatusServiceUnavailable, Message: "strict_session_binding_unavailable"}
			}
		}
	}

	if channel == nil {
		var err error
		channel, selectGroup, err = CacheGetRandomSatisfiedChannel(retry)
		if err != nil {
			showGroup := usingGroup
			if usingGroup == "auto" {
				showGroup = fmt.Sprintf("auto(%s)", selectGroup)
			}
			return nil, selectGroup, &ChannelSelectError{
				StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorGetChannelFailed,
				Params: map[string]any{"Group": showGroup, "Model": modelName, "Error": err.Error()},
			}
		}
		if channel == nil {
			return nil, selectGroup, &ChannelSelectError{
				StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorNoAvailableChannel,
				Params: map[string]any{"Group": usingGroup, "Model": modelName}, NoAvailableChannel: true,
			}
		}
	}
	if ok, kind := model.ChannelSatisfiesFilters(channel, modelName, constraints.Filters); !ok {
		return nil, selectGroup, &ChannelSelectError{
			StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorNoAvailableChannel,
			Params:     map[string]any{"Group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup), "Model": modelName},
			FilterKind: kind, Channel: channel, NoAvailableChannel: true,
		}
	}
	if retry.GetRetry() == 0 {
		MarkChannelAffinityUsed(c, selectGroup, channel.Id, channel.GetPriority())
	}
	return channel, selectGroup, nil
}

// Origin-task pins report a fixed code so task polling can tell a retired
// channel from a malformed request.
func pinnedChannelUnavailable(pin dto.ChannelPin, statusCode int, messageID string) *ChannelSelectError {
	if pin.Source == dto.PinSourceOriginTask {
		return &ChannelSelectError{StatusCode: http.StatusBadRequest, Code: "origin_task_channel_disabled", Message: "origin_task_channel_disabled"}
	}
	return &ChannelSelectError{StatusCode: statusCode, MessageID: messageID}
}

// AppendUsedChannel records an attempted channel in the request's channel
// trail, which the retry log and the consume log's admin_info both read.
func AppendUsedChannel(c *gin.Context, channelID int) {
	c.Set("use_channel", append(c.GetStringSlice("use_channel"), fmt.Sprintf("%d", channelID)))
}
