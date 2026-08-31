package model

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
// channel2advancedCustomConfig caches parsed Advanced Custom (type 58) configs so
// path-aware selection avoids re-parsing JSON per request. Refreshed on full sync.
var channel2advancedCustomConfig map[int]*kitdto.AdvancedCustomConfig
var channelSyncLock sync.RWMutex

func InitChannelCache() {
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		rebuildTaskAliasView()
		return
	}
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*kitdto.AdvancedCustomConfig)
	var channels []*Channel
	DB.Find(&channels)
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	var abilities []*Ability
	DB.Find(&abilities)
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]int)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]int)
	}
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue // skip disabled channels
		}
		groups := strings.Split(channel.Group, ",")
		for _, group := range groups {
			models := strings.Split(channel.Models, ",")
			for _, model := range models {
				if _, ok := newGroup2model2channels[group][model]; !ok {
					newGroup2model2channels[group][model] = make([]int, 0)
				}
				newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], channel.Id)
			}
		}
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				if oldChannel, ok := channelsIDM[i]; ok {
					// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
					if oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
						channel.ChannelInfo.MultiKeyPollingIndex = oldChannel.ChannelInfo.MultiKeyPollingIndex
					}
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	channelSyncLock.Unlock()
	// Lock ordering: InvalidatePricingCache acquires updatePricingLock, and
	// GetPricing (holding updatePricingLock) nests channelSyncLock.RLock via
	// loadPricingAdvancedCustomConfigs. channelSyncLock MUST be released before
	// invalidating the pricing cache, otherwise the reversed order deadlocks.
	InvalidatePricingCache()
	rebuildTaskAliasView()
	common.SysLog("channels synced from database")
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog("syncing channels from database")
		InitChannelCache()
	}
}

func GetRandomSatisfiedChannel(group string, model string, retry int, filters []dto.ChannelFilter) (*Channel, error) {
	return GetRandomSatisfiedChannelSkippingPriority(group, model, retry, filters, nil)
}

// GetRandomSatisfiedChannelAtNextHigherPriority selects a channel from the
// nearest available priority above currentPriority.
func GetRandomSatisfiedChannelAtNextHigherPriority(group string, model string, currentPriority int64, filters []dto.ChannelFilter) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelAtNextHigherPriority(group, model, currentPriority, filters)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	channels, _ := filterCandidateIDs(group2model2channels[group][model], model, filters)
	if len(channels) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channels, _ = filterCandidateIDs(group2model2channels[group][normalizedModel], model, filters)
	}

	var targetPriority int64
	found := false
	for _, channelID := range channels {
		channel, ok := channelsIDM[channelID]
		if !ok {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelID)
		}
		priority := channel.GetPriority()
		if priority > currentPriority && (!found || priority < targetPriority) {
			targetPriority = priority
			found = true
		}
	}
	if !found {
		return nil, nil
	}
	return randomSatisfiedChannelAtPriority(channels, targetPriority, nil)
}

// GetSatisfiedChannelsAtPriority returns enabled channels for the exact model
// (or its normalized model) at one priority. The result is backed by the
// existing channel cache and does not issue a database query.
func GetSatisfiedChannelsAtPriority(group string, model string, priority int64, filters []dto.ChannelFilter) ([]*Channel, error) {
	if !common.MemoryCacheEnabled {
		loadAbilities := func(modelName string) ([]Ability, error) {
			var abilities []Ability
			err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ? and priority = ?", group, modelName, true, priority).
				Find(&abilities).Error
			if err != nil {
				return nil, err
			}
			return filterAbilitiesByConstraints(abilities, model, filters), nil
		}
		abilities, err := loadAbilities(model)
		if err != nil {
			return nil, err
		}
		if len(abilities) == 0 {
			normalizedModel := ratio_setting.FormatMatchingModelName(model)
			if normalizedModel != model {
				abilities, err = loadAbilities(normalizedModel)
				if err != nil {
					return nil, err
				}
			}
		}
		if len(abilities) == 0 {
			return nil, nil
		}
		ids := make([]int, 0, len(abilities))
		seen := make(map[int]struct{}, len(abilities))
		for _, ability := range abilities {
			if _, ok := seen[ability.ChannelId]; ok {
				continue
			}
			seen[ability.ChannelId] = struct{}{}
			ids = append(ids, ability.ChannelId)
		}
		channels, err := GetChannelsByIds(ids)
		if err != nil {
			return nil, err
		}
		result := make([]*Channel, 0, len(channels))
		for _, channel := range channels {
			if channel.Status == common.ChannelStatusEnabled {
				result = append(result, channel)
			}
		}
		return result, nil
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	channels, _ := filterCandidateIDs(group2model2channels[group][model], model, filters)
	if len(channels) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channels, _ = filterCandidateIDs(group2model2channels[group][normalizedModel], model, filters)
	}
	if len(channels) == 0 {
		return nil, nil
	}

	result := make([]*Channel, 0, len(channels))
	for _, channelID := range channels {
		channel, ok := channelsIDM[channelID]
		if !ok {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelID)
		}
		if channel.GetPriority() == priority {
			result = append(result, channel)
		}
	}
	return result, nil
}

// GetSatisfiedChannels returns every enabled channel that can serve the model
// and constraints, regardless of priority. Callers must still apply the
// normal priority/weight policy when they do not have stronger evidence for a
// cross-priority decision.
func GetSatisfiedChannels(group string, model string, filters []dto.ChannelFilter) ([]*Channel, error) {
	if !common.MemoryCacheEnabled {
		loadAbilities := func(modelName string) ([]Ability, error) {
			var abilities []Ability
			err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, modelName, true).
				Find(&abilities).Error
			if err != nil {
				return nil, err
			}
			return filterAbilitiesByConstraints(abilities, model, filters), nil
		}
		abilities, err := loadAbilities(model)
		if err != nil {
			return nil, err
		}
		if len(abilities) == 0 {
			normalizedModel := ratio_setting.FormatMatchingModelName(model)
			if normalizedModel != model {
				abilities, err = loadAbilities(normalizedModel)
				if err != nil {
					return nil, err
				}
			}
		}
		if len(abilities) == 0 {
			return nil, nil
		}
		ids := make([]int, 0, len(abilities))
		seen := make(map[int]struct{}, len(abilities))
		for _, ability := range abilities {
			if _, ok := seen[ability.ChannelId]; ok {
				continue
			}
			seen[ability.ChannelId] = struct{}{}
			ids = append(ids, ability.ChannelId)
		}
		channels, err := GetChannelsByIds(ids)
		if err != nil {
			return nil, err
		}
		result := make([]*Channel, 0, len(channels))
		for _, channel := range channels {
			if channel.Status == common.ChannelStatusEnabled {
				result = append(result, channel)
			}
		}
		return result, nil
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	channelIDs, _ := filterCandidateIDs(group2model2channels[group][model], model, filters)
	if len(channelIDs) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channelIDs, _ = filterCandidateIDs(group2model2channels[group][normalizedModel], model, filters)
	}
	result := make([]*Channel, 0, len(channelIDs))
	seen := make(map[int]struct{}, len(channelIDs))
	for _, channelID := range channelIDs {
		if _, ok := seen[channelID]; ok {
			continue
		}
		seen[channelID] = struct{}{}
		channel, ok := channelsIDM[channelID]
		if !ok {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelID)
		}
		if channel.Status == common.ChannelStatusEnabled {
			result = append(result, channel)
		}
	}
	return result, nil
}

// GetRandomSatisfiedChannelAtPrioritySkippingChannels preserves the normal
// weighted selection within one priority while excluding channels that the
// caller has already consumed in the current routing episode.
func GetRandomSatisfiedChannelAtPrioritySkippingChannels(group string, model string, priority int64, filters []dto.ChannelFilter, skippedChannelIDs map[int]struct{}) (*Channel, error) {
	candidates, err := GetSatisfiedChannelsAtPriority(group, model, priority, filters)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	remaining := make([]*Channel, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		if _, skipped := skippedChannelIDs[candidate.Id]; !skipped {
			remaining = append(remaining, candidate)
		}
	}
	if !common.MemoryCacheEnabled {
		return randomSatisfiedChannelByDatabaseWeight(remaining)
	}
	return randomSatisfiedChannelByWeight(remaining)
}

// GetRandomSatisfiedChannelSkippingPriority keeps the original priority-based
// retry order while omitting the priority already consumed by an affinity hit.
func GetRandomSatisfiedChannelSkippingPriority(group string, model string, retry int, filters []dto.ChannelFilter, skippedPriority *int64) (*Channel, error) {
	return GetRandomSatisfiedChannelSkippingPriorityAndChannels(group, model, retry, filters, skippedPriority, nil)
}

// GetRandomSatisfiedChannelSkippingPriorityAndChannels also avoids channels
// that already failed in the current request when alternatives remain at the
// selected priority.
func GetRandomSatisfiedChannelSkippingPriorityAndChannels(group string, model string, retry int, filters []dto.ChannelFilter, skippedPriority *int64, failedChannelIDs map[int]struct{}) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return getChannelSkippingPriority(group, model, retry, filters, skippedPriority, failedChannelIDs)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// First, try to find channels with the exact model name.
	channels, _ := filterCandidateIDs(group2model2channels[group][model], model, filters)

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channels, _ = filterCandidateIDs(group2model2channels[group][normalizedModel], model, filters)
	}

	if len(channels) == 0 {
		return nil, nil
	}

	if len(channels) == 1 {
		if channel, ok := channelsIDM[channels[0]]; ok {
			return channel, nil
		}
		return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channels[0])
	}

	uniquePriorities := make(map[int]bool)
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			priority := channel.GetPriority()
			if skippedPriority == nil || priority != *skippedPriority {
				uniquePriorities[int(priority)] = true
			}
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}
	if len(uniquePriorities) == 0 && skippedPriority != nil {
		for _, channelId := range channels {
			channel, ok := channelsIDM[channelId]
			if !ok {
				return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
			}
			uniquePriorities[int(channel.GetPriority())] = true
		}
	}
	var sortedUniquePriorities []int
	for priority := range uniquePriorities {
		sortedUniquePriorities = append(sortedUniquePriorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedUniquePriorities)))

	if retry >= len(uniquePriorities) {
		retry = len(uniquePriorities) - 1
	}
	targetPriority := int64(sortedUniquePriorities[retry])

	return randomSatisfiedChannelAtPriority(channels, targetPriority, failedChannelIDs)
}

// GetRandomSatisfiedChannelForRetry selects an untried channel at or below
// currentPriority. A downward move always selects the immediately next
// available priority. It never skips an intermediate priority to reach a
// lower one before the retry budget runs out.
func GetRandomSatisfiedChannelForRetry(group string, model string, currentPriority int64, remainingRetries int, filters []dto.ChannelFilter, failedChannelIDs map[int]struct{}) (*Channel, error) {
	if remainingRetries <= 0 {
		return nil, nil
	}
	if !common.MemoryCacheEnabled {
		return getChannelForRetry(group, model, currentPriority, remainingRetries, filters, failedChannelIDs)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	channels, _ := filterCandidateIDs(group2model2channels[group][model], model, filters)
	if len(channels) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channels, _ = filterCandidateIDs(group2model2channels[group][normalizedModel], model, filters)
	}

	availableChannels := make([]int, 0, len(channels))
	prioritySet := make(map[int64]struct{})
	for _, channelID := range channels {
		channel, ok := channelsIDM[channelID]
		if !ok {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelID)
		}
		priority := channel.GetPriority()
		if priority > currentPriority {
			continue
		}
		if _, failed := failedChannelIDs[channelID]; failed {
			continue
		}
		availableChannels = append(availableChannels, channelID)
		prioritySet[priority] = struct{}{}
	}
	if len(availableChannels) == 0 {
		return nil, nil
	}

	priorities := make([]int64, 0, len(prioritySet))
	for priority := range prioritySet {
		priorities = append(priorities, priority)
	}
	sort.Slice(priorities, func(i, j int) bool {
		return priorities[i] > priorities[j]
	})

	targetPriority := priorities[0]
	if targetPriority == currentPriority && len(priorities) > 1 && remainingRetries <= len(priorities)-1 {
		targetPriority = priorities[1]
	}
	return randomSatisfiedChannelAtPriority(availableChannels, targetPriority, nil)
}

func randomSatisfiedChannelAtPriority(channels []int, targetPriority int64, failedChannelIDs map[int]struct{}) (*Channel, error) {
	var targetChannels []*Channel
	for _, channelID := range channels {
		if channel, ok := channelsIDM[channelID]; ok {
			if channel.GetPriority() == targetPriority {
				targetChannels = append(targetChannels, channel)
			}
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelID)
		}
	}

	if len(targetChannels) == 0 {
		return nil, fmt.Errorf("no channel found at priority: %d", targetPriority)
	}
	if len(failedChannelIDs) > 0 {
		remainingChannels := make([]*Channel, 0, len(targetChannels))
		for _, channel := range targetChannels {
			if _, failed := failedChannelIDs[channel.Id]; !failed {
				remainingChannels = append(remainingChannels, channel)
			}
		}
		if len(remainingChannels) > 0 {
			targetChannels = remainingChannels
		}
	}
	return randomSatisfiedChannelByWeight(targetChannels)
}

func randomSatisfiedChannelByWeight(targetChannels []*Channel) (*Channel, error) {
	if len(targetChannels) == 0 {
		return nil, errors.New("channel not found")
	}

	var sumWeight = 0
	for _, channel := range targetChannels {
		sumWeight += channel.GetWeight()
	}

	// smoothing factor and adjustment
	smoothingFactor := 1
	smoothingAdjustment := 0

	if sumWeight == 0 {
		// when all channels have weight 0, set sumWeight to the number of channels and set smoothing adjustment to 100
		// each channel's effective weight = 100
		sumWeight = len(targetChannels) * 100
		smoothingAdjustment = 100
	} else if sumWeight/len(targetChannels) < 10 {
		// when the average weight is less than 10, set smoothing factor to 100
		smoothingFactor = 100
	}

	// Calculate the total weight of all channels up to endIdx
	totalWeight := sumWeight * smoothingFactor

	// Generate a random value in the range [0, totalWeight)
	randomWeight := rand.Intn(totalWeight)

	// Find a channel based on its weight
	for _, channel := range targetChannels {
		randomWeight -= channel.GetWeight()*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return channel, nil
		}
	}
	// return null if no channel is not found
	return nil, errors.New("channel not found")
}

// randomSatisfiedChannelByDatabaseWeight mirrors the database-backed selector,
// whose effective weight is the configured weight plus the existing +10 floor.
// It is used by FRT exploration when the in-memory channel cache is disabled.
func randomSatisfiedChannelByDatabaseWeight(targetChannels []*Channel) (*Channel, error) {
	if len(targetChannels) == 0 {
		return nil, errors.New("channel not found")
	}
	weightSum := 0
	for _, channel := range targetChannels {
		weightSum += channel.GetWeight() + 10
	}
	weight := common.GetRandomInt(weightSum)
	for _, channel := range targetChannels {
		weight -= channel.GetWeight() + 10
		if weight <= 0 {
			return channel, nil
		}
	}
	return nil, errors.New("channel not found")
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return c, nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return &c.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel, ok := channelsIDM[id]; ok {
		channel.Status = status
	}
	if status != common.ChannelStatusEnabled {
		// delete the channel from group2model2channels
		for group, model2channels := range group2model2channels {
			for model, channels := range model2channels {
				for i, channelId := range channels {
					if channelId == id {
						// remove the channel from the slice
						group2model2channels[group][model] = append(channels[:i], channels[i+1:]...)
						break
					}
				}
			}
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	if channel == nil {
		channelSyncLock.Unlock()
		return
	}

	if channelsIDM == nil {
		channelsIDM = make(map[int]*Channel)
	}
	if oldChannel, ok := channelsIDM[channel.Id]; ok {
		logger.LogDebug(nil, "CacheUpdateChannel before: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, oldChannel.ChannelInfo.MultiKeyPollingIndex)
	}
	channelsIDM[channel.Id] = channel
	if channel2advancedCustomConfig == nil {
		channel2advancedCustomConfig = make(map[int]*kitdto.AdvancedCustomConfig)
	}
	delete(channel2advancedCustomConfig, channel.Id)
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			channel2advancedCustomConfig[channel.Id] = config
		}
	}
	logger.LogDebug(nil, "CacheUpdateChannel after: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)
	// Lock ordering: do NOT hold channelSyncLock while calling
	// InvalidatePricingCache. GetPricing acquires updatePricingLock first and then
	// channelSyncLock.RLock (via loadPricingAdvancedCustomConfigs); acquiring
	// updatePricingLock while holding channelSyncLock would be an AB-BA deadlock.
	channelSyncLock.Unlock()
	InvalidatePricingCache()
}
