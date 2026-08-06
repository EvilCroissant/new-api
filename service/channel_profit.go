package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
)

const (
	channelProfitSyncInterval = 15 * time.Minute
	channelProfitHTTPTimeout  = 12 * time.Second
	channelProfitMaxBodyBytes = 2 << 20
	channelProfitMaxWorkers   = 4
)

type ChannelProfitKeySummary struct {
	KeyId                 string  `json:"key_id"`
	KeyHint               string  `json:"key_hint"`
	UpstreamGroup         string  `json:"upstream_group"`
	UpstreamGroupRatio    float64 `json:"upstream_group_ratio"`
	RatioAvailable        bool    `json:"ratio_available"`
	CostUSD               float64 `json:"cost_usd"`
	CostAvailable         bool    `json:"cost_available"`
	Partial               bool    `json:"partial"`
	LastSyncedAt          int64   `json:"last_synced_at"`
	LastError             string  `json:"last_error"`
	UpstreamQuotaPerUnit  float64 `json:"upstream_quota_per_unit"`
	UpstreamConsumedQuota int64   `json:"upstream_consumed_quota"`
}

type ChannelProfitGroupRatio struct {
	Group string  `json:"group"`
	Ratio float64 `json:"ratio"`
}

type ChannelProfitRow struct {
	ChannelId       int                       `json:"channel_id"`
	ChannelName     string                    `json:"channel_name"`
	Enabled         bool                      `json:"enabled"`
	RevenueUSD      float64                   `json:"revenue_usd"`
	CostUSD         float64                   `json:"cost_usd"`
	CostAvailable   bool                      `json:"cost_available"`
	ProfitUSD       float64                   `json:"profit_usd"`
	ProfitAvailable bool                      `json:"profit_available"`
	Margin          float64                   `json:"margin"`
	MarginAvailable bool                      `json:"margin_available"`
	Partial         bool                      `json:"partial"`
	Status          string                    `json:"status"`
	LastSyncedAt    int64                     `json:"last_synced_at"`
	LastError       string                    `json:"last_error"`
	DownstreamRates []ChannelProfitGroupRatio `json:"downstream_rates"`
	Keys            []ChannelProfitKeySummary `json:"keys"`
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

type channelProfitSyncHandler struct{}

func (channelProfitSyncHandler) Type() string { return model.SystemTaskTypeChannelProfit }

func (channelProfitSyncHandler) Enabled() bool {
	count, err := model.CountEnabledChannelProfitConfigs()
	return err == nil && count > 0
}

func (channelProfitSyncHandler) Interval() time.Duration { return channelProfitSyncInterval }

func (channelProfitSyncHandler) NewPayload() any { return nil }

func (channelProfitSyncHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := SyncChannelProfits(ctx)
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

func StartChannelProfitSync() (*model.SystemTask, bool, error) {
	return EnqueueSystemTask(model.SystemTaskTypeChannelProfit, nil)
}

func SetChannelProfitMonitoring(channelId int, enabled bool) (*model.ChannelProfitConfig, error) {
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		return nil, err
	}
	if channel.Type != constant.ChannelTypeNewAPI {
		return nil, errors.New("profit monitoring only supports New API channels")
	}
	if !enabled {
		return model.SetChannelProfitConfig(channelId, false)
	}
	if _, err := channelProfitBaseURL(channel); err != nil {
		return nil, err
	}
	keys := uniqueChannelProfitKeys(channel)
	if len(keys) == 0 {
		return nil, errors.New("channel has no upstream key")
	}

	configs, err := model.ListEnabledChannelProfitConfigs()
	if err != nil {
		return nil, err
	}
	fingerprints := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		fingerprints[channelProfitKeyFingerprint(key)] = struct{}{}
	}
	for _, config := range configs {
		if config.ChannelId == channelId {
			continue
		}
		other, err := model.GetChannelById(config.ChannelId, true)
		if err != nil {
			return nil, err
		}
		for _, key := range uniqueChannelProfitKeys(other) {
			if _, shared := fingerprints[channelProfitKeyFingerprint(key)]; shared {
				return nil, fmt.Errorf("upstream key is already monitored by channel #%d", other.Id)
			}
		}
	}

	config, err := model.SetChannelProfitConfig(channelId, true)
	if err != nil {
		return nil, err
	}
	_, _, _ = StartChannelProfitSync()
	return config, nil
}

func SyncChannelProfits(ctx context.Context) (ChannelProfitSyncResult, error) {
	configs, err := model.ListEnabledChannelProfitConfigs()
	if err != nil {
		return ChannelProfitSyncResult{}, err
	}
	result := ChannelProfitSyncResult{Channels: len(configs)}
	if len(configs) == 0 {
		return result, nil
	}

	channels := make([]*model.Channel, 0, len(configs))
	owners := make(map[string][]int)
	for _, config := range configs {
		channel, err := model.GetChannelById(config.ChannelId, true)
		if err != nil {
			return result, err
		}
		channels = append(channels, channel)
		for _, key := range uniqueChannelProfitKeys(channel) {
			fingerprint := channelProfitKeyFingerprint(key)
			owners[fingerprint] = append(owners[fingerprint], channel.Id)
			result.Keys++
		}
	}

	usageDate := time.Now().In(time.Local).Format("2006-01-02")
	semaphore := make(chan struct{}, channelProfitMaxWorkers)
	var waitGroup sync.WaitGroup
	var resultMu sync.Mutex
	for _, channel := range channels {
		channel := channel
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}

			synced, failed := syncChannelProfit(ctx, channel, usageDate, owners)
			resultMu.Lock()
			result.Synced += synced
			result.Failed += failed
			resultMu.Unlock()
		}()
	}
	waitGroup.Wait()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func syncChannelProfit(ctx context.Context, channel *model.Channel, usageDate string, owners map[string][]int) (int, int) {
	keys := uniqueChannelProfitKeys(channel)
	if len(keys) == 0 {
		return 0, 1
	}
	baseURL, err := channelProfitBaseURL(channel)
	if err != nil {
		for _, key := range keys {
			saveChannelProfitFailure(channel.Id, usageDate, key, err)
		}
		return 0, len(keys)
	}

	settings := channel.GetSetting()
	client, err := GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		for _, key := range keys {
			saveChannelProfitFailure(channel.Id, usageDate, key, err)
		}
		return 0, len(keys)
	}
	quotaPerUnit, err := fetchChannelProfitQuotaPerUnit(ctx, client, baseURL)
	if err != nil {
		for _, key := range keys {
			saveChannelProfitFailure(channel.Id, usageDate, key, err)
		}
		return 0, len(keys)
	}

	synced := 0
	failed := 0
	for _, key := range keys {
		fingerprint := channelProfitKeyFingerprint(key)
		if len(owners[fingerprint]) > 1 {
			err := fmt.Errorf("upstream key is shared by monitored channels %v", owners[fingerprint])
			saveChannelProfitFailure(channel.Id, usageDate, key, err)
			failed++
			continue
		}

		usage, err := fetchChannelProfitUsage(ctx, client, baseURL, key)
		if err != nil {
			saveChannelProfitFailure(channel.Id, usageDate, key, err)
			failed++
			continue
		}
		group, ratio, ratioAvailable, ratioErr := fetchChannelProfitGroup(ctx, client, baseURL, key)
		if err := updateChannelProfitSnapshot(channel.Id, usageDate, key, usage, quotaPerUnit, group, ratio, ratioAvailable, ratioErr); err != nil {
			saveChannelProfitFailure(channel.Id, usageDate, key, err)
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
	now := common.GetTimestamp()
	fingerprint := channelProfitKeyFingerprint(key)
	snapshot, err := model.GetChannelProfitSnapshot(channelId, usageDate, fingerprint)
	if err == nil {
		if currentQuota < snapshot.CurrentQuota {
			snapshot.LastSyncedAt = now
			snapshot.LastError = "upstream cumulative usage counter decreased"
			return model.SaveChannelProfitSnapshot(snapshot)
		}
		if !ratioAvailable && snapshot.RatioAvailable {
			group = snapshot.UpstreamGroup
			ratio = snapshot.UpstreamGroupRatio
			ratioAvailable = true
		}
		snapshot.CurrentQuota = currentQuota
		snapshot.UpstreamQuotaPerUnit = quotaPerUnit
		snapshot.UpstreamGroup = group
		snapshot.UpstreamGroupRatio = ratio
		snapshot.RatioAvailable = ratioAvailable
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
		if !ratioAvailable && previous.RatioAvailable {
			group = previous.UpstreamGroup
			ratio = previous.UpstreamGroupRatio
			ratioAvailable = true
		}
		currentDate, parseErr := time.ParseInLocation("2006-01-02", usageDate, time.Local)
		if parseErr != nil {
			return parseErr
		}
		previousIsRecent := previous.UsageDate == currentDate.AddDate(0, 0, -1).Format("2006-01-02") &&
			previous.LastSyncedAt >= currentDate.Add(-2*channelProfitSyncInterval).Unix()
		if currentQuota >= previous.CurrentQuota && previousIsRecent {
			baseline = previous.CurrentQuota
			partial = false
		} else if currentQuota < previous.CurrentQuota {
			lastError = "upstream cumulative usage counter decreased"
		}
	} else if !errors.Is(previousErr, gorm.ErrRecordNotFound) {
		return previousErr
	}

	snapshot = &model.ChannelProfitSnapshot{
		ChannelId:            channelId,
		UsageDate:            usageDate,
		KeyFingerprint:       fingerprint,
		KeyHint:              model.MaskTokenKey(key),
		BaselineQuota:        baseline,
		CurrentQuota:         currentQuota,
		UpstreamQuotaPerUnit: quotaPerUnit,
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

	configs, err := model.ListChannelProfitConfigs()
	if err != nil {
		return nil, err
	}
	configByChannel := make(map[int]bool, len(configs))
	for _, config := range configs {
		configByChannel[config.ChannelId] = config.Enabled
	}

	channels, err := model.GetAllChannels(0, 0, true, true)
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
	for _, channel := range channels {
		if channel.Type != constant.ChannelTypeNewAPI {
			continue
		}
		enabled := configByChannel[channel.Id]
		if !enabled && !includeDisabled {
			continue
		}
		row := buildChannelProfitRow(channel, enabled, quotaByChannel[channel.Id], snapshotsByChannel[channel.Id])
		summary.Rows = append(summary.Rows, row)
		if !enabled {
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

func buildChannelProfitRow(channel *model.Channel, enabled bool, revenueQuota int64, snapshots []*model.ChannelProfitSnapshot) ChannelProfitRow {
	row := ChannelProfitRow{
		ChannelId:       channel.Id,
		ChannelName:     channel.Name,
		Enabled:         enabled,
		RevenueUSD:      float64(revenueQuota) / common.QuotaPerUnit,
		Status:          "disabled",
		DownstreamRates: make([]ChannelProfitGroupRatio, 0),
		Keys:            make([]ChannelProfitKeySummary, 0, len(snapshots)),
	}
	groupRates := ratio_setting.GetGroupRatioCopy()
	for _, group := range channel.GetGroups() {
		if ratio, ok := groupRates[group]; ok {
			row.DownstreamRates = append(row.DownstreamRates, ChannelProfitGroupRatio{Group: group, Ratio: ratio})
		}
	}
	if !enabled {
		return row
	}

	expectedKeys := len(uniqueChannelProfitKeys(channel))
	row.CostAvailable = expectedKeys > 0 && len(snapshots) == expectedKeys
	row.Status = "synced"
	for _, snapshot := range snapshots {
		consumed := snapshot.CurrentQuota - snapshot.BaselineQuota
		costAvailable := snapshot.UpstreamQuotaPerUnit > 0 && consumed >= 0
		cost := 0.0
		if costAvailable {
			cost = float64(consumed) / snapshot.UpstreamQuotaPerUnit
		} else {
			row.CostAvailable = false
		}
		keyId := snapshot.KeyFingerprint
		if len(keyId) > 12 {
			keyId = keyId[:12]
		}
		key := ChannelProfitKeySummary{
			KeyId:                 keyId,
			KeyHint:               snapshot.KeyHint,
			UpstreamGroup:         snapshot.UpstreamGroup,
			UpstreamGroupRatio:    snapshot.UpstreamGroupRatio,
			RatioAvailable:        snapshot.RatioAvailable,
			CostUSD:               cost,
			CostAvailable:         costAvailable,
			Partial:               snapshot.Partial,
			LastSyncedAt:          snapshot.LastSyncedAt,
			LastError:             snapshot.LastError,
			UpstreamQuotaPerUnit:  snapshot.UpstreamQuotaPerUnit,
			UpstreamConsumedQuota: consumed,
		}
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
	if len(snapshots) == 0 {
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
		Group string `json:"group"`
		Other string `json:"other"`
	} `json:"data"`
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

func fetchChannelProfitGroup(ctx context.Context, client *http.Client, baseURL string, key string) (string, float64, bool, error) {
	response := channelProfitLogResponse{}
	if err := fetchChannelProfitJSON(ctx, client, baseURL+"/api/log/token", key, &response); err != nil {
		return "", 0, false, err
	}
	if !response.Success {
		return "", 0, false, errors.New(response.Message)
	}
	if len(response.Data) == 0 {
		return "", 0, false, errors.New("upstream token has no usage log for ratio discovery")
	}

	group := response.Data[0].Group
	other := make(map[string]any)
	if err := common.UnmarshalJsonStr(response.Data[0].Other, &other); err != nil {
		return group, 0, false, fmt.Errorf("decode upstream log ratio: %w", err)
	}
	ratio, ok := other["group_ratio"].(float64)
	if !ok || ratio < 0 {
		return group, 0, false, errors.New("upstream log did not include group_ratio")
	}
	return group, ratio, true, nil
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
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
		return fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
	}
	if err := common.DecodeJson(io.LimitReader(resp.Body, channelProfitMaxBodyBytes), dest); err != nil {
		return err
	}
	return nil
}

func channelProfitBaseURL(channel *model.Channel) (string, error) {
	if channel.BaseURL == nil || strings.TrimSpace(*channel.BaseURL) == "" {
		return "", errors.New("channel base URL is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(*channel.BaseURL), "/")
	if strings.HasSuffix(baseURL, "/v1") {
		baseURL = strings.TrimSuffix(baseURL, "/v1")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("channel base URL is invalid")
	}
	return baseURL, nil
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
