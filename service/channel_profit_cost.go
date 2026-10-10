package service

// Adapted from the cost-estimation design in
// atelier-of-dongn/YMeng-CC-New-API (ip-policy, AGPL-3.0).
// This integration keeps request-time configuration snapshots and never treats
// an estimate or missing usage as an actual cost.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

var profitUnsupportedProbe = regexp.MustCompile(`\b(param|header|hour|minute|weekday|month|day|u)\s*\(`)

func profitNumber(value any) (float64, bool) {
	var number float64
	switch v := value.(type) {
	case float64:
		number = v
	case int:
		number = float64(v)
	case int64:
		number = float64(v)
	default:
		return 0, false
	}
	return number, number >= 0 && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func estimateChannelProfitCost(snapshot *model.ChannelProfitRequestSnapshot, log *model.Log, other *model.LogOther) (float64, string, string) {
	ratio := snapshot.Config.ManualRatio
	if ratio == nil && snapshot.KeyState != nil {
		ratio = snapshot.KeyState.Ratio
	}
	if ratio == nil {
		return 0, "", "ratio_unavailable"
	}
	if _, ok := profitNumber(*ratio); !ok {
		return 0, "", "invalid_cost"
	}
	factor := snapshot.Config.CostFactor
	if factor <= 0 {
		factor = 1
	}
	values := other.Snapshot()
	if *ratio == 0 {
		return 0, "upstream_ratio", ""
	}
	name := log.ModelName
	if mapped, ok := values["upstream_model_name"].(string); ok && mapped != "" {
		name = mapped
	}
	var prices map[string]model.Pricing
	if snapshot.Pricing != nil && common.UnmarshalJsonStr(snapshot.Pricing.PricingJSON, &prices) == nil {
		if pricing, ok := prices[name]; ok {
			cost, reason := channelProfitUsageCost(pricing, snapshot.Pricing.QuotaPerUnit, log, values)
			if reason == "" {
				return cost * *ratio * factor, "upstream_pricing", ""
			}
			// Unsupported expressions or missing facts must remain explicit. Falling
			// back here would hide a known difference between upstream and local billing.
			return 0, "", reason
		}
	}
	// This is only a labelled estimate: local and upstream base prices may differ.
	// Reject surcharges/tasks because their complete upstream cost is not known.
	if values["tool_surcharges"] != nil || values["is_task"] == true || values["image_count"] != nil || values["billing_unit"] == "image" {
		return 0, "", "usage_unsupported"
	}
	localRatio, ok := profitNumber(values["group_ratio"])
	if special, valid := profitNumber(values["user_group_ratio"]); valid {
		localRatio, ok = special, true
	}
	if !ok || localRatio <= 0 || log.Quota < 0 {
		return 0, "", "pricing_unavailable"
	}
	return float64(log.Quota) / common.QuotaPerUnit / localRatio * *ratio * factor, "local_ratio_fallback", ""
}

func channelProfitUsageCost(pricing model.Pricing, qpu float64, log *model.Log, values map[string]any) (float64, string) {
	if values["is_task"] == true || values["tool_surcharges"] != nil || values["image_count"] != nil || values["billing_unit"] == "image" {
		return 0, "usage_unsupported"
	}
	if log.PromptTokens < 0 || log.CompletionTokens < 0 {
		return 0, "invalid_usage"
	}
	usage := &dto.Usage{PromptTokens: log.PromptTokens, CompletionTokens: log.CompletionTokens}
	counts := map[string]int{}
	for _, key := range []string{"cache_tokens", "cache_creation_tokens", "cache_write_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h", "image_output", "image_cache_tokens", "audio_input_token_count"} {
		if raw, exists := values[key]; exists {
			number, valid := profitNumber(raw)
			if !valid || number > math.MaxInt32 || math.Trunc(number) != number {
				return 0, "invalid_usage"
			}
			counts[key] = int(number)
		}
	}
	usage.PromptTokensDetails.CachedTokens = counts["cache_tokens"]
	totalWrite := max(counts["cache_creation_tokens"], counts["cache_write_tokens"], counts["cache_creation_tokens_5m"]+counts["cache_creation_tokens_1h"])
	usage.PromptTokensDetails.CachedCreationTokens = totalWrite
	if values["usage_semantic"] == "anthropic" {
		usage.UsageSemantic = "anthropic"
		usage.ClaudeCacheCreation1hTokens = counts["cache_creation_tokens_1h"]
		usage.ClaudeCacheCreation5mTokens = max(counts["cache_creation_tokens_5m"], totalWrite-usage.ClaudeCacheCreation1hTokens)
	}
	// These legacy log fields do not carry a complete multimodal usage contract.
	// Keep their cost unknown rather than estimating from incomplete facts.
	if counts["image_output"] > 0 || counts["image_cache_tokens"] > 0 || counts["audio_input_token_count"] > 0 || values["audio"] != nil {
		return 0, "usage_unsupported"
	}
	if pricing.BillingMode == "tiered_expr" {
		if pricing.BillingExpr == "" || profitUnsupportedProbe.MatchString(pricing.BillingExpr) {
			return 0, "pricing_unsupported"
		}
		vars := billingexpr.UsedVars(pricing.BillingExpr)
		for _, key := range []string{"img", "img_cr", "img_o", "ai", "ao", "image_count"} {
			if vars[key] {
				return 0, "usage_unsupported"
			}
		}
		params := BuildTieredTokenParams(usage, usage.UsageSemantic == "anthropic", vars)
		cost, _, err := billingexpr.RunExpr(pricing.BillingExpr, params)
		if err != nil {
			return 0, "pricing_unsupported"
		}
		if _, ok := profitNumber(cost); !ok {
			return 0, "invalid_cost"
		}
		return cost / 1_000_000, ""
	}
	if pricing.QuotaType == 1 {
		if _, ok := profitNumber(pricing.ModelPrice); !ok {
			return 0, "invalid_cost"
		}
		return pricing.ModelPrice, ""
	}
	if pricing.QuotaType != 0 || qpu <= 0 || math.IsInf(qpu, 0) || math.IsNaN(qpu) {
		return 0, "pricing_unavailable"
	}
	cacheRatio, writeRatio := 1.0, 1.25
	if pricing.CacheRatio != nil {
		cacheRatio = *pricing.CacheRatio
	}
	if pricing.CreateCacheRatio != nil {
		writeRatio = *pricing.CreateCacheRatio
	}
	for _, n := range []float64{cacheRatio, writeRatio, pricing.ModelRatio, pricing.CompletionRatio} {
		if _, ok := profitNumber(n); !ok {
			return 0, "invalid_cost"
		}
	}
	prompt := float64(usage.PromptTokens)
	if usage.UsageSemantic != "anthropic" {
		prompt = max(prompt-float64(usage.PromptTokensDetails.CachedTokens)-float64(totalWrite), 0)
	}
	write1h := usage.ClaudeCacheCreation1hTokens
	tokens := prompt + float64(usage.PromptTokensDetails.CachedTokens)*cacheRatio + float64(totalWrite-write1h)*writeRatio + float64(write1h)*writeRatio*1.6 + float64(usage.CompletionTokens)*pricing.CompletionRatio
	return tokens * pricing.ModelRatio / qpu, ""
}

type channelProfitCostLog struct {
	TokenName string `json:"token_name"`
	Group     string `json:"group"`
	Type      int    `json:"type"`
	CreatedAt int64  `json:"created_at"`
	Other     string `json:"other"`
}

type channelProfitTokenLogs struct {
	Entries []channelProfitCostLog
	Err     error
}

// Monitor credentials belong to one upstream account. Only send them to that
// site, and never replace the API key used for token-scoped usage or logs.
type channelProfitMonitorTransport struct {
	base    http.RoundTripper
	monitor *model.UpstreamMonitor
}

func (transport channelProfitMonitorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	monitor := transport.monitor
	baseURL, err := url.Parse(monitor.BaseURL)
	if err == nil && strings.EqualFold(request.URL.Scheme, baseURL.Scheme) && strings.EqualFold(request.URL.Host, baseURL.Host) && strings.HasPrefix(request.URL.Path, strings.TrimRight(baseURL.Path, "/")+"/") {
		auth := request.Header.Get("Authorization")
		path := strings.TrimPrefix(request.URL.Path, strings.TrimRight(baseURL.Path, "/"))
		if monitor.AccessToken != "" && monitor.NewAPIUserID > 0 && (auth == "Bearer "+monitor.AccessToken || (auth == "" && (path == "/api/status" || path == "/api/pricing"))) {
			request = request.Clone(request.Context())
			request.Header.Set("Authorization", "Bearer "+monitor.AccessToken)
			request.Header.Set("New-Api-User", fmt.Sprint(monitor.NewAPIUserID))
		}
	}
	return transport.base.RoundTrip(request)
}

func channelProfitMonitorClient(group *channelProfitGroup, client *http.Client) (*http.Client, *model.UpstreamMonitor, error) {
	monitors, err := model.ListUpstreamMonitors()
	if err != nil {
		return nil, nil, err
	}
	for _, monitor := range monitors {
		if model.ProfitCostSiteID(monitor.BaseURL) != model.ProfitCostSiteID(group.BaseURL) {
			continue
		}
		if monitor.Provider != UpstreamMonitorProviderNewAPI {
			return client, monitor, nil
		}
		group.AccessToken = strings.TrimSpace(monitor.AccessToken)
		copyClient := *client
		base := client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		copyClient.Transport = channelProfitMonitorTransport{base: base, monitor: monitor}
		return &copyClient, monitor, nil
	}
	return client, nil, nil
}

// Discover only the metadata needed for estimates; this never reconciles requests.
func syncChannelProfitRequestCosts(ctx context.Context, group *channelProfitGroup, client *http.Client, providers ...string) error {
	client, monitor, err := channelProfitMonitorClient(group, client)
	if err != nil {
		return err
	}
	group.CostLogs = make(map[string]channelProfitTokenLogs, len(group.Keys))
	siteID := model.ProfitCostSiteID(group.BaseURL)
	pricing := model.ChannelProfitPricing{ID: siteID}
	if err := model.DB.Where("id = ?", siteID).Limit(1).Find(&pricing).Error; err != nil {
		return err
	}
	now := time.Now().Unix()
	if group.CostMode == "request" {
		pricing.LastError = ""
		return model.SaveChannelProfitPricing(&pricing)
	}
	var syncErr error
	isSub2API := monitor != nil && monitor.Provider == UpstreamMonitorProviderSub2API
	if len(providers) == 0 && !isSub2API {
		keys := make([]string, 0, len(group.Keys))
		for _, key := range group.Keys {
			keys = append(keys, key.Value)
		}
		backend, probeErr := detectChannelProfitBackend(ctx, client, group.BaseURL, keys, time.Now().In(time.Local).Format("2006-01-02"))
		if probeErr == nil {
			isSub2API = backend.Provider == channelProfitProviderSub2API
		} else if monitor == nil {
			pricing.LastError = errorText(probeErr)
			if err := model.SaveChannelProfitPricing(&pricing); err != nil {
				return err
			}
			return probeErr
		}
	}
	if len(providers) > 0 {
		isSub2API = providers[0] == channelProfitProviderSub2API
	}
	if isSub2API {
		// New API prices cached under the same site must not price a Sub2API request.
		pricing.PricingJSON, pricing.QuotaPerUnit = "", 0
		if group.ManualRatio != nil {
			pricing.LastError = ""
			return model.SaveChannelProfitPricing(&pricing)
		}
		var accountRatios map[string]float64
		var accountErr error
		accountLoaded := false
		for _, key := range group.Keys {
			scope := model.ProfitCostFingerprint(siteID + ":" + model.ProfitCostFingerprint(key.Value))
			state := model.ChannelProfitKeyState{ID: scope, SiteID: siteID}
			if err := model.DB.Where("id = ?", scope).Limit(1).Find(&state).Error; err != nil {
				return err
			}
			_, ratio, available, ratioErr := fetchChannelProfitSub2APIGroup(ctx, client, group.BaseURL, key.Value)
			if !available && monitor != nil && monitor.Provider == UpstreamMonitorProviderSub2API {
				if !accountLoaded {
					accountRatios, accountErr = fetchChannelProfitSub2APIAccountRatios(ctx, client, monitor)
					accountLoaded = true
				}
				if accountRatio, ok := accountRatios[key.Value]; ok {
					ratio, available, ratioErr = accountRatio, true, nil
				} else {
					ratioErr = errors.Join(ratioErr, accountErr)
				}
			}
			if available {
				state.Ratio, state.UpdatedAt = &ratio, now
			} else if ratioErr == nil {
				ratioErr = errors.New("Sub2API key ratio unavailable: configure the matching upstream account or a manual cost ratio")
			}
			state.LastError = errorText(ratioErr)
			if err := model.SaveChannelProfitKeyState(&state); err != nil {
				return err
			}
			syncErr = errors.Join(syncErr, ratioErr)
		}
		pricing.LastError = errorText(syncErr)
		if err := model.SaveChannelProfitPricing(&pricing); err != nil {
			return err
		}
		return syncErr
	}
	if now-pricing.LastAttemptAt >= 300 {
		pricing.LastAttemptAt = now
		qpu, quotaErr := fetchChannelProfitQuotaPerUnit(ctx, client, group.BaseURL)
		if quotaErr == nil {
			pricing.QuotaPerUnit = qpu
		}
		var response struct {
			Success bool              `json:"success"`
			Data    []json.RawMessage `json:"data"`
		}
		err := fetchChannelProfitJSON(ctx, client, group.BaseURL+"/api/pricing", "", &response)
		if err == nil && !response.Success {
			err = errors.New("upstream pricing unavailable")
		}
		if err == nil {
			prices := map[string]model.Pricing{}
			for _, raw := range response.Data {
				var price model.Pricing
				var fields map[string]json.RawMessage
				if common.Unmarshal(raw, &price) != nil || common.Unmarshal(raw, &fields) != nil || price.ModelName == "" {
					continue
				}
				// An omitted price is not a free model. Preserve explicit zero only.
				if price.BillingMode != "tiered_expr" {
					required := []string{"quota_type", "model_ratio", "completion_ratio"}
					if price.QuotaType == 1 {
						required = []string{"quota_type", "model_price"}
					}
					complete := true
					for _, field := range required {
						if len(fields[field]) == 0 || string(fields[field]) == "null" {
							complete = false
						}
					}
					if !complete {
						continue
					}
				}
				prices[price.ModelName] = price
			}
			data, encodeErr := common.Marshal(prices)
			err = encodeErr
			if err == nil {
				pricing.PricingJSON = string(data)
				pricing.UpdatedAt = now
			}
		}
		if err != nil {
			syncErr = fmt.Errorf("upstream model pricing unavailable; ratio fallback estimates only: %w", err)
		}
	}
	if pricing.PricingJSON == "" && syncErr == nil {
		syncErr = errors.New("upstream model pricing unavailable; ratio fallback estimates only")
	}
	metadata := map[string]channelProfitNewAPIKeyMetadata{}
	ratios := map[string]float64{}
	if group.AccessToken != "" {
		metadata, ratios, err = fetchChannelProfitNewAPIMetadata(ctx, client, group.BaseURL, group.AccessToken, group.Keys, monitor)
		if err != nil {
			ratios = nil
		}
	}
	for _, key := range group.Keys {
		scope := model.ProfitCostFingerprint(siteID + ":" + model.ProfitCostFingerprint(key.Value))
		state := model.ChannelProfitKeyState{ID: scope, SiteID: siteID}
		if err := model.DB.Where("id = ?", scope).Limit(1).Find(&state).Error; err != nil {
			return err
		}
		// The account endpoint verifies the key's group before using its ratio.
		// It also refreshes idle keys without pretending an old log is fresh.
		if info, ok := metadata[key.Fingerprint]; ok && info.Group != "auto" {
			if ratio, ok := ratios[info.Group]; ok {
				state.Ratio, state.UpdatedAt = &ratio, now
				state.LastError = ""
				if err := model.SaveChannelProfitKeyState(&state); err != nil {
					return err
				}
				continue
			}
		}
		entries, logErr := fetchChannelProfitRequestLogs(ctx, client, group, key, metadata[key.Fingerprint].Name)
		if logErr != nil {
			state.LastError = logErr.Error()
			group.CostLogs[key.Value] = channelProfitTokenLogs{Err: logErr}
			if err := model.SaveChannelProfitKeyState(&state); err != nil {
				return err
			}
			syncErr = errors.Join(syncErr, logErr)
			continue
		}
		group.CostLogs[key.Value] = channelProfitTokenLogs{Entries: entries}
		state.LastError = ""
		latest := int64(-1)
		for _, entry := range entries {
			if entry.Type != model.LogTypeConsume || entry.CreatedAt < latest {
				continue
			}
			var other map[string]any
			if common.UnmarshalJsonStr(entry.Other, &other) != nil {
				continue
			}
			ratio, ok := profitNumber(other["group_ratio"])
			if special, valid := profitNumber(other["user_group_ratio"]); valid {
				ratio, ok = special, true
			}
			if ok && entry.CreatedAt >= state.UpdatedAt {
				state.Ratio = &ratio
				state.UpdatedAt = entry.CreatedAt
				latest = entry.CreatedAt
			}
		}
		if err := model.SaveChannelProfitKeyState(&state); err != nil {
			return err
		}
	}
	pricing.LastError = errorText(syncErr)
	if err := model.SaveChannelProfitPricing(&pricing); err != nil {
		return err
	}
	return syncErr
}

func fetchChannelProfitSub2APIAccountRatios(ctx context.Context, client *http.Client, monitor *model.UpstreamMonitor) (map[string]float64, error) {
	if monitor.AccessToken == "" {
		return nil, errors.New("Sub2API account token unavailable; configure upstream account credentials or a manual cost ratio")
	}
	keyGroups := map[string]string{}
	listed := 0
	complete := false
	for page := 1; page <= 100; page++ {
		query := url.Values{"page": {strconv.Itoa(page)}, "page_size": {"100"}}
		var response struct {
			Code int `json:"code"`
			Data struct {
				Total *int `json:"total"`
				Items []struct {
					Key     string `json:"key"`
					GroupID *int64 `json:"group_id"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := fetchChannelProfitJSON(ctx, client, monitor.BaseURL+"/api/v1/keys?"+query.Encode(), monitor.AccessToken, &response); err != nil {
			return nil, fmt.Errorf("fetch Sub2API account keys: %w", err)
		}
		if response.Code != 0 || response.Data.Total == nil || *response.Data.Total < 0 {
			return nil, errors.New("Sub2API account returned an invalid API key list")
		}
		listed += len(response.Data.Items)
		for _, key := range response.Data.Items {
			if key.Key == "" || key.GroupID == nil {
				continue
			}
			groupID := strconv.FormatInt(*key.GroupID, 10)
			if previous, ok := keyGroups[key.Key]; ok && previous != groupID {
				return nil, errors.New("Sub2API account returned conflicting key groups")
			}
			keyGroups[key.Key] = groupID
		}
		if listed >= *response.Data.Total {
			complete = true
			break
		}
		if len(response.Data.Items) == 0 {
			break
		}
	}
	if !complete {
		return nil, errors.New("Sub2API account returned an incomplete API key list")
	}
	var snapshot upstreamMonitorGroupSnapshot
	age := time.Now().Unix() - monitor.LastSyncedAt
	if monitor.LastError != "" || monitor.LastSyncedAt <= 0 || age < 0 || age > 900 || common.UnmarshalJsonStr(monitor.GroupsJSON, &snapshot) != nil || len(snapshot.Groups) == 0 {
		headers := http.Header{"Authorization": {"Bearer " + monitor.AccessToken}}
		groups, err := fetchUpstreamMonitorJSON(ctx, client, http.MethodGet, monitor.BaseURL+"/api/v1/groups/available", headers, nil)
		if err != nil {
			return nil, fmt.Errorf("fetch Sub2API account groups: %w", err)
		}
		rates, err := fetchUpstreamMonitorJSON(ctx, client, http.MethodGet, monitor.BaseURL+"/api/v1/groups/rates", headers, nil)
		if err != nil {
			return nil, fmt.Errorf("fetch Sub2API account rates: %w", err)
		}
		_, data, err := parseSub2APIGroups(groups, rates)
		if err != nil {
			return nil, err
		}
		if err := common.UnmarshalJsonStr(data, &snapshot); err != nil {
			return nil, err
		}
	}
	groupRatios := make(map[string]float64, len(snapshot.Groups))
	for _, group := range snapshot.Groups {
		if ratio, ok := upstreamMonitorNumber(group.Multiplier); ok && ratio >= 0 {
			groupRatios[group.ID] = ratio
		}
	}
	ratios := make(map[string]float64, len(keyGroups))
	for key, groupID := range keyGroups {
		if ratio, ok := groupRatios[groupID]; ok {
			ratios[key] = ratio
		}
	}
	return ratios, nil
}

func fetchChannelProfitRequestLogs(ctx context.Context, client *http.Client, group *channelProfitGroup, key *channelProfitGroupKey, tokenName string) ([]channelProfitCostLog, error) {
	if tokenName == "" || group.AccessToken == "" {
		var response struct {
			Success bool                   `json:"success"`
			Data    []channelProfitCostLog `json:"data"`
		}
		if err := fetchChannelProfitJSON(ctx, client, group.BaseURL+"/api/log/token", key.Value, &response); err != nil {
			return nil, fmt.Errorf("upstream request logs unavailable: %w", err)
		}
		if !response.Success {
			return nil, errors.New("/api/log/token: upstream rejected request logs; check API key permissions")
		}
		return response.Data, nil
	}
	entries := make([]channelProfitCostLog, 0)
	start := time.Now().Add(-24 * time.Hour).Unix()
	end := time.Now().Unix()
	listed := 0
	// Bound each poll to recent logs used only for group-ratio discovery.
	for page := 1; page <= 100; page++ {
		query := url.Values{"token_name": {tokenName}, "type": {"2"}, "p": {strconv.Itoa(page)}, "size": {"100"}}
		query.Set("start_timestamp", strconv.FormatInt(start, 10))
		query.Set("end_timestamp", strconv.FormatInt(end, 10))
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				Items    []channelProfitCostLog `json:"items"`
				Total    int                    `json:"total"`
				PageSize int                    `json:"page_size"`
			} `json:"data"`
		}
		if err := fetchChannelProfitJSON(ctx, client, group.BaseURL+"/api/log/self?"+query.Encode(), group.AccessToken, &response); err != nil {
			return nil, fmt.Errorf("upstream request logs unavailable: %w", err)
		}
		if !response.Success {
			return nil, errors.New("/api/log/self: upstream rejected request logs; check account permissions")
		}
		listed += len(response.Data.Items)
		for _, entry := range response.Data.Items {
			if entry.TokenName == tokenName {
				entries = append(entries, entry)
			}
		}
		pageSize := response.Data.PageSize
		if pageSize <= 0 {
			pageSize = 100
		}
		if page*pageSize >= response.Data.Total {
			if listed < response.Data.Total {
				return nil, errors.New("/api/log/self: upstream returned incomplete request logs")
			}
			return entries, nil
		}
		if len(response.Data.Items) == 0 {
			return nil, errors.New("/api/log/self: upstream returned incomplete request logs")
		}
	}
	return nil, errors.New("/api/log/self: upstream request log pagination limit exceeded")
}
