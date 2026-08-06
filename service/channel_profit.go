package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
)

const (
	channelProfitSchedulerInterval        = time.Minute
	channelProfitDefaultSyncInterval      = 60 * time.Minute
	channelProfitDefaultIntervalMinutes   = int(channelProfitDefaultSyncInterval / time.Minute)
	channelProfitMaxSyncIntervalMinutes   = 7 * 24 * 60
	channelProfitMaxDisplayNameCharacters = 100
	channelProfitHTTPTimeout              = 30 * time.Second
	channelProfitMaxBodyBytes             = 2 << 20
	channelProfitMaxWorkers               = 4
	channelProfitUsageDays                = 1
	channelProfitNewAPITokenPageSize      = 100

	channelProfitProviderNewAPI  = "new_api"
	channelProfitProviderSub2API = "sub2api"
	channelProfitProviderMixed   = "mixed"
)

type ChannelProfitKeySummary struct {
	KeyId                 string   `json:"key_id"`
	KeyHint               string   `json:"key_hint"`
	KeyName               string   `json:"key_name"`
	ChannelIds            []int    `json:"channel_ids"`
	ChannelNames          []string `json:"channel_names"`
	Provider              string   `json:"provider"`
	UpstreamGroup         string   `json:"upstream_group"`
	UpstreamGroupRatio    float64  `json:"upstream_group_ratio"`
	RatioAvailable        bool     `json:"ratio_available"`
	CostUSD               float64  `json:"cost_usd"`
	CostAvailable         bool     `json:"cost_available"`
	Partial               bool     `json:"partial"`
	LastSyncedAt          int64    `json:"last_synced_at"`
	LastError             string   `json:"last_error"`
	UpstreamQuotaPerUnit  float64  `json:"upstream_quota_per_unit"`
	UpstreamConsumedQuota int64    `json:"upstream_consumed_quota"`
}

type ChannelProfitGroupRatio struct {
	Group string  `json:"group"`
	Ratio float64 `json:"ratio"`
}

type ChannelProfitRow struct {
	GroupId               string                    `json:"group_id"`
	ChannelId             int                       `json:"channel_id"`
	ChannelIds            []int                     `json:"channel_ids"`
	ChannelName           string                    `json:"channel_name"`
	BaseURL               string                    `json:"base_url"`
	Provider              string                    `json:"provider"`
	Enabled               bool                      `json:"enabled"`
	SyncIntervalMinutes   int                       `json:"sync_interval_minutes"`
	LastSyncAttemptAt     int64                     `json:"last_sync_attempt_at"`
	AccessTokenConfigured bool                      `json:"access_token_configured"`
	RevenueUSD            float64                   `json:"revenue_usd"`
	CostUSD               float64                   `json:"cost_usd"`
	CostAvailable         bool                      `json:"cost_available"`
	ProfitUSD             float64                   `json:"profit_usd"`
	ProfitAvailable       bool                      `json:"profit_available"`
	Margin                float64                   `json:"margin"`
	MarginAvailable       bool                      `json:"margin_available"`
	Partial               bool                      `json:"partial"`
	Status                string                    `json:"status"`
	LastSyncedAt          int64                     `json:"last_synced_at"`
	LastError             string                    `json:"last_error"`
	DownstreamRates       []ChannelProfitGroupRatio `json:"downstream_rates"`
	Keys                  []ChannelProfitKeySummary `json:"keys"`
}

type ChannelProfitSummary struct {
	UsageDate       string             `json:"usage_date"`
	RevenueUSD      float64            `json:"revenue_usd"`
	CostUSD         float64            `json:"cost_usd"`
	CostAvailable   bool               `json:"cost_available"`
	ProfitUSD       float64            `json:"profit_usd"`
	ProfitAvailable bool               `json:"profit_available"`
	Margin          float64            `json:"margin"`
	MarginAvailable bool               `json:"margin_available"`
	Partial         bool               `json:"partial"`
	LastSyncedAt    int64              `json:"last_synced_at"`
	Rows            []ChannelProfitRow `json:"rows"`
}

type ChannelProfitSyncResult struct {
	Channels int `json:"channels"`
	Keys     int `json:"keys"`
	Synced   int `json:"synced"`
	Failed   int `json:"failed"`
}

type ChannelProfitConfigUpdate struct {
	Enabled             *bool
	DisplayName         *string
	SyncIntervalMinutes *int
	AccessToken         *string
}

type channelProfitSyncPayload struct {
	ChannelId int  `json:"channel_id,omitempty"`
	DueOnly   bool `json:"due_only,omitempty"`
}

type ChannelProfitSyncOptions struct {
	ChannelId int
	DueOnly   bool
}

type channelProfitGroupKey struct {
	Value        string
	Fingerprint  string
	Owner        *model.Channel
	ChannelIds   []int
	ChannelNames []string
}

type channelProfitGroup struct {
	Id                  string
	BaseURL             string
	Channels            []*model.Channel
	Keys                []*channelProfitGroupKey
	Enabled             bool
	DisplayName         string
	SyncIntervalMinutes int
	LastSyncAttemptAt   int64
	AccessToken         string
}

type channelProfitSyncHandler struct{}

func (channelProfitSyncHandler) Type() string { return model.SystemTaskTypeChannelProfit }

func (channelProfitSyncHandler) Enabled() bool {
	count, err := model.CountEnabledChannelProfitConfigs()
	return err == nil && count > 0
}

func (channelProfitSyncHandler) Interval() time.Duration { return channelProfitSchedulerInterval }

func (channelProfitSyncHandler) NewPayload() any {
	return channelProfitSyncPayload{DueOnly: true}
}

func (channelProfitSyncHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := channelProfitSyncPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	result, err := SyncChannelProfits(ctx, ChannelProfitSyncOptions{
		ChannelId: payload.ChannelId,
		DueOnly:   payload.DueOnly,
	})
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}

func init() {
	RegisterSystemTaskHandler(channelProfitSyncHandler{})
}

func StartChannelProfitSync(channelIds ...int) (*model.SystemTask, bool, error) {
	payload := channelProfitSyncPayload{}
	if len(channelIds) > 0 {
		payload.ChannelId = channelIds[0]
	}
	return EnqueueSystemTask(model.SystemTaskTypeChannelProfit, payload)
}

func StartChannelProfitGroupSync(channelId int) (*model.SystemTask, bool, error) {
	group, err := getChannelProfitGroup(channelId)
	if err != nil {
		return nil, false, err
	}
	if !group.Enabled {
		return nil, false, errors.New("profit monitoring is disabled for this channel group")
	}
	return StartChannelProfitSync(group.Channels[0].Id)
}

func SetChannelProfitMonitoring(channelId int, enabled bool) (*model.ChannelProfitConfig, error) {
	return UpdateChannelProfitConfig(channelId, ChannelProfitConfigUpdate{Enabled: &enabled})
}

func UpdateChannelProfitConfig(channelId int, update ChannelProfitConfigUpdate) (*model.ChannelProfitConfig, error) {
	group, err := getChannelProfitGroup(channelId)
	if err != nil {
		return nil, err
	}
	if update.Enabled != nil && *update.Enabled && len(group.Keys) == 0 {
		return nil, errors.New("channel group has no upstream key")
	}
	if update.DisplayName != nil {
		name := strings.TrimSpace(*update.DisplayName)
		if utf8.RuneCountInString(name) > channelProfitMaxDisplayNameCharacters {
			return nil, fmt.Errorf("display name must not exceed %d characters", channelProfitMaxDisplayNameCharacters)
		}
		update.DisplayName = &name
	}
	if update.SyncIntervalMinutes != nil {
		interval := *update.SyncIntervalMinutes
		if interval < 1 || interval > channelProfitMaxSyncIntervalMinutes {
			return nil, fmt.Errorf("sync interval must be between 1 and %d minutes", channelProfitMaxSyncIntervalMinutes)
		}
	}
	if update.AccessToken != nil {
		token := strings.TrimSpace(*update.AccessToken)
		update.AccessToken = &token
	}

	channelIds := make([]int, 0, len(group.Channels))
	for _, channel := range group.Channels {
		channelIds = append(channelIds, channel.Id)
	}
	configs, err := model.UpdateChannelProfitConfigs(channelIds, model.ChannelProfitConfigUpdate{
		Enabled:             update.Enabled,
		DisplayName:         update.DisplayName,
		SyncIntervalMinutes: update.SyncIntervalMinutes,
		AccessToken:         update.AccessToken,
	})
	if err != nil {
		return nil, err
	}
	if update.Enabled != nil && *update.Enabled || update.AccessToken != nil {
		_, _, _ = StartChannelProfitSync(group.Channels[0].Id)
	}
	return configs[0], nil
}

func SyncChannelProfits(ctx context.Context, syncOptions ...ChannelProfitSyncOptions) (ChannelProfitSyncResult, error) {
	options := ChannelProfitSyncOptions{}
	if len(syncOptions) > 0 {
		options = syncOptions[0]
	}
	groups, err := listChannelProfitGroups()
	if err != nil {
		return ChannelProfitSyncResult{}, err
	}
	now := common.GetTimestamp()
	selected := make([]*channelProfitGroup, 0, len(groups))
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		if options.ChannelId > 0 && !channelProfitGroupContainsChannel(group, options.ChannelId) {
			continue
		}
		if options.DueOnly && group.LastSyncAttemptAt > 0 &&
			now-group.LastSyncAttemptAt < int64(group.SyncIntervalMinutes*60) {
			continue
		}
		selected = append(selected, group)
	}
	result := ChannelProfitSyncResult{Channels: len(selected)}
	for _, group := range selected {
		result.Keys += len(group.Keys)
	}
	if len(selected) == 0 {
		if options.ChannelId > 0 {
			return result, errors.New("monitored channel group not found")
		}
		return result, nil
	}

	usageDate := time.Now().In(time.Local).Format("2006-01-02")
	semaphore := make(chan struct{}, channelProfitMaxWorkers)
	var waitGroup sync.WaitGroup
	var resultMu sync.Mutex
	var firstErr error
	for _, group := range selected {
		group := group
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}

			synced, failed := syncChannelProfitGroup(ctx, group, usageDate)
			channelIds := make([]int, 0, len(group.Channels))
			for _, channel := range group.Channels {
				channelIds = append(channelIds, channel.Id)
			}
			attemptedAt := common.GetTimestamp()
			_, updateErr := model.UpdateChannelProfitConfigs(channelIds, model.ChannelProfitConfigUpdate{
				LastSyncAttemptAt: &attemptedAt,
			})
			resultMu.Lock()
			result.Synced += synced
			result.Failed += failed
			if updateErr != nil && firstErr == nil {
				firstErr = updateErr
			}
			resultMu.Unlock()
		}()
	}
	waitGroup.Wait()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, firstErr
}

func syncChannelProfitGroup(ctx context.Context, group *channelProfitGroup, usageDate string) (int, int) {
	if len(group.Keys) == 0 {
		return 0, 1
	}
	channel := group.Channels[0]
	settings := channel.GetSetting()
	client, err := GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		for _, key := range group.Keys {
			saveChannelProfitFailure(key.Owner.Id, usageDate, key.Value, err)
		}
		return 0, len(group.Keys)
	}
	keyValues := make([]string, 0, len(group.Keys))
	for _, key := range group.Keys {
		keyValues = append(keyValues, key.Value)
	}
	backend, err := detectChannelProfitBackend(ctx, client, group.BaseURL, keyValues, usageDate)
	if err != nil {
		for _, key := range group.Keys {
			saveChannelProfitFailure(key.Owner.Id, usageDate, key.Value, err)
		}
		return 0, len(group.Keys)
	}

	newAPIMetadata := map[string]channelProfitNewAPIKeyMetadata{}
	newAPIGroupRatios := map[string]float64{}
	if backend.Provider == channelProfitProviderNewAPI && group.AccessToken != "" {
		newAPIMetadata, newAPIGroupRatios, err = fetchChannelProfitNewAPIMetadata(
			ctx,
			client,
			group.BaseURL,
			group.AccessToken,
			group.Keys,
		)
		if err != nil {
			for _, key := range group.Keys {
				saveChannelProfitFailure(key.Owner.Id, usageDate, key.Value, err)
			}
			return 0, len(group.Keys)
		}
	}

	synced := 0
	failed := 0
	interval := time.Duration(group.SyncIntervalMinutes) * time.Minute
	for _, key := range group.Keys {
		var updateErr error
		switch backend.Provider {
		case channelProfitProviderNewAPI:
			if group.AccessToken != "" {
				metadata, ok := newAPIMetadata[key.Fingerprint]
				if !ok {
					updateErr = errors.New("upstream access token did not return this API key")
					break
				}
				costUSD, statErr := fetchChannelProfitNewAPIExactCost(
					ctx,
					client,
					group.BaseURL,
					group.AccessToken,
					metadata.Name,
					usageDate,
					backend.QuotaPerUnit,
				)
				if statErr != nil {
					updateErr = statErr
					break
				}
				ratio, ratioAvailable := newAPIGroupRatios[metadata.Group]
				updateErr = updateChannelProfitSnapshotForProvider(
					key.Owner.Id, usageDate, key.Value, channelProfitProviderNewAPI,
					0, backend.QuotaPerUnit, costUSD, true,
					metadata.Name, metadata.Group, ratio, ratioAvailable, nil, interval,
				)
				break
			}
			usage, usageErr := fetchChannelProfitUsage(ctx, client, group.BaseURL, key.Value)
			if usageErr != nil {
				updateErr = usageErr
				break
			}
			keyName, upstreamGroup, ratio, ratioAvailable, ratioErr := fetchChannelProfitGroup(ctx, client, group.BaseURL, key.Value)
			updateErr = updateChannelProfitSnapshotForProvider(
				key.Owner.Id, usageDate, key.Value, channelProfitProviderNewAPI,
				usage, backend.QuotaPerUnit, 0, false,
				keyName, upstreamGroup, ratio, ratioAvailable, ratioErr, interval,
			)
		case channelProfitProviderSub2API:
			usage, ok := backend.Sub2APIUsageByKey[key.Fingerprint]
			if !ok {
				usage, err = fetchChannelProfitSub2APIUsage(ctx, client, group.BaseURL, key.Value, usageDate)
				if err != nil {
					updateErr = err
					break
				}
			}
			upstreamGroup, ratio, ratioAvailable, ratioErr := fetchChannelProfitSub2APIGroup(ctx, client, group.BaseURL, key.Value)
			if upstreamGroup == "" {
				upstreamGroup = usage.PlanName
			}
			updateErr = updateChannelProfitSnapshotForProvider(
				key.Owner.Id, usageDate, key.Value, channelProfitProviderSub2API,
				0, 0, usage.CostUSD, true,
				usage.PlanName, upstreamGroup, ratio, ratioAvailable, ratioErr, interval,
			)
		default:
			updateErr = errors.New("unsupported profit monitoring backend")
		}
		if updateErr != nil {
			saveChannelProfitFailure(key.Owner.Id, usageDate, key.Value, updateErr)
			failed++
			continue
		}
		synced++
	}
	return synced, failed
}

func updateChannelProfitSnapshot(
	channelId int,
	usageDate string,
	key string,
	currentQuota int64,
	quotaPerUnit float64,
	group string,
	ratio float64,
	ratioAvailable bool,
	ratioErr error,
) error {
	return updateChannelProfitSnapshotForProvider(
		channelId,
		usageDate,
		key,
		channelProfitProviderNewAPI,
		currentQuota,
		quotaPerUnit,
		0,
		false,
		"",
		group,
		ratio,
		ratioAvailable,
		ratioErr,
		channelProfitDefaultSyncInterval,
	)
}

func updateChannelProfitSnapshotForProvider(
	channelId int,
	usageDate string,
	key string,
	provider string,
	currentQuota int64,
	quotaPerUnit float64,
	directCostUSD float64,
	directCostAvailable bool,
	keyName string,
	group string,
	ratio float64,
	ratioAvailable bool,
	ratioErr error,
	syncInterval time.Duration,
) error {
	if provider != channelProfitProviderNewAPI && provider != channelProfitProviderSub2API {
		return errors.New("unsupported profit monitoring backend")
	}
	if directCostAvailable && (directCostUSD < 0 || math.IsNaN(directCostUSD) || math.IsInf(directCostUSD, 0)) {
		return errors.New("upstream returned an invalid direct cost")
	}

	now := common.GetTimestamp()
	fingerprint := channelProfitKeyFingerprint(key)
	snapshot, err := model.GetChannelProfitSnapshot(channelId, usageDate, fingerprint)
	if err == nil {
		existingProvider := channelProfitSnapshotProvider(snapshot)
		providerChanged := existingProvider != "" && existingProvider != provider
		if provider == channelProfitProviderNewAPI && !directCostAvailable && !providerChanged && currentQuota < snapshot.CurrentQuota {
			snapshot.LastSyncedAt = now
			snapshot.LastError = "upstream cumulative usage counter decreased"
			return model.SaveChannelProfitSnapshot(snapshot)
		}
		if !ratioAvailable && snapshot.RatioAvailable && !providerChanged {
			group = snapshot.UpstreamGroup
			ratio = snapshot.UpstreamGroupRatio
			ratioAvailable = true
		}
		if keyName == "" && !providerChanged {
			keyName = snapshot.UpstreamKeyName
		}
		snapshot.Provider = provider
		snapshot.CurrentQuota = currentQuota
		snapshot.UpstreamQuotaPerUnit = quotaPerUnit
		snapshot.DirectCostUSD = directCostUSD
		snapshot.DirectCostAvailable = directCostAvailable
		snapshot.UpstreamKeyName = keyName
		snapshot.UpstreamGroup = group
		snapshot.UpstreamGroupRatio = ratio
		snapshot.RatioAvailable = ratioAvailable
		if providerChanged {
			snapshot.BaselineQuota = currentQuota
			snapshot.Partial = provider == channelProfitProviderNewAPI
		}
		if provider == channelProfitProviderSub2API || directCostAvailable {
			snapshot.BaselineQuota = 0
			snapshot.CurrentQuota = 0
			if provider == channelProfitProviderSub2API {
				snapshot.UpstreamQuotaPerUnit = 0
			}
			snapshot.Partial = false
		}
		snapshot.LastSyncedAt = now
		snapshot.LastError = errorText(ratioErr)
		return model.SaveChannelProfitSnapshot(snapshot)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	baseline := currentQuota
	partial := true
	lastError := errorText(ratioErr)
	previous, previousErr := model.GetLatestChannelProfitSnapshot(channelId, fingerprint)
	if previousErr == nil {
		previousProvider := channelProfitSnapshotProvider(previous)
		if !ratioAvailable && previous.RatioAvailable && previousProvider == provider {
			group = previous.UpstreamGroup
			ratio = previous.UpstreamGroupRatio
			ratioAvailable = true
		}
		if keyName == "" && previousProvider == provider {
			keyName = previous.UpstreamKeyName
		}
		if provider == channelProfitProviderNewAPI && !directCostAvailable && previousProvider == provider {
			currentDate, parseErr := time.ParseInLocation("2006-01-02", usageDate, time.Local)
			if parseErr != nil {
				return parseErr
			}
			if syncInterval <= 0 {
				syncInterval = channelProfitDefaultSyncInterval
			}
			previousIsRecent := previous.UsageDate == currentDate.AddDate(0, 0, -1).Format("2006-01-02") &&
				previous.LastSyncedAt >= currentDate.Add(-2*syncInterval).Unix()
			if currentQuota >= previous.CurrentQuota && previousIsRecent {
				baseline = previous.CurrentQuota
				partial = false
			} else if currentQuota < previous.CurrentQuota {
				lastError = "upstream cumulative usage counter decreased"
			}
		}
	} else if !errors.Is(previousErr, gorm.ErrRecordNotFound) {
		return previousErr
	}
	if provider == channelProfitProviderSub2API || directCostAvailable {
		baseline = 0
		currentQuota = 0
		if provider == channelProfitProviderSub2API {
			quotaPerUnit = 0
		}
		partial = false
	}

	snapshot = &model.ChannelProfitSnapshot{
		ChannelId:            channelId,
		UsageDate:            usageDate,
		KeyFingerprint:       fingerprint,
		KeyHint:              model.MaskTokenKey(key),
		Provider:             provider,
		BaselineQuota:        baseline,
		CurrentQuota:         currentQuota,
		UpstreamQuotaPerUnit: quotaPerUnit,
		DirectCostUSD:        directCostUSD,
		DirectCostAvailable:  directCostAvailable,
		UpstreamKeyName:      keyName,
		UpstreamGroup:        group,
		UpstreamGroupRatio:   ratio,
		RatioAvailable:       ratioAvailable,
		Partial:              partial,
		LastSyncedAt:         now,
		LastError:            lastError,
	}
	return model.SaveChannelProfitSnapshot(snapshot)
}

func saveChannelProfitFailure(channelId int, usageDate string, key string, syncErr error) {
	fingerprint := channelProfitKeyFingerprint(key)
	snapshot, err := model.GetChannelProfitSnapshot(channelId, usageDate, fingerprint)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		snapshot = &model.ChannelProfitSnapshot{
			ChannelId:      channelId,
			UsageDate:      usageDate,
			KeyFingerprint: fingerprint,
			KeyHint:        model.MaskTokenKey(key),
			Partial:        true,
		}
	} else if err != nil {
		return
	}
	snapshot.LastSyncedAt = common.GetTimestamp()
	snapshot.LastError = errorText(syncErr)
	_ = model.SaveChannelProfitSnapshot(snapshot)
}

func GetChannelProfitSummary(usageDate string, includeDisabled bool) (*ChannelProfitSummary, error) {
	date, err := time.ParseInLocation("2006-01-02", usageDate, time.Local)
	if err != nil {
		return nil, errors.New("invalid usage date")
	}
	if date.After(time.Now().In(time.Local)) {
		return nil, errors.New("usage date cannot be in the future")
	}

	groups, err := listChannelProfitGroups()
	if err != nil {
		return nil, err
	}
	snapshots, err := model.ListChannelProfitSnapshots(usageDate)
	if err != nil {
		return nil, err
	}
	snapshotsByChannel := make(map[int][]*model.ChannelProfitSnapshot)
	for _, snapshot := range snapshots {
		snapshotsByChannel[snapshot.ChannelId] = append(snapshotsByChannel[snapshot.ChannelId], snapshot)
	}
	quotaByChannel, err := model.SumChannelNetQuota(date.Unix(), date.AddDate(0, 0, 1).Unix())
	if err != nil {
		return nil, err
	}

	summary := &ChannelProfitSummary{
		UsageDate: usageDate,
		Rows:      make([]ChannelProfitRow, 0),
	}
	allCostsAvailable := true
	enabledRows := 0
	for _, group := range groups {
		if !group.Enabled && !includeDisabled {
			continue
		}
		revenueQuota := int64(0)
		groupSnapshots := make([]*model.ChannelProfitSnapshot, 0)
		for _, channel := range group.Channels {
			revenueQuota += quotaByChannel[channel.Id]
			groupSnapshots = append(groupSnapshots, snapshotsByChannel[channel.Id]...)
		}
		row := buildChannelProfitGroupRow(group, revenueQuota, groupSnapshots)
		summary.Rows = append(summary.Rows, row)
		if !group.Enabled {
			continue
		}
		enabledRows++
		summary.RevenueUSD += row.RevenueUSD
		summary.CostUSD += row.CostUSD
		if !row.CostAvailable {
			allCostsAvailable = false
		}
		if row.Partial {
			summary.Partial = true
		}
		if row.LastSyncedAt > summary.LastSyncedAt {
			summary.LastSyncedAt = row.LastSyncedAt
		}
	}

	summary.CostAvailable = allCostsAvailable && enabledRows > 0
	summary.ProfitAvailable = summary.CostAvailable
	if summary.ProfitAvailable {
		summary.ProfitUSD = summary.RevenueUSD - summary.CostUSD
		if summary.RevenueUSD > 0 {
			summary.Margin = summary.ProfitUSD / summary.RevenueUSD
			summary.MarginAvailable = true
		}
	}
	return summary, nil
}

func buildChannelProfitGroupRow(group *channelProfitGroup, revenueQuota int64, snapshots []*model.ChannelProfitSnapshot) ChannelProfitRow {
	channelIds := make([]int, 0, len(group.Channels))
	for _, channel := range group.Channels {
		channelIds = append(channelIds, channel.Id)
	}
	row := ChannelProfitRow{
		GroupId:               group.Id,
		ChannelId:             group.Channels[0].Id,
		ChannelIds:            channelIds,
		ChannelName:           group.DisplayName,
		BaseURL:               group.BaseURL,
		Enabled:               group.Enabled,
		SyncIntervalMinutes:   group.SyncIntervalMinutes,
		LastSyncAttemptAt:     group.LastSyncAttemptAt,
		AccessTokenConfigured: group.AccessToken != "",
		RevenueUSD:            float64(revenueQuota) / common.QuotaPerUnit,
		Status:                "disabled",
		DownstreamRates:       make([]ChannelProfitGroupRatio, 0),
		Keys:                  make([]ChannelProfitKeySummary, 0, len(group.Keys)),
	}
	groupRates := ratio_setting.GetGroupRatioCopy()
	rateByGroup := make(map[string]float64)
	for _, channel := range group.Channels {
		for _, channelGroup := range channel.GetGroups() {
			if ratio, ok := groupRates[channelGroup]; ok {
				rateByGroup[channelGroup] = ratio
			}
		}
	}
	groupNames := make([]string, 0, len(rateByGroup))
	for channelGroup := range rateByGroup {
		groupNames = append(groupNames, channelGroup)
	}
	sort.Strings(groupNames)
	for _, channelGroup := range groupNames {
		row.DownstreamRates = append(row.DownstreamRates, ChannelProfitGroupRatio{
			Group: channelGroup,
			Ratio: rateByGroup[channelGroup],
		})
	}

	snapshotByKey := make(map[string]*model.ChannelProfitSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		current := snapshotByKey[snapshot.KeyFingerprint]
		if current == nil || snapshot.LastSyncedAt > current.LastSyncedAt ||
			(snapshot.LastSyncedAt == current.LastSyncedAt && snapshot.Id > current.Id) {
			snapshotByKey[snapshot.KeyFingerprint] = snapshot
		}
	}

	row.CostAvailable = group.Enabled && len(group.Keys) > 0
	row.Status = "synced"
	for _, groupKey := range group.Keys {
		snapshot := snapshotByKey[groupKey.Fingerprint]
		keyId := groupKey.Fingerprint
		if len(keyId) > 12 {
			keyId = keyId[:12]
		}
		key := ChannelProfitKeySummary{
			KeyId:        keyId,
			KeyHint:      model.MaskTokenKey(groupKey.Value),
			ChannelIds:   append([]int(nil), groupKey.ChannelIds...),
			ChannelNames: append([]string(nil), groupKey.ChannelNames...),
		}
		if snapshot == nil {
			row.CostAvailable = false
			row.Keys = append(row.Keys, key)
			continue
		}

		provider := channelProfitSnapshotProvider(snapshot)
		if row.Provider == "" {
			row.Provider = provider
		} else if provider != "" && row.Provider != provider {
			row.Provider = channelProfitProviderMixed
		}
		consumed := snapshot.CurrentQuota - snapshot.BaselineQuota
		costAvailable := false
		cost := 0.0
		if snapshot.DirectCostAvailable {
			costAvailable = snapshot.DirectCostUSD >= 0 && !math.IsNaN(snapshot.DirectCostUSD) && !math.IsInf(snapshot.DirectCostUSD, 0)
			if costAvailable {
				cost = snapshot.DirectCostUSD
			}
		} else if provider == channelProfitProviderNewAPI && snapshot.UpstreamQuotaPerUnit > 0 && consumed >= 0 {
			costAvailable = true
			cost = float64(consumed) / snapshot.UpstreamQuotaPerUnit
		}
		if !costAvailable {
			row.CostAvailable = false
		}
		key.KeyHint = snapshot.KeyHint
		key.KeyName = snapshot.UpstreamKeyName
		if key.KeyName == "" && len(groupKey.ChannelNames) > 0 {
			key.KeyName = groupKey.ChannelNames[0]
		}
		key.Provider = provider
		key.UpstreamGroup = snapshot.UpstreamGroup
		key.UpstreamGroupRatio = snapshot.UpstreamGroupRatio
		key.RatioAvailable = snapshot.RatioAvailable
		key.CostUSD = cost
		key.CostAvailable = costAvailable
		key.Partial = snapshot.Partial
		key.LastSyncedAt = snapshot.LastSyncedAt
		key.LastError = snapshot.LastError
		key.UpstreamQuotaPerUnit = snapshot.UpstreamQuotaPerUnit
		key.UpstreamConsumedQuota = consumed
		row.Keys = append(row.Keys, key)
		row.CostUSD += cost
		if snapshot.Partial {
			row.Partial = true
		}
		if snapshot.LastSyncedAt > row.LastSyncedAt {
			row.LastSyncedAt = snapshot.LastSyncedAt
		}
		if snapshot.LastError != "" {
			row.Status = "error"
			if row.LastError == "" {
				row.LastError = snapshot.LastError
			}
		}
	}
	if !group.Enabled {
		row.Status = "disabled"
		row.CostAvailable = false
		row.ProfitAvailable = false
		row.MarginAvailable = false
		return row
	}
	if len(snapshotByKey) == 0 {
		row.Status = "pending"
		row.Partial = true
	}
	if row.Status == "synced" && row.Partial {
		row.Status = "partial"
	}
	row.ProfitAvailable = row.CostAvailable
	if row.ProfitAvailable {
		row.ProfitUSD = row.RevenueUSD - row.CostUSD
		if row.RevenueUSD > 0 {
			row.Margin = row.ProfitUSD / row.RevenueUSD
			row.MarginAvailable = true
		}
	}
	return row
}

func listChannelProfitGroups() ([]*channelProfitGroup, error) {
	configs, err := model.ListChannelProfitConfigs()
	if err != nil {
		return nil, err
	}
	configByChannel := make(map[int]*model.ChannelProfitConfig, len(configs))
	for _, config := range configs {
		configByChannel[config.ChannelId] = config
	}

	channels, err := model.GetAllChannels(0, 0, true, true)
	if err != nil {
		return nil, err
	}
	groupByBaseURL := make(map[string]*channelProfitGroup)
	for _, channel := range channels {
		baseURL, baseURLErr := channelProfitBaseURL(channel)
		if baseURLErr != nil || len(uniqueChannelProfitKeys(channel)) == 0 {
			continue
		}
		group := groupByBaseURL[baseURL]
		if group == nil {
			group = &channelProfitGroup{
				BaseURL:             baseURL,
				SyncIntervalMinutes: channelProfitDefaultIntervalMinutes,
			}
			groupByBaseURL[baseURL] = group
		}
		group.Channels = append(group.Channels, channel)
	}

	groups := make([]*channelProfitGroup, 0, len(groupByBaseURL))
	for _, group := range groupByBaseURL {
		sort.Slice(group.Channels, func(i, j int) bool {
			return group.Channels[i].Id < group.Channels[j].Id
		})
		group.Id = common.GenerateHMAC(group.BaseURL)
		if len(group.Id) > 16 {
			group.Id = group.Id[:16]
		}
		group.DisplayName = group.Channels[0].Name
		intervalConfigured := false
		for _, channel := range group.Channels {
			config := configByChannel[channel.Id]
			if config == nil {
				continue
			}
			group.Enabled = group.Enabled || config.Enabled
			if group.DisplayName == group.Channels[0].Name && strings.TrimSpace(config.DisplayName) != "" {
				group.DisplayName = strings.TrimSpace(config.DisplayName)
			}
			if !intervalConfigured && config.SyncIntervalMinutes > 0 {
				group.SyncIntervalMinutes = config.SyncIntervalMinutes
				intervalConfigured = true
			}
			if config.LastSyncAttemptAt > group.LastSyncAttemptAt {
				group.LastSyncAttemptAt = config.LastSyncAttemptAt
			}
			if group.AccessToken == "" && strings.TrimSpace(config.AccessToken) != "" {
				group.AccessToken = strings.TrimSpace(config.AccessToken)
			}
		}

		keyByFingerprint := make(map[string]*channelProfitGroupKey)
		for _, channel := range group.Channels {
			for _, value := range uniqueChannelProfitKeys(channel) {
				fingerprint := channelProfitKeyFingerprint(value)
				key := keyByFingerprint[fingerprint]
				if key == nil {
					key = &channelProfitGroupKey{
						Value:       value,
						Fingerprint: fingerprint,
						Owner:       channel,
					}
					keyByFingerprint[fingerprint] = key
					group.Keys = append(group.Keys, key)
				}
				key.ChannelIds = append(key.ChannelIds, channel.Id)
				key.ChannelNames = append(key.ChannelNames, channel.Name)
			}
		}
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Channels[0].Id < groups[j].Channels[0].Id
	})
	return groups, nil
}

func getChannelProfitGroup(channelId int) (*channelProfitGroup, error) {
	groups, err := listChannelProfitGroups()
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		if channelProfitGroupContainsChannel(group, channelId) {
			return group, nil
		}
	}
	return nil, errors.New("channel group not found")
}

func channelProfitGroupContainsChannel(group *channelProfitGroup, channelId int) bool {
	for _, channel := range group.Channels {
		if channel.Id == channelId {
			return true
		}
	}
	return false
}

type channelProfitStatusResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		QuotaPerUnit float64 `json:"quota_per_unit"`
	} `json:"data"`
}

type channelProfitUsageResponse struct {
	Code    bool   `json:"code"`
	Message string `json:"message"`
	Data    struct {
		TotalUsed int64 `json:"total_used"`
	} `json:"data"`
}

type channelProfitLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    []struct {
		TokenName string `json:"token_name"`
		Group     string `json:"group"`
		Other     string `json:"other"`
	} `json:"data"`
}

type channelProfitSub2APIUsageResponse struct {
	PlanName   string `json:"planName"`
	DailyUsage *[]struct {
		Date       string  `json:"date"`
		ActualCost float64 `json:"actual_cost"`
	} `json:"daily_usage"`
	Usage *struct {
		Today *struct {
			ActualCost *float64 `json:"actual_cost"`
		} `json:"today"`
	} `json:"usage"`
}

type channelProfitSub2APIUsage struct {
	CostUSD  float64
	PlanName string
}

type channelProfitSub2APIBillingResponse struct {
	Object              string   `json:"object"`
	GroupRateMultiplier *float64 `json:"group_rate_multiplier"`
}

type channelProfitBackend struct {
	Provider          string
	QuotaPerUnit      float64
	Sub2APIUsageByKey map[string]channelProfitSub2APIUsage
}

type channelProfitNewAPIKeyMetadata struct {
	Name  string
	Group string
}

type channelProfitNewAPITokenListResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
		Total    int `json:"total"`
		Items    []struct {
			Key   string `json:"key"`
			Name  string `json:"name"`
			Group string `json:"group"`
		} `json:"items"`
	} `json:"data"`
}

type channelProfitNewAPIGroupResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    map[string]struct {
		Ratio any `json:"ratio"`
	} `json:"data"`
}

type channelProfitNewAPIStatResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Quota int64 `json:"quota"`
	} `json:"data"`
}

type channelProfitHTTPError struct {
	StatusCode int
}

func (err *channelProfitHTTPError) Error() string {
	return fmt.Sprintf("upstream returned HTTP %d", err.StatusCode)
}

func detectChannelProfitBackend(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	keys []string,
	usageDate string,
) (channelProfitBackend, error) {
	quotaPerUnit, newAPIErr := fetchChannelProfitQuotaPerUnit(ctx, client, baseURL)
	if newAPIErr == nil {
		return channelProfitBackend{
			Provider:     channelProfitProviderNewAPI,
			QuotaPerUnit: quotaPerUnit,
		}, nil
	}

	usageByKey := make(map[string]channelProfitSub2APIUsage)
	var sub2APIErr error
	for _, key := range keys {
		usage, err := fetchChannelProfitSub2APIUsage(ctx, client, baseURL, key, usageDate)
		if err != nil {
			sub2APIErr = err
			continue
		}
		usageByKey[channelProfitKeyFingerprint(key)] = usage
		return channelProfitBackend{
			Provider:          channelProfitProviderSub2API,
			Sub2APIUsageByKey: usageByKey,
		}, nil
	}
	if sub2APIErr == nil {
		sub2APIErr = errors.New("channel has no upstream key")
	}
	return channelProfitBackend{}, fmt.Errorf(
		"upstream is neither a supported New API nor Sub2API backend (New API probe: %v; Sub2API probe: %v)",
		newAPIErr,
		sub2APIErr,
	)
}

func fetchChannelProfitQuotaPerUnit(ctx context.Context, client *http.Client, baseURL string) (float64, error) {
	response := channelProfitStatusResponse{}
	if err := fetchChannelProfitJSON(ctx, client, baseURL+"/api/status", "", &response); err != nil {
		return 0, err
	}
	if !response.Success || response.Data.QuotaPerUnit <= 0 {
		return 0, fmt.Errorf("upstream status did not return a valid quota_per_unit: %s", response.Message)
	}
	return response.Data.QuotaPerUnit, nil
}

func fetchChannelProfitUsage(ctx context.Context, client *http.Client, baseURL string, key string) (int64, error) {
	response := channelProfitUsageResponse{}
	if err := fetchChannelProfitJSON(ctx, client, baseURL+"/api/usage/token", key, &response); err != nil {
		return 0, err
	}
	if !response.Code {
		return 0, errors.New(response.Message)
	}
	if response.Data.TotalUsed < 0 {
		return 0, errors.New("upstream returned a negative total_used")
	}
	return response.Data.TotalUsed, nil
}

func fetchChannelProfitGroup(ctx context.Context, client *http.Client, baseURL string, key string) (string, string, float64, bool, error) {
	response := channelProfitLogResponse{}
	if err := fetchChannelProfitJSON(ctx, client, baseURL+"/api/log/token", key, &response); err != nil {
		return "", "", 0, false, err
	}
	if !response.Success {
		return "", "", 0, false, errors.New(response.Message)
	}
	if len(response.Data) == 0 {
		return "", "", 0, false, errors.New("upstream token has no usage log for ratio discovery")
	}

	keyName := response.Data[0].TokenName
	group := response.Data[0].Group
	other := make(map[string]any)
	if err := common.UnmarshalJsonStr(response.Data[0].Other, &other); err != nil {
		return keyName, group, 0, false, fmt.Errorf("decode upstream log ratio: %w", err)
	}
	ratio, ok := other["group_ratio"].(float64)
	if !ok || ratio < 0 {
		return keyName, group, 0, false, errors.New("upstream log did not include group_ratio")
	}
	return keyName, group, ratio, true, nil
}

func fetchChannelProfitNewAPIMetadata(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	accessToken string,
	keys []*channelProfitGroupKey,
) (map[string]channelProfitNewAPIKeyMetadata, map[string]float64, error) {
	keysByMask := make(map[string][]*channelProfitGroupKey, len(keys)*2)
	for _, key := range keys {
		masks := []string{
			model.MaskTokenKey(key.Value),
			model.MaskTokenKey(strings.TrimPrefix(key.Value, "sk-")),
		}
		for _, mask := range masks {
			if mask == "" {
				continue
			}
			alreadyAdded := false
			for _, existing := range keysByMask[mask] {
				if existing.Fingerprint == key.Fingerprint {
					alreadyAdded = true
					break
				}
			}
			if !alreadyAdded {
				keysByMask[mask] = append(keysByMask[mask], key)
			}
		}
	}

	metadataByKey := make(map[string]channelProfitNewAPIKeyMetadata, len(keys))
	for page := 1; ; page++ {
		query := url.Values{}
		query.Set("p", strconv.Itoa(page))
		query.Set("size", strconv.Itoa(channelProfitNewAPITokenPageSize))
		response := channelProfitNewAPITokenListResponse{}
		if err := fetchChannelProfitJSON(ctx, client, baseURL+"/api/token/?"+query.Encode(), accessToken, &response); err != nil {
			return nil, nil, fmt.Errorf("fetch upstream API keys: %w", err)
		}
		if !response.Success {
			return nil, nil, fmt.Errorf("fetch upstream API keys: %s", response.Message)
		}
		for _, item := range response.Data.Items {
			matched := keysByMask[item.Key]
			if len(matched) > 1 {
				return nil, nil, fmt.Errorf("multiple local API keys share upstream mask %s", item.Key)
			}
			if len(matched) == 0 {
				continue
			}
			key := matched[0]
			metadata := channelProfitNewAPIKeyMetadata{
				Name:  strings.TrimSpace(item.Name),
				Group: strings.TrimSpace(item.Group),
			}
			if metadata.Name == "" {
				return nil, nil, fmt.Errorf("upstream API key %s has no name", item.Key)
			}
			if existing, ok := metadataByKey[key.Fingerprint]; ok && existing != metadata {
				return nil, nil, fmt.Errorf("upstream API key mask %s matched multiple token records", item.Key)
			}
			metadataByKey[key.Fingerprint] = metadata
		}
		pageSize := response.Data.PageSize
		if pageSize <= 0 {
			pageSize = channelProfitNewAPITokenPageSize
		}
		if page*pageSize >= response.Data.Total || len(response.Data.Items) == 0 {
			break
		}
	}
	if len(metadataByKey) != len(keys) {
		return nil, nil, fmt.Errorf("upstream access token matched %d of %d local API keys", len(metadataByKey), len(keys))
	}
	nameOwner := make(map[string]string, len(metadataByKey))
	for fingerprint, metadata := range metadataByKey {
		if owner, exists := nameOwner[metadata.Name]; exists && owner != fingerprint {
			return nil, nil, fmt.Errorf("upstream API key name %q is duplicated and cannot be billed independently", metadata.Name)
		}
		nameOwner[metadata.Name] = fingerprint
	}

	groupResponse := channelProfitNewAPIGroupResponse{}
	if err := fetchChannelProfitJSON(ctx, client, baseURL+"/api/user/self/groups", accessToken, &groupResponse); err != nil {
		return nil, nil, fmt.Errorf("fetch upstream group ratios: %w", err)
	}
	if !groupResponse.Success {
		return nil, nil, fmt.Errorf("fetch upstream group ratios: %s", groupResponse.Message)
	}
	groupRatios := make(map[string]float64, len(groupResponse.Data))
	for name, item := range groupResponse.Data {
		var ratio float64
		switch value := item.Ratio.(type) {
		case float64:
			ratio = value
		case string:
			parsed, parseErr := strconv.ParseFloat(value, 64)
			if parseErr != nil {
				continue
			}
			ratio = parsed
		default:
			continue
		}
		if ratio >= 0 && !math.IsNaN(ratio) && !math.IsInf(ratio, 0) {
			groupRatios[name] = ratio
		}
	}
	return metadataByKey, groupRatios, nil
}

func fetchChannelProfitNewAPIExactCost(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	accessToken string,
	tokenName string,
	usageDate string,
	quotaPerUnit float64,
) (float64, error) {
	date, err := time.ParseInLocation("2006-01-02", usageDate, time.Local)
	if err != nil {
		return 0, err
	}
	query := url.Values{}
	query.Set("start_timestamp", strconv.FormatInt(date.Unix(), 10))
	query.Set("end_timestamp", strconv.FormatInt(date.AddDate(0, 0, 1).Unix()-1, 10))
	query.Set("token_name", tokenName)
	response := channelProfitNewAPIStatResponse{}
	if err := fetchChannelProfitJSON(ctx, client, baseURL+"/api/log/self/stat?"+query.Encode(), accessToken, &response); err != nil {
		return 0, err
	}
	if !response.Success {
		return 0, errors.New(response.Message)
	}
	if response.Data.Quota < 0 || quotaPerUnit <= 0 {
		return 0, errors.New("upstream returned invalid daily quota data")
	}
	return float64(response.Data.Quota) / quotaPerUnit, nil
}

func fetchChannelProfitSub2APIUsage(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	key string,
	usageDate string,
) (channelProfitSub2APIUsage, error) {
	query := url.Values{}
	query.Set("days", strconv.Itoa(channelProfitUsageDays))
	if timezone := time.Local.String(); timezone != "" && timezone != "Local" {
		query.Set("timezone", timezone)
	}
	targetURL := baseURL + "/v1/usage?" + query.Encode()
	response := channelProfitSub2APIUsageResponse{}
	if err := fetchChannelProfitJSON(ctx, client, targetURL, key, &response); err != nil {
		return channelProfitSub2APIUsage{}, err
	}
	if response.DailyUsage != nil {
		for _, usage := range *response.DailyUsage {
			if usage.Date != usageDate {
				continue
			}
			if usage.ActualCost < 0 || math.IsNaN(usage.ActualCost) || math.IsInf(usage.ActualCost, 0) {
				return channelProfitSub2APIUsage{}, errors.New("Sub2API returned an invalid daily actual_cost")
			}
			return channelProfitSub2APIUsage{CostUSD: usage.ActualCost, PlanName: response.PlanName}, nil
		}
		return channelProfitSub2APIUsage{PlanName: response.PlanName}, nil
	}
	if usageDate == time.Now().In(time.Local).Format("2006-01-02") &&
		response.Usage != nil && response.Usage.Today != nil && response.Usage.Today.ActualCost != nil {
		cost := *response.Usage.Today.ActualCost
		if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
			return channelProfitSub2APIUsage{}, errors.New("Sub2API returned an invalid today actual_cost")
		}
		return channelProfitSub2APIUsage{CostUSD: cost, PlanName: response.PlanName}, nil
	}
	return channelProfitSub2APIUsage{}, errors.New("upstream usage response did not include Sub2API daily usage")
}

func fetchChannelProfitSub2APIGroup(ctx context.Context, client *http.Client, baseURL string, key string) (string, float64, bool, error) {
	response := channelProfitSub2APIBillingResponse{}
	if err := fetchChannelProfitJSON(ctx, client, baseURL+"/v1/sub2api/billing", key, &response); err != nil {
		httpErr := &channelProfitHTTPError{}
		if errors.As(err, &httpErr) &&
			(httpErr.StatusCode == http.StatusForbidden || httpErr.StatusCode == http.StatusNotFound || httpErr.StatusCode == http.StatusMethodNotAllowed) {
			return "", 0, false, nil
		}
		return "", 0, false, err
	}
	if response.Object != "sub2api.key_billing" || response.GroupRateMultiplier == nil {
		return "", 0, false, errors.New("Sub2API billing response did not include group_rate_multiplier")
	}
	ratio := *response.GroupRateMultiplier
	if ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return "", 0, false, errors.New("Sub2API returned an invalid group_rate_multiplier")
	}
	return "", ratio, true, nil
}

func fetchChannelProfitJSON(ctx context.Context, client *http.Client, targetURL string, key string, dest any) error {
	requestCtx, cancel := context.WithTimeout(ctx, channelProfitHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
		return &channelProfitHTTPError{StatusCode: resp.StatusCode}
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, channelProfitMaxBodyBytes), dest); err != nil {
		return err
	}
	return nil
}

func channelProfitBaseURL(channel *model.Channel) (string, error) {
	baseURL := strings.TrimSpace(channel.GetBaseURL())
	if baseURL == "" {
		return "", errors.New("channel base URL is required")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") {
		baseURL = strings.TrimSuffix(baseURL, "/v1")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("channel base URL is invalid")
	}
	return baseURL, nil
}

func channelProfitCanProbe(channel *model.Channel) bool {
	// 上游后端由探测接口识别，不能以本地转发渠道类型作为筛选条件。
	if channel == nil {
		return false
	}
	if _, err := channelProfitBaseURL(channel); err != nil {
		return false
	}
	return len(uniqueChannelProfitKeys(channel)) > 0
}

func channelProfitSnapshotProvider(snapshot *model.ChannelProfitSnapshot) string {
	if snapshot.Provider != "" {
		return snapshot.Provider
	}
	if snapshot.DirectCostAvailable {
		return channelProfitProviderSub2API
	}
	if snapshot.UpstreamQuotaPerUnit > 0 {
		return channelProfitProviderNewAPI
	}
	return ""
}

func uniqueChannelProfitKeys(channel *model.Channel) []string {
	seen := make(map[string]struct{})
	keys := make([]string, 0)
	for _, key := range channel.GetKeys() {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		fingerprint := channelProfitKeyFingerprint(key)
		if _, ok := seen[fingerprint]; ok {
			continue
		}
		seen[fingerprint] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func channelProfitKeyFingerprint(key string) string {
	return common.GenerateHMAC(strings.TrimSpace(key))
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
