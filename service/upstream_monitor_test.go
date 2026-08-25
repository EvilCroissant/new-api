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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeUpstreamMonitorURL(t *testing.T) {
	result, err := normalizeUpstreamMonitorURL("https://monitor.example.com/v1/", nil)
	require.NoError(t, err)
	assert.Equal(t, "https://monitor.example.com", result)
}

func TestNormalizeUpstreamMonitorURLRejectsCredentialsAndQuery(t *testing.T) {
	_, err := normalizeUpstreamMonitorURL("https://user:password@monitor.example.com", nil)
	require.Error(t, err)

	_, err = normalizeUpstreamMonitorURL("https://monitor.example.com?token=secret", nil)
	require.Error(t, err)
}

func TestDetectUpstreamMonitorWithClientRecognizesNewAPIStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/api/status", request.URL.Path)
		_, _ = writer.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
	}))
	t.Cleanup(server.Close)

	result, err := detectUpstreamMonitorWithClient(context.Background(), server.Client(), server.URL)
	require.NoError(t, err)
	assert.True(t, result.Detected)
	assert.Equal(t, UpstreamMonitorProviderNewAPI, result.Provider)
}

func TestDetectUpstreamMonitorWithClientRecognizesSub2APIHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/status":
			writer.WriteHeader(http.StatusNotFound)
		case "/health":
			_, _ = writer.Write([]byte(`{"status":"ok"}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	result, err := detectUpstreamMonitorWithClient(context.Background(), server.Client(), server.URL)
	require.NoError(t, err)
	assert.True(t, result.Detected)
	assert.Equal(t, UpstreamMonitorProviderSub2API, result.Provider)
}

func TestDetectUpstreamMonitorWithClientDoesNotTreatUnauthorizedAsSub2API(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	result, err := detectUpstreamMonitorWithClient(context.Background(), server.Client(), server.URL)
	require.NoError(t, err)
	assert.False(t, result.Detected)
	assert.Empty(t, result.Provider)
}

func TestValidateUpstreamMonitorInputRequiresCorrectCredentialsForProvider(t *testing.T) {
	err := validateUpstreamMonitorInput(UpstreamMonitorProviderNewAPI, 0, "pat")
	require.Error(t, err)

	err = validateUpstreamMonitorInput(UpstreamMonitorProviderSub2API, 0, "jwt")
	require.NoError(t, err)

	err = validateUpstreamMonitorInput(UpstreamMonitorProviderSub2API, 0, "")
	require.Error(t, err)
}

func TestValidateUpstreamMonitorCreateInputRequiresSub2APIRefreshToken(t *testing.T) {
	err := validateUpstreamMonitorCreateInput(UpstreamMonitorCreateInput{
		Provider:    UpstreamMonitorProviderSub2API,
		AccessToken: "jwt",
	})
	require.Error(t, err)

	err = validateUpstreamMonitorCreateInput(UpstreamMonitorCreateInput{
		Provider:     UpstreamMonitorProviderSub2API,
		AccessToken:  "jwt",
		RefreshToken: "refresh-token",
	})
	require.NoError(t, err)
}

func TestSyncSub2APIUpstreamMonitorRefreshesExpiredAccessToken(t *testing.T) {
	var refreshCalls int
	var accountCalls int
	var modelPlazaCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/auth/refresh":
			refreshCalls++
			require.Equal(t, http.MethodPost, request.Method)
			payload := struct {
				RefreshToken string `json:"refresh_token"`
			}{}
			require.NoError(t, common.DecodeJson(request.Body, &payload))
			assert.Equal(t, "refresh-old", payload.RefreshToken)
			_, _ = writer.Write([]byte(`{"code":0,"data":{"access_token":"access-new","refresh_token":"refresh-new","expires_in":900,"token_type":"Bearer"}}`))
		case "/api/v1/auth/me":
			accountCalls++
			if request.Header.Get("Authorization") == "Bearer access-old" {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			assert.Equal(t, "Bearer access-new", request.Header.Get("Authorization"))
			_, _ = writer.Write([]byte(`{"code":0,"data":{"balance":12.5}}`))
		case "/api/v1/groups/available":
			assert.Equal(t, "Bearer access-new", request.Header.Get("Authorization"))
			_, _ = writer.Write([]byte(`{"code":0,"data":[{"id":1,"name":"Standard"}]}`))
		case "/api/v1/groups/rates":
			assert.Equal(t, "Bearer access-new", request.Header.Get("Authorization"))
			_, _ = writer.Write([]byte(`{"code":0,"data":{"1":0.5}}`))
		case "/api/v1/model-plaza":
			modelPlazaCalls++
			writer.WriteHeader(http.StatusInternalServerError)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	monitor := &model.UpstreamMonitor{
		BaseURL:      server.URL,
		Provider:     UpstreamMonitorProviderSub2API,
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
	}
	err := syncSub2APIUpstreamMonitorWithClient(monitor, server.Client())
	require.NoError(t, err)
	assert.Equal(t, 1, refreshCalls)
	assert.Equal(t, 2, accountCalls)
	assert.Equal(t, "access-new", monitor.AccessToken)
	assert.Equal(t, "refresh-new", monitor.RefreshToken)
	assert.Equal(t, 12.5, monitor.BalanceUSD)
	assert.Equal(t, 1, monitor.GroupCount)
	assert.Zero(t, modelPlazaCalls)
	assert.Zero(t, monitor.PricingCount)
	assert.Empty(t, monitor.PricingJSON)
}

func TestSyncSub2APIUpstreamMonitorRetriesOnlyOnceAfterRefresh(t *testing.T) {
	var refreshCalls int
	var accountCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/auth/refresh":
			refreshCalls++
			_, _ = writer.Write([]byte(`{"code":0,"data":{"access_token":"access-new","refresh_token":"refresh-new"}}`))
		case "/api/v1/auth/me":
			accountCalls++
			writer.WriteHeader(http.StatusUnauthorized)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	monitor := &model.UpstreamMonitor{
		BaseURL:      server.URL,
		Provider:     UpstreamMonitorProviderSub2API,
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
	}
	err := syncSub2APIUpstreamMonitorWithClient(monitor, server.Client())
	require.Error(t, err)
	assert.Equal(t, 1, refreshCalls)
	assert.Equal(t, 2, accountCalls)
	assert.Equal(t, "access-new", monitor.AccessToken)
	assert.Equal(t, "refresh-new", monitor.RefreshToken)
}

func TestSyncSub2APIUpstreamMonitorReturnsRefreshFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/auth/me":
			writer.WriteHeader(http.StatusUnauthorized)
		case "/api/v1/auth/refresh":
			writer.WriteHeader(http.StatusUnauthorized)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	monitor := &model.UpstreamMonitor{
		BaseURL:      server.URL,
		Provider:     UpstreamMonitorProviderSub2API,
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
	}
	err := syncSub2APIUpstreamMonitorWithClient(monitor, server.Client())
	require.Error(t, err)
	assert.ErrorContains(t, err, "refresh upstream access token: upstream returned HTTP 401")
}

func TestParseSub2APIGroupsPreservesGroupsAndRates(t *testing.T) {
	count, snapshot, err := parseSub2APIGroups(
		[]byte(`{"code":0,"data":[{"id":1,"name":"Standard","description":"General use","rate_multiplier":0.1}]}`),
		[]byte(`{"code":0,"data":{"1":0.5}}`),
	)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.JSONEq(t, `{"groups":[{"id":"1","name":"Standard","description":"General use","multiplier":0.5}]}`, snapshot)
}

func TestParseNewAPIGroupsNormalizesDescriptionsAndMultipliers(t *testing.T) {
	count, snapshot, err := parseNewAPIGroups([]byte(`{
		"success": true,
		"data": {
			"gpt-plus": {"ratio": 0.2, "desc": "GPT Plus"},
			"auto": {"ratio": "自动", "desc": "Automatic routing"}
		}
	}`))
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.JSONEq(t, `{"groups":[{"id":"auto","name":"auto","description":"Automatic routing","multiplier":"自动"},{"id":"gpt-plus","name":"gpt-plus","description":"GPT Plus","multiplier":0.2}]}`, snapshot)
}

func TestSyncNewAPIUpstreamMonitorStoresGroupMultipliersWithoutPricingRequest(t *testing.T) {
	var pricingCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer personal-access-token", request.Header.Get("Authorization"))
		assert.Equal(t, "99", request.Header.Get("New-Api-User"))
		switch request.URL.Path {
		case "/api/status":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		case "/api/user/self":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"quota":1250000}}`))
		case "/api/user/self/groups":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"gpt-plus":{"ratio":0.2,"desc":"GPT Plus"}}}`))
		case "/api/pricing":
			pricingCalls++
			writer.WriteHeader(http.StatusInternalServerError)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	monitor := &model.UpstreamMonitor{
		BaseURL:      server.URL,
		Provider:     UpstreamMonitorProviderNewAPI,
		NewAPIUserID: 99,
		AccessToken:  "personal-access-token",
	}
	err := syncNewAPIUpstreamMonitorWithClient(monitor, server.Client())
	require.NoError(t, err)
	assert.Equal(t, 2.5, monitor.BalanceUSD)
	assert.Equal(t, 1, monitor.GroupCount)
	assert.JSONEq(t, `{"groups":[{"id":"gpt-plus","name":"gpt-plus","description":"GPT Plus","multiplier":0.2}]}`, monitor.GroupsJSON)
	assert.Zero(t, pricingCalls)
	assert.Zero(t, monitor.PricingCount)
	assert.Empty(t, monitor.PricingJSON)
}
