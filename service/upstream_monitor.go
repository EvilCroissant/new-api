/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

package service

import (
	"bytes"
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

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

const (
	UpstreamMonitorProviderNewAPI  = "newapi"
	UpstreamMonitorProviderSub2API = "sub2api"

	upstreamMonitorHTTPTimeout           = 12 * time.Second
	upstreamMonitorMaxBodySize           = 1 << 20
	upstreamMonitorAutomaticSyncInterval = time.Hour
)

type UpstreamMonitorCreateInput struct {
	Name         string
	BaseURL      string
	Provider     string
	NewAPIUserID int
	AccessToken  string
	RefreshToken string
}

type UpstreamMonitorUpdateInput struct {
	NewAPIUserID *int
	AccessToken  *string
	RefreshToken *string
}

type UpstreamMonitorDetectResult struct {
	BaseURL  string `json:"base_url"`
	Provider string `json:"provider,omitempty"`
	Detected bool   `json:"detected"`
}

// UpstreamMonitorDetail is the safe response used by the admin UI. Credentials
// are never included; snapshots are decoded only for rendering the monitored data.
type UpstreamMonitorDetail struct {
	Id                     int     `json:"id"`
	Name                   string  `json:"name"`
	BaseURL                string  `json:"base_url"`
	Provider               string  `json:"provider"`
	NewAPIUserID           int     `json:"new_api_user_id"`
	AccessTokenConfigured  bool    `json:"access_token_configured"`
	RefreshTokenConfigured bool    `json:"refresh_token_configured"`
	BalanceUSD             float64 `json:"balance_usd"`
	BalanceAvailable       bool    `json:"balance_available"`
	GroupCount             int     `json:"group_count"`
	PricingCount           int     `json:"pricing_count"`
	Groups                 any     `json:"groups,omitempty"`
	Pricing                any     `json:"pricing,omitempty"`
	LastSyncedAt           int64   `json:"last_synced_at"`
	LastError              string  `json:"last_error"`
	CreatedAt              int64   `json:"created_at"`
	UpdatedAt              int64   `json:"updated_at"`
}

// upstreamMonitorGroupSnapshot is the provider-neutral format persisted for
// the group data shown in the upstream monitor page.
type upstreamMonitorGroupSnapshot struct {
	Groups []upstreamMonitorGroup `json:"groups"`
}

type upstreamMonitorGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Multiplier  any    `json:"multiplier,omitempty"`
}

type upstreamMonitorHTTPError struct {
	StatusCode int
}

type UpstreamMonitorAutomaticSyncResult struct {
	Total  int `json:"total"`
	Synced int `json:"synced"`
	Failed int `json:"failed"`
}

type upstreamMonitorSyncHandler struct{}

func (upstreamMonitorSyncHandler) Type() string { return model.SystemTaskTypeUpstreamMonitor }

func (upstreamMonitorSyncHandler) Enabled() bool {
	count, err := model.CountUpstreamMonitors()
	return err == nil && count > 0
}

func (upstreamMonitorSyncHandler) Interval() time.Duration {
	return upstreamMonitorAutomaticSyncInterval
}

func (upstreamMonitorSyncHandler) NewPayload() any { return struct{}{} }

func (upstreamMonitorSyncHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := SyncAllUpstreamMonitors(ctx, NewSystemTaskProgressReporter(task, runnerID))
	if err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, result, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}

func init() {
	RegisterSystemTaskHandler(upstreamMonitorSyncHandler{})
}

func (err *upstreamMonitorHTTPError) Error() string {
	return fmt.Sprintf("upstream returned HTTP %d", err.StatusCode)
}

func NormalizeUpstreamMonitorURL(rawURL string) (string, error) {
	return normalizeUpstreamMonitorURL(rawURL, ValidateSSRFProtectedFetchURL)
}

func normalizeUpstreamMonitorURL(rawURL string, validateURL func(string) error) (string, error) {
	baseURL := strings.TrimSpace(rawURL)
	if baseURL == "" {
		return "", errors.New("upstream URL is required")
	}
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid upstream URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", errors.New("upstream URL must use HTTP or HTTPS")
	}
	if parsedURL.Host == "" {
		return "", errors.New("upstream URL must include a host")
	}
	if parsedURL.User != nil {
		return "", errors.New("upstream URL must not include credentials")
	}
	if parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return "", errors.New("upstream URL must not include a query or fragment")
	}

	parsedURL.Path = strings.TrimRight(parsedURL.Path, "/")
	if parsedURL.Path == "/v1" {
		parsedURL.Path = ""
	}
	parsedURL.RawPath = ""
	normalizedURL := strings.TrimRight(parsedURL.String(), "/")
	if validateURL != nil {
		if err := validateURL(normalizedURL); err != nil {
			return "", fmt.Errorf("upstream URL is not allowed: %w", err)
		}
	}
	return normalizedURL, nil
}

func DetectUpstreamMonitor(ctx context.Context, rawURL string) (UpstreamMonitorDetectResult, error) {
	baseURL, err := NormalizeUpstreamMonitorURL(rawURL)
	if err != nil {
		return UpstreamMonitorDetectResult{}, err
	}
	return detectUpstreamMonitorWithClient(ctx, upstreamMonitorHTTPClient(), baseURL)
}

func detectUpstreamMonitorWithClient(ctx context.Context, client *http.Client, baseURL string) (UpstreamMonitorDetectResult, error) {
	result := UpstreamMonitorDetectResult{BaseURL: baseURL}

	if body, err := fetchUpstreamMonitorJSON(ctx, client, http.MethodGet, baseURL+"/api/status", nil, nil); err == nil {
		status := struct {
			Success bool `json:"success"`
			Data    struct {
				QuotaPerUnit any `json:"quota_per_unit"`
			} `json:"data"`
		}{}
		if common.Unmarshal(body, &status) == nil && status.Success {
			if quotaPerUnit, ok := upstreamMonitorNumber(status.Data.QuotaPerUnit); ok && quotaPerUnit > 0 {
				result.Provider = UpstreamMonitorProviderNewAPI
				result.Detected = true
				return result, nil
			}
		}
	}

	if body, err := fetchUpstreamMonitorJSON(ctx, client, http.MethodGet, baseURL+"/health", nil, nil); err == nil {
		health := struct {
			Status string `json:"status"`
		}{}
		if common.Unmarshal(body, &health) == nil && strings.EqualFold(health.Status, "ok") {
			result.Provider = UpstreamMonitorProviderSub2API
			result.Detected = true
			return result, nil
		}
	}

	if body, err := fetchUpstreamMonitorJSON(ctx, client, http.MethodGet, baseURL+"/api/v1/settings/public", nil, nil); err == nil {
		response := struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{}
		if common.Unmarshal(body, &response) == nil && response.Code == 0 && strings.EqualFold(response.Message, "success") {
			result.Provider = UpstreamMonitorProviderSub2API
			result.Detected = true
		}
	}

	return result, nil
}

func CreateUpstreamMonitor(input UpstreamMonitorCreateInput) (*UpstreamMonitorDetail, error) {
	normalizedURL, err := NormalizeUpstreamMonitorURL(input.BaseURL)
	if err != nil {
		return nil, err
	}
	if err := validateUpstreamMonitorCreateInput(input); err != nil {
		return nil, err
	}
	if existing, err := model.GetUpstreamMonitorByBaseURL(normalizedURL); err == nil && existing != nil {
		return nil, errors.New("an upstream monitor already exists for this URL")
	} else if err != nil && !model.IsUpstreamMonitorNotFound(err) {
		return nil, err
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		parsedURL, parseErr := url.Parse(normalizedURL)
		if parseErr != nil {
			return nil, parseErr
		}
		name = parsedURL.Host
	}
	if len(name) > 100 {
		return nil, errors.New("upstream monitor name must not exceed 100 characters")
	}

	monitor := &model.UpstreamMonitor{
		Name:         name,
		BaseURL:      normalizedURL,
		Provider:     input.Provider,
		NewAPIUserID: input.NewAPIUserID,
		AccessToken:  strings.TrimSpace(input.AccessToken),
		RefreshToken: strings.TrimSpace(input.RefreshToken),
	}
	if err := model.CreateUpstreamMonitor(monitor); err != nil {
		return nil, err
	}
	if err := SyncUpstreamMonitor(monitor); err != nil {
		// The monitor remains saved so the administrator can correct credentials
		// and retry from the page. The visible LastError contains no credential data.
		return upstreamMonitorDetail(monitor, true)
	}
	return upstreamMonitorDetail(monitor, true)
}

func ListUpstreamMonitorDetails() ([]UpstreamMonitorDetail, error) {
	monitors, err := model.ListUpstreamMonitors()
	if err != nil {
		return nil, err
	}
	details := make([]UpstreamMonitorDetail, 0, len(monitors))
	for _, monitor := range monitors {
		detail, err := upstreamMonitorDetail(monitor, true)
		if err != nil {
			return nil, err
		}
		details = append(details, *detail)
	}
	return details, nil
}

func UpdateUpstreamMonitorCredentials(ctx context.Context, id int, input UpstreamMonitorUpdateInput) (*UpstreamMonitorDetail, error) {
	return updateUpstreamMonitorCredentialsWithClient(ctx, id, input, upstreamMonitorHTTPClient())
}

func updateUpstreamMonitorCredentialsWithClient(ctx context.Context, id int, input UpstreamMonitorUpdateInput, client *http.Client) (*UpstreamMonitorDetail, error) {
	if input.NewAPIUserID == nil && input.AccessToken == nil && input.RefreshToken == nil {
		return nil, errors.New("at least one credential field is required")
	}
	monitor, err := model.GetUpstreamMonitorByID(id)
	if err != nil {
		return nil, err
	}

	switch monitor.Provider {
	case UpstreamMonitorProviderNewAPI:
		if input.RefreshToken != nil {
			return nil, errors.New("New API monitors do not use a refresh token")
		}
	case UpstreamMonitorProviderSub2API:
		if input.NewAPIUserID != nil {
			return nil, errors.New("Sub2API monitors do not use a New API user ID")
		}
	default:
		return nil, errors.New("unsupported upstream monitor provider")
	}

	credentialUpdate := model.UpstreamMonitorCredentialUpdate{}
	if input.NewAPIUserID != nil {
		if *input.NewAPIUserID <= 0 {
			return nil, errors.New("New API user ID is required")
		}
		monitor.NewAPIUserID = *input.NewAPIUserID
		credentialUpdate.NewAPIUserID = &monitor.NewAPIUserID
	}
	if input.AccessToken != nil {
		accessToken := strings.TrimSpace(*input.AccessToken)
		if accessToken == "" {
			return nil, errors.New("upstream access token must not be empty")
		}
		monitor.AccessToken = accessToken
		credentialUpdate.AccessToken = &monitor.AccessToken
	}
	if input.RefreshToken != nil {
		refreshToken := strings.TrimSpace(*input.RefreshToken)
		if refreshToken == "" {
			return nil, errors.New("Sub2API refresh token must not be empty")
		}
		monitor.RefreshToken = refreshToken
		credentialUpdate.RefreshToken = &monitor.RefreshToken
	}

	if err := validateUpstreamMonitorCreateInput(UpstreamMonitorCreateInput{
		Provider:     monitor.Provider,
		NewAPIUserID: monitor.NewAPIUserID,
		AccessToken:  monitor.AccessToken,
		RefreshToken: monitor.RefreshToken,
	}); err != nil {
		return nil, err
	}
	if err := model.UpdateUpstreamMonitorCredentials(id, credentialUpdate); err != nil {
		return nil, err
	}

	_, saveErr := syncAndSaveUpstreamMonitorWithClient(ctx, monitor, client)
	if saveErr != nil {
		return nil, saveErr
	}
	return upstreamMonitorDetail(monitor, true)
}

func GetUpstreamMonitorDetail(id int) (*UpstreamMonitorDetail, error) {
	monitor, err := model.GetUpstreamMonitorByID(id)
	if err != nil {
		return nil, err
	}
	return upstreamMonitorDetail(monitor, true)
}

func SyncUpstreamMonitorByID(id int) (*UpstreamMonitorDetail, error) {
	return SyncUpstreamMonitorByIDWithContext(context.Background(), id)
}

func SyncUpstreamMonitorByIDWithContext(ctx context.Context, id int) (*UpstreamMonitorDetail, error) {
	monitor, err := model.GetUpstreamMonitorByID(id)
	if err != nil {
		return nil, err
	}
	if err := SyncUpstreamMonitorWithContext(ctx, monitor); err != nil {
		return nil, err
	}
	return upstreamMonitorDetail(monitor, true)
}

func SyncAllUpstreamMonitors(ctx context.Context, reportProgress func(processed, total int)) (UpstreamMonitorAutomaticSyncResult, error) {
	return syncAllUpstreamMonitorsWithClient(ctx, reportProgress, upstreamMonitorHTTPClient())
}

func syncAllUpstreamMonitorsWithClient(ctx context.Context, reportProgress func(processed, total int), client *http.Client) (UpstreamMonitorAutomaticSyncResult, error) {
	monitors, err := model.ListUpstreamMonitors()
	if err != nil {
		return UpstreamMonitorAutomaticSyncResult{}, err
	}
	result := UpstreamMonitorAutomaticSyncResult{Total: len(monitors)}
	if reportProgress != nil {
		reportProgress(0, result.Total)
	}
	for index, monitor := range monitors {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		syncErr, saveErr := syncAndSaveUpstreamMonitorWithClient(ctx, monitor, client)
		monitorErr := syncErr
		if saveErr != nil {
			monitorErr = saveErr
		}
		if monitorErr != nil {
			result.Failed++
			logger.LogWarn(ctx, fmt.Sprintf("automatic upstream monitor sync failed: id=%d name=%s err=%v", monitor.Id, monitor.Name, monitorErr))
		} else {
			result.Synced++
		}
		if reportProgress != nil {
			reportProgress(index+1, result.Total)
		}
	}
	return result, nil
}

func DeleteUpstreamMonitor(id int) error {
	if _, err := model.GetUpstreamMonitorByID(id); err != nil {
		return err
	}
	return model.DeleteUpstreamMonitor(id)
}

func SyncUpstreamMonitor(monitor *model.UpstreamMonitor) error {
	return SyncUpstreamMonitorWithContext(context.Background(), monitor)
}

func SyncUpstreamMonitorWithContext(ctx context.Context, monitor *model.UpstreamMonitor) error {
	syncErr, saveErr := syncAndSaveUpstreamMonitorWithClient(ctx, monitor, upstreamMonitorHTTPClient())
	if saveErr != nil {
		return saveErr
	}
	return syncErr
}

func syncAndSaveUpstreamMonitorWithClient(ctx context.Context, monitor *model.UpstreamMonitor, client *http.Client) (error, error) {
	if monitor == nil {
		return errors.New("upstream monitor is required"), nil
	}
	if client == nil {
		return errors.New("upstream monitor HTTP client is required"), nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	previousAccessToken := monitor.AccessToken
	previousRefreshToken := monitor.RefreshToken
	previousNewAPIUserID := monitor.NewAPIUserID
	var err error
	switch monitor.Provider {
	case UpstreamMonitorProviderNewAPI:
		err = syncNewAPIUpstreamMonitorWithContext(ctx, monitor, client)
	case UpstreamMonitorProviderSub2API:
		err = syncSub2APIUpstreamMonitorWithContext(ctx, monitor, client)
	default:
		err = errors.New("unsupported upstream monitor provider")
	}

	monitor.LastSyncedAt = time.Now().Unix()
	monitor.LastError = upstreamMonitorErrorText(err)
	saveErr := model.SaveUpstreamMonitorSyncResult(monitor, previousNewAPIUserID, previousAccessToken, previousRefreshToken)
	return err, saveErr
}

func syncNewAPIUpstreamMonitor(monitor *model.UpstreamMonitor) error {
	return syncNewAPIUpstreamMonitorWithClient(monitor, upstreamMonitorHTTPClient())
}

func syncNewAPIUpstreamMonitorWithClient(monitor *model.UpstreamMonitor, client *http.Client) error {
	return syncNewAPIUpstreamMonitorWithContext(context.Background(), monitor, client)
}

func syncNewAPIUpstreamMonitorWithContext(ctx context.Context, monitor *model.UpstreamMonitor, client *http.Client) error {
	if err := validateUpstreamMonitorInput(monitor.Provider, monitor.NewAPIUserID, monitor.AccessToken); err != nil {
		return err
	}
	if client == nil {
		return errors.New("upstream monitor HTTP client is required")
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+monitor.AccessToken)
	headers.Set("New-Api-User", strconv.Itoa(monitor.NewAPIUserID))

	type result struct {
		name string
		body []byte
		err  error
	}
	results := make(chan result, 3)
	var wg sync.WaitGroup
	for _, endpoint := range []struct {
		name string
		path string
	}{
		{name: "status", path: "/api/status"},
		{name: "self", path: "/api/user/self"},
		{name: "groups", path: "/api/user/self/groups"},
	} {
		wg.Add(1)
		go func(endpointName string, endpointPath string) {
			defer wg.Done()
			body, fetchErr := fetchUpstreamMonitorJSON(ctx, client, http.MethodGet, monitor.BaseURL+endpointPath, headers, nil)
			results <- result{name: endpointName, body: body, err: fetchErr}
		}(endpoint.name, endpoint.path)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	bodies := make(map[string][]byte, 3)
	for result := range results {
		if result.err != nil {
			return fmt.Errorf("fetch upstream %s: %w", result.name, result.err)
		}
		bodies[result.name] = result.body
	}

	quotaPerUnit, err := parseNewAPIQuotaPerUnit(bodies["status"])
	if err != nil {
		return err
	}
	quota, err := parseNewAPIQuota(bodies["self"])
	if err != nil {
		return err
	}
	monitor.BalanceUSD = quota / quotaPerUnit
	monitor.BalanceAvailable = true
	monitor.GroupCount, monitor.GroupsJSON, err = parseNewAPIGroups(bodies["groups"])
	if err != nil {
		return err
	}
	monitor.PricingCount = 0
	monitor.PricingJSON = ""
	return nil
}

func syncSub2APIUpstreamMonitor(monitor *model.UpstreamMonitor) error {
	return syncSub2APIUpstreamMonitorWithClient(monitor, upstreamMonitorHTTPClient())
}

func syncSub2APIUpstreamMonitorWithClient(monitor *model.UpstreamMonitor, client *http.Client) error {
	return syncSub2APIUpstreamMonitorWithContext(context.Background(), monitor, client)
}

func syncSub2APIUpstreamMonitorWithContext(ctx context.Context, monitor *model.UpstreamMonitor, client *http.Client) error {
	if err := validateUpstreamMonitorInput(monitor.Provider, monitor.NewAPIUserID, monitor.AccessToken); err != nil {
		return err
	}
	if client == nil {
		return errors.New("upstream monitor HTTP client is required")
	}

	err := syncSub2APIUpstreamMonitorWithAccessToken(ctx, monitor, client, monitor.AccessToken)
	if !isUpstreamMonitorUnauthorized(err) {
		return err
	}
	if strings.TrimSpace(monitor.RefreshToken) == "" {
		return errors.New("Sub2API refresh token is not configured")
	}
	if err := refreshSub2APIUpstreamMonitorAccessToken(ctx, monitor, client); err != nil {
		return err
	}
	return syncSub2APIUpstreamMonitorWithAccessToken(ctx, monitor, client, monitor.AccessToken)
}

func syncSub2APIUpstreamMonitorWithAccessToken(ctx context.Context, monitor *model.UpstreamMonitor, client *http.Client, accessToken string) error {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+accessToken)

	selfBody, err := fetchUpstreamMonitorJSON(ctx, client, http.MethodGet, monitor.BaseURL+"/api/v1/auth/me", headers, nil)
	if err != nil {
		return fmt.Errorf("fetch upstream account: %w", err)
	}
	balance, err := parseSub2APIBalance(selfBody)
	if err != nil {
		return err
	}

	groupsBody, err := fetchUpstreamMonitorJSON(ctx, client, http.MethodGet, monitor.BaseURL+"/api/v1/groups/available", headers, nil)
	if err != nil {
		return fmt.Errorf("fetch upstream groups: %w", err)
	}
	ratesBody, err := fetchUpstreamMonitorJSON(ctx, client, http.MethodGet, monitor.BaseURL+"/api/v1/groups/rates", headers, nil)
	if err != nil {
		return fmt.Errorf("fetch upstream group rates: %w", err)
	}
	groupCount, groupsSnapshot, err := parseSub2APIGroups(groupsBody, ratesBody)
	if err != nil {
		return err
	}

	monitor.BalanceUSD = balance
	monitor.BalanceAvailable = true
	monitor.GroupCount = groupCount
	monitor.GroupsJSON = groupsSnapshot
	monitor.PricingCount = 0
	monitor.PricingJSON = ""
	return nil
}

func refreshSub2APIUpstreamMonitorAccessToken(ctx context.Context, monitor *model.UpstreamMonitor, client *http.Client) error {
	payload, err := common.Marshal(struct {
		RefreshToken string `json:"refresh_token"`
	}{
		RefreshToken: monitor.RefreshToken,
	})
	if err != nil {
		return fmt.Errorf("encode Sub2API refresh request: %w", err)
	}
	body, err := fetchUpstreamMonitorJSON(
		ctx,
		client,
		http.MethodPost,
		monitor.BaseURL+"/api/v1/auth/refresh",
		nil,
		payload,
	)
	if err != nil {
		return fmt.Errorf("refresh upstream access token: %w", err)
	}
	response := struct {
		Code int `json:"code"`
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}{}
	if err := common.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("decode upstream refresh response: %w", err)
	}
	if response.Code != 0 || strings.TrimSpace(response.Data.AccessToken) == "" {
		return errors.New("upstream refresh response did not return an access token")
	}
	monitor.AccessToken = strings.TrimSpace(response.Data.AccessToken)
	if strings.TrimSpace(response.Data.RefreshToken) != "" {
		monitor.RefreshToken = strings.TrimSpace(response.Data.RefreshToken)
	}
	return nil
}

func upstreamMonitorDetail(monitor *model.UpstreamMonitor, includeSnapshots bool) (*UpstreamMonitorDetail, error) {
	if monitor == nil {
		return nil, errors.New("upstream monitor is required")
	}
	detail := &UpstreamMonitorDetail{
		Id:                     monitor.Id,
		Name:                   monitor.Name,
		BaseURL:                monitor.BaseURL,
		Provider:               monitor.Provider,
		NewAPIUserID:           monitor.NewAPIUserID,
		AccessTokenConfigured:  strings.TrimSpace(monitor.AccessToken) != "",
		RefreshTokenConfigured: strings.TrimSpace(monitor.RefreshToken) != "",
		BalanceUSD:             monitor.BalanceUSD,
		BalanceAvailable:       monitor.BalanceAvailable,
		GroupCount:             monitor.GroupCount,
		PricingCount:           monitor.PricingCount,
		LastSyncedAt:           monitor.LastSyncedAt,
		LastError:              monitor.LastError,
		CreatedAt:              monitor.CreatedAt,
		UpdatedAt:              monitor.UpdatedAt,
	}
	if includeSnapshots && monitor.GroupsJSON != "" {
		if err := common.UnmarshalJsonStr(monitor.GroupsJSON, &detail.Groups); err != nil {
			return nil, fmt.Errorf("decode stored group snapshot: %w", err)
		}
	}
	if includeSnapshots && monitor.PricingJSON != "" {
		if err := common.UnmarshalJsonStr(monitor.PricingJSON, &detail.Pricing); err != nil {
			return nil, fmt.Errorf("decode stored pricing snapshot: %w", err)
		}
	}
	return detail, nil
}

func validateUpstreamMonitorInput(provider string, newAPIUserID int, accessToken string) error {
	if strings.TrimSpace(accessToken) == "" {
		return errors.New("upstream access token is required")
	}
	switch provider {
	case UpstreamMonitorProviderNewAPI:
		if newAPIUserID <= 0 {
			return errors.New("New API user ID is required")
		}
	case UpstreamMonitorProviderSub2API:
		return nil
	default:
		return errors.New("upstream provider must be newapi or sub2api")
	}
	return nil
}

func validateUpstreamMonitorCreateInput(input UpstreamMonitorCreateInput) error {
	if err := validateUpstreamMonitorInput(input.Provider, input.NewAPIUserID, input.AccessToken); err != nil {
		return err
	}
	if input.Provider == UpstreamMonitorProviderSub2API && strings.TrimSpace(input.RefreshToken) == "" {
		return errors.New("Sub2API refresh token is required")
	}
	return nil
}

func isUpstreamMonitorUnauthorized(err error) bool {
	if err == nil {
		return false
	}
	var upstreamErr *upstreamMonitorHTTPError
	return errors.As(err, &upstreamErr) && upstreamErr.StatusCode == http.StatusUnauthorized
}

func upstreamMonitorHTTPClient() *http.Client {
	baseClient := GetSSRFProtectedHTTPClient()
	if baseClient == nil {
		baseClient = newProtectedFetchHTTPClient()
	}
	client := *baseClient
	client.Timeout = upstreamMonitorHTTPTimeout
	return &client
}

func fetchUpstreamMonitorJSON(
	ctx context.Context,
	client *http.Client,
	method string,
	targetURL string,
	headers http.Header,
	body []byte,
) ([]byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, upstreamMonitorHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if headers != nil {
		for key, values := range headers {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
		return nil, &upstreamMonitorHTTPError{StatusCode: resp.StatusCode}
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, upstreamMonitorMaxBodySize+1))
	if err != nil {
		return nil, err
	}
	if len(responseBody) > upstreamMonitorMaxBodySize {
		return nil, errors.New("upstream response exceeds the maximum size")
	}
	return responseBody, nil
}

func parseNewAPIQuotaPerUnit(body []byte) (float64, error) {
	response := struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			QuotaPerUnit any `json:"quota_per_unit"`
		} `json:"data"`
	}{}
	if err := common.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("decode upstream status: %w", err)
	}
	quotaPerUnit, ok := upstreamMonitorNumber(response.Data.QuotaPerUnit)
	if !response.Success || !ok || quotaPerUnit <= 0 {
		return 0, errors.New("upstream status did not return a valid quota_per_unit")
	}
	return quotaPerUnit, nil
}

func parseNewAPIQuota(body []byte) (float64, error) {
	response := struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Quota any `json:"quota"`
		} `json:"data"`
	}{}
	if err := common.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("decode upstream account: %w", err)
	}
	quota, ok := upstreamMonitorNumber(response.Data.Quota)
	if !response.Success || !ok || quota < 0 {
		return 0, errors.New("upstream account did not return a valid quota")
	}
	return quota, nil
}

func parseNewAPIGroups(body []byte) (int, string, error) {
	response := struct {
		Success bool `json:"success"`
		Data    map[string]struct {
			Ratio any    `json:"ratio"`
			Desc  string `json:"desc"`
		} `json:"data"`
	}{}
	if err := common.Unmarshal(body, &response); err != nil {
		return 0, "", fmt.Errorf("decode upstream groups: %w", err)
	}
	if !response.Success || response.Data == nil {
		return 0, "", errors.New("upstream group response is invalid")
	}

	groupIDs := make([]string, 0, len(response.Data))
	for groupID := range response.Data {
		groupIDs = append(groupIDs, groupID)
	}
	sort.Strings(groupIDs)
	snapshot := upstreamMonitorGroupSnapshot{
		Groups: make([]upstreamMonitorGroup, 0, len(groupIDs)),
	}
	for _, groupID := range groupIDs {
		group := response.Data[groupID]
		snapshot.Groups = append(snapshot.Groups, upstreamMonitorGroup{
			ID:          groupID,
			Name:        groupID,
			Description: strings.TrimSpace(group.Desc),
			Multiplier:  normalizeUpstreamMonitorMultiplier(group.Ratio),
		})
	}
	encodedSnapshot, err := common.Marshal(snapshot)
	if err != nil {
		return 0, "", fmt.Errorf("encode upstream groups: %w", err)
	}
	return len(snapshot.Groups), string(encodedSnapshot), nil
}

func parseSub2APIBalance(body []byte) (float64, error) {
	response := struct {
		Code int `json:"code"`
		Data struct {
			Balance any `json:"balance"`
		} `json:"data"`
	}{}
	if err := common.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("decode upstream account: %w", err)
	}
	balance, ok := upstreamMonitorNumber(response.Data.Balance)
	if response.Code != 0 || !ok || balance < 0 {
		return 0, errors.New("upstream account did not return a valid balance")
	}
	return balance, nil
}

func parseSub2APIGroups(groupsBody []byte, ratesBody []byte) (int, string, error) {
	groupsResponse := struct {
		Code int `json:"code"`
		Data []struct {
			ID             any    `json:"id"`
			Name           string `json:"name"`
			Description    string `json:"description"`
			Desc           string `json:"desc"`
			RateMultiplier any    `json:"rate_multiplier"`
		} `json:"data"`
	}{}
	if err := common.Unmarshal(groupsBody, &groupsResponse); err != nil {
		return 0, "", fmt.Errorf("decode upstream groups: %w", err)
	}
	if groupsResponse.Code != 0 || groupsResponse.Data == nil {
		return 0, "", errors.New("upstream group response is invalid")
	}
	ratesResponse := struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}{}
	if err := common.Unmarshal(ratesBody, &ratesResponse); err != nil {
		return 0, "", fmt.Errorf("decode upstream group rates: %w", err)
	}
	if ratesResponse.Code != 0 || ratesResponse.Data == nil {
		return 0, "", errors.New("upstream group rates response is invalid")
	}
	snapshot := upstreamMonitorGroupSnapshot{
		Groups: make([]upstreamMonitorGroup, 0, len(groupsResponse.Data)),
	}
	for _, group := range groupsResponse.Data {
		groupID, ok := upstreamMonitorGroupID(group.ID)
		if !ok {
			return 0, "", errors.New("upstream group response contains an invalid group ID")
		}
		multiplier := group.RateMultiplier
		if rate, exists := ratesResponse.Data[groupID]; exists {
			multiplier = rate
		}
		description := strings.TrimSpace(group.Description)
		if description == "" {
			description = strings.TrimSpace(group.Desc)
		}
		name := strings.TrimSpace(group.Name)
		if name == "" {
			name = groupID
		}
		snapshot.Groups = append(snapshot.Groups, upstreamMonitorGroup{
			ID:          groupID,
			Name:        name,
			Description: description,
			Multiplier:  normalizeUpstreamMonitorMultiplier(multiplier),
		})
	}
	encodedSnapshot, err := common.Marshal(snapshot)
	if err != nil {
		return 0, "", err
	}
	return len(snapshot.Groups), string(encodedSnapshot), nil
}

func upstreamMonitorGroupID(value any) (string, bool) {
	switch id := value.(type) {
	case string:
		id = strings.TrimSpace(id)
		return id, id != ""
	case float64:
		if math.IsNaN(id) || math.IsInf(id, 0) {
			return "", false
		}
		return strconv.FormatFloat(id, 'f', -1, 64), true
	default:
		return "", false
	}
}

func normalizeUpstreamMonitorMultiplier(value any) any {
	if multiplier, ok := upstreamMonitorNumber(value); ok {
		return multiplier
	}
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if text != "" {
			return text
		}
	}
	return nil
}

func upstreamMonitorNumber(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}

func upstreamMonitorErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
