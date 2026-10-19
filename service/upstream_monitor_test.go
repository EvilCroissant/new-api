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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
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

func TestValidateUpstreamMonitorCreateInputSupportsSub2APICredentials(t *testing.T) {
	require.NoError(t, validateUpstreamMonitorCreateInput(UpstreamMonitorCreateInput{Provider: UpstreamMonitorProviderSub2API}))
	err := validateUpstreamMonitorCreateInput(UpstreamMonitorCreateInput{
		Provider: UpstreamMonitorProviderSub2API,
		APIKey:   "api-key",
	})
	require.NoError(t, err)

	err = validateUpstreamMonitorCreateInput(UpstreamMonitorCreateInput{
		Provider:    UpstreamMonitorProviderSub2API,
		AccessToken: "jwt",
	})
	require.NoError(t, err)

	err = validateUpstreamMonitorCreateInput(UpstreamMonitorCreateInput{
		Provider:     UpstreamMonitorProviderSub2API,
		AccessToken:  "jwt",
		RefreshToken: "refresh-token",
	})
	require.NoError(t, err)
}

// This fixture is the monitor schema in release v1.0.0-rc.42.10, before APIKey.
type upstreamMonitorBeforeAPIKey struct {
	Id               int
	Name             string `gorm:"type:varchar(100);not null"`
	BaseURL          string `gorm:"type:varchar(500);uniqueIndex;not null"`
	Provider         string `gorm:"type:varchar(16);not null"`
	NewAPIUserID     int
	AccessToken      string `gorm:"type:text"`
	RefreshToken     string `gorm:"type:text"`
	BalanceUSD       float64
	BalanceAvailable bool
	GroupCount       int
	PricingCount     int
	GroupsJSON       string `gorm:"type:text"`
	PricingJSON      string `gorm:"type:text"`
	LastSyncedAt     int64  `gorm:"bigint;index"`
	LastError        string `gorm:"type:text"`
	CreatedAt        int64  `gorm:"autoCreateTime"`
	UpdatedAt        int64  `gorm:"autoUpdateTime"`
}

func TestUpstreamMonitorDatabaseMigrationAndKeySelection(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "monitors.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			for _, upgrade := range []bool{false, true} {
				t.Run(fmt.Sprintf("upgrade_%t", upgrade), func(t *testing.T) {
					prefix := fmt.Sprintf("monitor_regression_%t_", upgrade)
					db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: prefix}})
					require.NoError(t, err)
					sqlDB, err := db.DB()
					require.NoError(t, err)
					previousDB := model.DB
					model.DB = db
					t.Cleanup(func() {
						model.DB = previousDB
						require.NoError(t, db.Migrator().DropTable(&model.UpstreamMonitor{}, &model.Channel{}))
						require.NoError(t, sqlDB.Close())
					})
					var version string
					versionSQL := "SELECT version()"
					if dialect == "sqlite" {
						versionSQL = "SELECT sqlite_version()"
					}
					require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
					t.Logf("%s: %s", dialect, version)
					if upgrade {
						oldTable := db.Table(prefix + "upstream_monitors")
						require.NoError(t, oldTable.AutoMigrate(&upstreamMonitorBeforeAPIKey{}))
						legacy := &upstreamMonitorBeforeAPIKey{Name: "Legacy monitor", BaseURL: "https://legacy.example.com", Provider: UpstreamMonitorProviderSub2API, AccessToken: "jwt", RefreshToken: "refresh", BalanceUSD: 42, BalanceAvailable: true, GroupsJSON: `{"groups":[]}`, LastSyncedAt: 100}
						require.NoError(t, oldTable.Create(legacy).Error)
					}
					for range 2 {
						require.NoError(t, db.AutoMigrate(&model.UpstreamMonitor{}))
					}
					assert.True(t, db.Migrator().HasIndex(&model.UpstreamMonitor{}, "BaseURL"))
					assert.True(t, db.Migrator().HasIndex(&model.UpstreamMonitor{}, "LastSyncedAt"))
					var monitor *model.UpstreamMonitor
					if upgrade {
						monitor, err = model.GetUpstreamMonitorByBaseURL("https://legacy.example.com")
						require.NoError(t, err)
						assert.Equal(t, 42.0, monitor.BalanceUSD)
						assert.Equal(t, `{"groups":[]}`, monitor.GroupsJSON)
						assert.Equal(t, int64(100), monitor.LastSyncedAt)
						var nullKeys int64
						require.NoError(t, db.Model(&model.UpstreamMonitor{}).Where("id = ? AND api_key IS NULL", monitor.Id).Count(&nullKeys).Error)
						assert.Equal(t, int64(1), nullKeys)
					} else {
						monitor = &model.UpstreamMonitor{Name: "Fresh monitor", BaseURL: "https://fresh.example.com", Provider: UpstreamMonitorProviderSub2API, AccessToken: "jwt", RefreshToken: "refresh"}
						require.NoError(t, model.CreateUpstreamMonitor(monitor))
					}
					assert.Error(t, model.CreateUpstreamMonitor(&model.UpstreamMonitor{Name: "Duplicate", BaseURL: monitor.BaseURL, Provider: monitor.Provider}))
					monitor.BalanceUSD, monitor.BalanceAvailable, monitor.LastSyncedAt = 18, true, 200
					require.NoError(t, model.SaveUpstreamMonitorSyncResult(monitor, 0, "jwt", "refresh", ""))
					key := "configured-key"
					require.NoError(t, model.UpdateUpstreamMonitorCredentials(monitor.Id, model.UpstreamMonitorCredentialUpdate{APIKey: &key}))
					monitor.AccessToken, monitor.RefreshToken, monitor.BalanceUSD = "stale-jwt", "stale-refresh", 99
					require.ErrorIs(t, model.SaveUpstreamMonitorSyncResult(monitor, 0, "jwt", "refresh", ""), model.ErrUpstreamMonitorCredentialsChanged)
					monitor, err = model.GetUpstreamMonitorByID(monitor.Id)
					require.NoError(t, err)
					assert.Equal(t, key, monitor.APIKey)
					assert.Equal(t, "jwt", monitor.AccessToken)
					assert.Equal(t, "refresh", monitor.RefreshToken)
					assert.Equal(t, 18.0, monitor.BalanceUSD)
					detail, err := upstreamMonitorDetail(monitor, true)
					require.NoError(t, err)
					assert.True(t, detail.APIKeyConfigured)
					encoded, err := common.Marshal(detail)
					require.NoError(t, err)
					for _, secret := range []string{"configured-key", "jwt", "refresh\""} {
						assert.NotContains(t, string(encoded), secret)
					}
					monitor.AccessToken, monitor.RefreshToken = "rotated-jwt", "rotated-refresh"
					require.NoError(t, model.SaveUpstreamMonitorSyncResult(monitor, 0, "jwt", "refresh", key))
					key = ""
					require.NoError(t, model.UpdateUpstreamMonitorCredentials(monitor.Id, model.UpstreamMonitorCredentialUpdate{APIKey: &key}))
					monitor, err = model.GetUpstreamMonitorByID(monitor.Id)
					require.NoError(t, err)
					assert.Empty(t, monitor.APIKey)
					assert.Equal(t, "rotated-jwt", monitor.AccessToken)
					assert.Equal(t, "rotated-refresh", monitor.RefreshToken)
					require.NoError(t, db.AutoMigrate(&model.Channel{}))
					t.Run("automatic_selection", func(t *testing.T) { testUpstreamMonitorChannelKeys(t, db) })
				})
			}
		})
	}
}

func testUpstreamMonitorChannelKeys(t *testing.T, db *gorm.DB) {
	baseURL, otherURL, normalizedURL := "https://monitor.example.com/v1/", "https://other.example.com", "https://monitor.example.com"
	channels := []*model.Channel{
		{Key: "first", BaseURL: &baseURL, Status: common.ChannelStatusEnabled},
		{Key: "disabled-channel", BaseURL: &baseURL, Status: common.ChannelStatusManuallyDisabled},
		{Key: "other-site", BaseURL: &otherURL, Status: common.ChannelStatusEnabled},
		{Key: "disabled-key\nsecond\nfirst", BaseURL: &normalizedURL, Status: common.ChannelStatusEnabled, ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled}, MultiKeyPollingIndex: 2}},
	}
	for _, channel := range channels {
		require.NoError(t, db.Create(channel).Error)
	}
	monitor := &model.UpstreamMonitor{BaseURL: normalizedURL, Provider: UpstreamMonitorProviderSub2API}
	key, err := selectUpstreamMonitorAPIKey(context.Background(), monitor)
	require.NoError(t, err)
	assert.Contains(t, []string{"first", "second"}, key)
	assert.Empty(t, monitor.APIKey)
	var multi model.Channel
	require.NoError(t, db.First(&multi, channels[3].Id).Error)
	assert.Equal(t, 2, multi.ChannelInfo.MultiKeyPollingIndex)
	require.NoError(t, db.Model(channels[0]).Update("status", common.ChannelStatusManuallyDisabled).Error)
	multi.Key = `["disabled-key","second"]`
	require.NoError(t, db.Model(&multi).Update("key", multi.Key).Error)
	key, err = selectUpstreamMonitorAPIKey(context.Background(), monitor)
	require.NoError(t, err)
	assert.Equal(t, "second", key)
	monitor.APIKey = " explicit-key "
	key, err = selectUpstreamMonitorAPIKey(context.Background(), monitor)
	require.NoError(t, err)
	assert.Equal(t, "explicit-key", key)
	monitor.APIKey = ""
	monitor.BaseURL = "https://unmatched.example.com"
	key, err = selectUpstreamMonitorAPIKey(context.Background(), monitor)
	require.NoError(t, err)
	assert.Empty(t, key)
}

func TestSyncSub2APIAPIKeyReadsOnlyWalletBalance(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		want      float64
		wantError bool
	}{
		{name: "wallet", body: `{"mode":"unrestricted","isValid":true,"unit":"USD","balance":12.5,"remaining":99}`, want: 12.5},
		{name: "zero wallet", body: `{"mode":"unrestricted","isValid":true,"unit":"USD","balance":0}`},
		{name: "negative wallet", body: `{"mode":"unrestricted","isValid":true,"unit":"USD","balance":-1.25}`, want: -1.25},
		{name: "key quota", body: `{"mode":"quota_limited","isValid":true,"remaining":10,"quota":{"remaining":10}}`, wantError: true},
		{name: "subscription", body: `{"mode":"unrestricted","isValid":true,"remaining":20,"subscription":{}}`, wantError: true},
		{name: "unlimited", body: `{"mode":"unrestricted","isValid":true,"remaining":-1}`, wantError: true},
		{name: "rate limits only", body: `{"mode":"quota_limited","isValid":true,"rate_limits":{}}`, wantError: true},
		{name: "null balance", body: `{"mode":"unrestricted","isValid":true,"balance":null}`, wantError: true},
		{name: "wrong unit", body: `{"mode":"unrestricted","isValid":true,"unit":"CNY","balance":12}`, wantError: true},
		{name: "invalid key", body: `{"mode":"unrestricted","isValid":false,"balance":12}`, wantError: true},
		{name: "malformed", body: `{`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1/usage", r.URL.Path)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "Bearer api-key", r.Header.Get("Authorization"))
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			monitor := &model.UpstreamMonitor{Provider: UpstreamMonitorProviderSub2API, BaseURL: server.URL, APIKey: "api-key", BalanceUSD: 42, BalanceAvailable: true, GroupCount: 1, GroupsJSON: `{"groups":[{}]}`}
			err := syncSub2APIUpstreamMonitorWithClient(monitor, server.Client())
			if tc.wantError {
				require.Error(t, err)
				assert.False(t, monitor.BalanceAvailable)
				assert.NotContains(t, err.Error(), "api-key")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, monitor.BalanceUSD)
			assert.True(t, monitor.BalanceAvailable)
			assert.Zero(t, monitor.GroupCount)
			assert.Empty(t, monitor.GroupsJSON)
		})
	}
}

func TestSyncSub2APIAPIKeyDoesNotRefreshOnUnauthorized(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "/v1/usage", r.URL.Path)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	monitor := &model.UpstreamMonitor{Provider: UpstreamMonitorProviderSub2API, BaseURL: server.URL, APIKey: "secret-key"}
	err := syncSub2APIUpstreamMonitorWithClient(monitor, server.Client())
	require.Error(t, err)
	assert.Equal(t, 1, calls)
	assert.False(t, monitor.BalanceAvailable)
	assert.NotContains(t, err.Error(), "secret-key")
}

func TestSyncSub2APIUpstreamMonitorPrefersKeyAndFallsBackToJWT(t *testing.T) {
	for _, tc := range []struct {
		name       string
		key        string
		status     int
		body       string
		jwt        string
		refresh    string
		wantWallet float64
		wantPaths  []string
	}{
		{name: "key succeeds", key: "key", status: 200, body: `{"mode":"unrestricted","isValid":true,"unit":"USD","balance":9}`, jwt: "jwt", refresh: "refresh", wantWallet: 9, wantPaths: []string{"/v1/usage"}},
		{name: "no key", jwt: "jwt", wantWallet: 12.5, wantPaths: []string{"/api/v1/auth/me", "/api/v1/groups/available", "/api/v1/groups/rates"}},
		{name: "expired key", key: "key", status: 401, jwt: "jwt", wantWallet: 12.5, wantPaths: []string{"/v1/usage", "/api/v1/auth/me", "/api/v1/groups/available", "/api/v1/groups/rates"}},
		{name: "forbidden key", key: "key", status: 403, jwt: "jwt", wantWallet: 12.5, wantPaths: []string{"/v1/usage", "/api/v1/auth/me", "/api/v1/groups/available", "/api/v1/groups/rates"}},
		{name: "key quota", key: "key", status: 200, body: `{"mode":"quota_limited","isValid":true,"remaining":99}`, jwt: "jwt", wantWallet: 12.5, wantPaths: []string{"/v1/usage", "/api/v1/auth/me", "/api/v1/groups/available", "/api/v1/groups/rates"}},
		{name: "expired key and jwt", key: "key", status: 401, jwt: "expired", refresh: "refresh", wantWallet: 12.5, wantPaths: []string{"/v1/usage", "/api/v1/auth/me", "/api/v1/auth/refresh", "/api/v1/auth/me", "/api/v1/groups/available", "/api/v1/groups/rates"}},
		{name: "refresh only fallback", key: "key", status: 401, refresh: "refresh", wantWallet: 12.5, wantPaths: []string{"/v1/usage", "/api/v1/auth/refresh", "/api/v1/auth/me", "/api/v1/groups/available", "/api/v1/groups/rates"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				switch r.URL.Path {
				case "/v1/usage":
					assert.Equal(t, "Bearer key", r.Header.Get("Authorization"))
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				case "/api/v1/auth/me":
					if r.Header.Get("Authorization") == "Bearer expired" {
						w.WriteHeader(401)
						return
					}
					assert.Equal(t, "Bearer jwt", r.Header.Get("Authorization"))
					_, _ = w.Write([]byte(`{"code":0,"data":{"balance":12.5}}`))
				case "/api/v1/auth/refresh":
					assert.Empty(t, r.Header.Get("Authorization"))
					_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"jwt","refresh_token":"rotated"}}`))
				case "/api/v1/groups/available":
					_, _ = w.Write([]byte(`{"code":0,"data":[]}`))
				case "/api/v1/groups/rates":
					_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
				default:
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(server.Close)
			monitor := &model.UpstreamMonitor{Provider: UpstreamMonitorProviderSub2API, BaseURL: server.URL, APIKey: tc.key, AccessToken: tc.jwt, RefreshToken: tc.refresh}
			require.NoError(t, syncSub2APIUpstreamMonitorWithClient(monitor, server.Client()))
			assert.Equal(t, tc.wantPaths, paths)
			assert.Equal(t, tc.wantWallet, monitor.BalanceUSD)
			assert.True(t, monitor.BalanceAvailable)
			assert.Equal(t, tc.key, monitor.APIKey)
		})
	}
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

func TestSyncSub2APIUpstreamMonitorKeepsWalletWhenGroupMetadataFails(t *testing.T) {
	for _, failurePath := range []string{"/api/v1/groups/available", "/api/v1/groups/rates"} {
		t.Run(failurePath, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == failurePath {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				switch r.URL.Path {
				case "/v1/usage":
					_, _ = w.Write([]byte(`{"mode":"unrestricted","isValid":true,"remaining":99}`))
				case "/api/v1/auth/me":
					_, _ = w.Write([]byte(`{"code":0,"data":{"balance":-1.25}}`))
				case "/api/v1/groups/available":
					_, _ = w.Write([]byte(`{"code":0,"data":[]}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			monitor := &model.UpstreamMonitor{Provider: UpstreamMonitorProviderSub2API, BaseURL: server.URL, APIKey: "key", AccessToken: "jwt", GroupCount: 1, GroupsJSON: `{"groups":[{}]}`}
			require.NoError(t, syncSub2APIUpstreamMonitorWithClient(monitor, server.Client()))
			assert.Equal(t, -1.25, monitor.BalanceUSD)
			assert.True(t, monitor.BalanceAvailable)
			assert.Zero(t, monitor.GroupCount)
			assert.Empty(t, monitor.GroupsJSON)
		})
	}
}

func TestSyncSub2APIUpstreamMonitorCancelledKeyRequestSkipsJWTFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		cancel()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(cancel)
	monitor := &model.UpstreamMonitor{Provider: UpstreamMonitorProviderSub2API, BaseURL: server.URL, APIKey: "key", AccessToken: "jwt", RefreshToken: "refresh"}
	require.ErrorIs(t, syncSub2APIUpstreamMonitorWithContext(ctx, monitor, server.Client()), context.Canceled)
	assert.Equal(t, []string{"/v1/usage"}, paths)
	assert.False(t, monitor.BalanceAvailable)
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
