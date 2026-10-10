package service

// Adapted from the cost-estimation/reconciliation design in
// atelier-of-dongn/YMeng-CC-New-API (ip-policy, AGPL-3.0).
// This integration keeps request-time configuration snapshots and never treats
// an estimate, missing usage or an unmatched upstream request as an actual cost.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
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
	// Keep their cost unknown until an exact upstream charge is available.
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
	TokenName string   `json:"token_name"`
	Group     string   `json:"group"`
	RequestID string   `json:"request_id"`
	Type      int      `json:"type"`
	Quota     *float64 `json:"quota"`
	CreatedAt int64    `json:"created_at"`
	Other     string   `json:"other"`
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
		if group.AccessToken == "" {
			group.AccessToken = strings.TrimSpace(monitor.AccessToken)
		}
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

// Poll token-scoped logs, never an account-wide log stream that could attribute
// another key's request to this channel. Daily snapshots remain independent.
func syncChannelProfitRequestCosts(ctx context.Context, group *channelProfitGroup, client *http.Client) error {
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
	var syncErr error
	if monitor != nil && monitor.Provider == UpstreamMonitorProviderSub2API {
		for _, key := range group.Keys {
			scope := model.ProfitCostFingerprint(siteID + ":" + model.ProfitCostFingerprint(key.Value))
			state := model.ChannelProfitKeyState{ID: scope, SiteID: siteID}
			if err := model.DB.Where("id = ?", scope).Limit(1).Find(&state).Error; err != nil {
				return err
			}
			_, ratio, available, ratioErr := fetchChannelProfitSub2APIGroup(ctx, client, group.BaseURL, key.Value)
			if available {
				state.Ratio, state.UpdatedAt = &ratio, now
			} else if ratioErr == nil {
				ratioErr = errors.New("upstream key group ratio unavailable")
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
			syncErr = errors.New("upstream model pricing unavailable; ratio fallback estimates only")
		}
	}
	if pricing.QuotaPerUnit <= 0 {
		syncErr = errors.Join(syncErr, errors.New("upstream quota unit unavailable; actual request cost reconciliation unavailable"))
	}
	if pricing.PricingJSON == "" && syncErr == nil {
		syncErr = errors.New("upstream model pricing unavailable; ratio fallback estimates only")
	}
	metadata := map[string]channelProfitNewAPIKeyMetadata{}
	ratios := map[string]float64{}
	if group.AccessToken != "" {
		metadata, ratios, err = fetchChannelProfitNewAPIMetadata(ctx, client, group.BaseURL, group.AccessToken, group.Keys, monitor)
		if err != nil {
			metadata, ratios = nil, nil
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
			}
		}
		var response struct {
			Success bool                   `json:"success"`
			Data    []channelProfitCostLog `json:"data"`
		}
		if err := fetchChannelProfitJSON(ctx, client, group.BaseURL+"/api/log/token", key.Value, &response); err != nil || !response.Success {
			state.LastError = "upstream request logs unavailable"
			group.CostLogs[key.Value] = channelProfitTokenLogs{Err: errors.New(state.LastError)}
			if err := model.SaveChannelProfitKeyState(&state); err != nil {
				return err
			}
			syncErr = errors.New(state.LastError)
			continue
		}
		group.CostLogs[key.Value] = channelProfitTokenLogs{Entries: response.Data}
		state.LastError = ""
		latest := int64(-1)
		for _, entry := range response.Data {
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
		if pricing.QuotaPerUnit <= 0 {
			continue
		}
		// Conflicting duplicate IDs are not safe to reconcile.
		charges := map[string]float64{}
		conflicts := map[string]bool{}
		for _, entry := range response.Data {
			if entry.Type != model.LogTypeConsume || entry.RequestID == "" || entry.Quota == nil {
				continue
			}
			quota, valid := profitNumber(*entry.Quota)
			if !valid {
				continue
			}
			if previous, ok := charges[entry.RequestID]; ok && previous != quota {
				conflicts[entry.RequestID] = true
			}
			charges[entry.RequestID] = quota
		}
		for id, quota := range charges {
			if conflicts[id] {
				syncErr = errors.New("conflicting upstream request costs")
				continue
			}
			if err := model.ReconcileChannelProfitCost(scope, id, quota/pricing.QuotaPerUnit); err != nil {
				return fmt.Errorf("reconcile upstream request cost: %w", err)
			}
		}
	}
	pricing.LastError = errorText(syncErr)
	if err := model.SaveChannelProfitPricing(&pricing); err != nil {
		return err
	}
	return syncErr
}
