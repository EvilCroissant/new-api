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

package model

import (
	"errors"

	"gorm.io/gorm"
)

var ErrUpstreamMonitorCredentialsChanged = errors.New("upstream monitor credentials changed during synchronization")

// UpstreamMonitor stores an independently configured upstream account monitor.
// It deliberately has no relation to local channels or their API keys.
type UpstreamMonitor struct {
	Id               int     `json:"id"`
	Name             string  `json:"name" gorm:"type:varchar(100);not null"`
	BaseURL          string  `json:"base_url" gorm:"type:varchar(500);uniqueIndex;not null"`
	Provider         string  `json:"provider" gorm:"type:varchar(16);not null"`
	NewAPIUserID     int     `json:"new_api_user_id"`
	AccessToken      string  `json:"-" gorm:"type:text"`
	RefreshToken     string  `json:"-" gorm:"type:text"`
	BalanceUSD       float64 `json:"balance_usd"`
	BalanceAvailable bool    `json:"balance_available"`
	GroupCount       int     `json:"group_count"`
	PricingCount     int     `json:"pricing_count"`
	GroupsJSON       string  `json:"-" gorm:"type:text"`
	PricingJSON      string  `json:"-" gorm:"type:text"`
	LastSyncedAt     int64   `json:"last_synced_at" gorm:"bigint;index"`
	LastError        string  `json:"last_error" gorm:"type:text"`
	CreatedAt        int64   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        int64   `json:"updated_at" gorm:"autoUpdateTime"`
}

func ListUpstreamMonitors() ([]*UpstreamMonitor, error) {
	monitors := make([]*UpstreamMonitor, 0)
	err := DB.Order("id DESC").Find(&monitors).Error
	return monitors, err
}

func CountUpstreamMonitors() (int64, error) {
	var count int64
	err := DB.Model(&UpstreamMonitor{}).Count(&count).Error
	return count, err
}

func GetUpstreamMonitorByID(id int) (*UpstreamMonitor, error) {
	monitor := &UpstreamMonitor{}
	err := DB.First(monitor, id).Error
	return monitor, err
}

func GetUpstreamMonitorByBaseURL(baseURL string) (*UpstreamMonitor, error) {
	monitor := &UpstreamMonitor{}
	err := DB.Where("base_url = ?", baseURL).First(monitor).Error
	return monitor, err
}

func CreateUpstreamMonitor(monitor *UpstreamMonitor) error {
	return DB.Create(monitor).Error
}

func SaveUpstreamMonitor(monitor *UpstreamMonitor) error {
	return DB.Save(monitor).Error
}

type UpstreamMonitorCredentialUpdate struct {
	NewAPIUserID *int
	AccessToken  *string
	RefreshToken *string
}

func UpdateUpstreamMonitorCredentials(id int, update UpstreamMonitorCredentialUpdate) error {
	updates := map[string]any{}
	if update.NewAPIUserID != nil {
		updates["new_api_user_id"] = *update.NewAPIUserID
	}
	if update.AccessToken != nil {
		updates["access_token"] = *update.AccessToken
	}
	if update.RefreshToken != nil {
		updates["refresh_token"] = *update.RefreshToken
	}
	if len(updates) == 0 {
		return nil
	}
	return DB.Model(&UpstreamMonitor{}).Where("id = ?", id).Updates(updates).Error
}

func SaveUpstreamMonitorSyncResult(monitor *UpstreamMonitor, previousNewAPIUserID int, previousAccessToken string, previousRefreshToken string) error {
	result := DB.Model(&UpstreamMonitor{}).
		Where("id = ? AND new_api_user_id = ? AND access_token = ? AND refresh_token = ?", monitor.Id, previousNewAPIUserID, previousAccessToken, previousRefreshToken).
		Updates(map[string]any{
			"access_token":      monitor.AccessToken,
			"refresh_token":     monitor.RefreshToken,
			"balance_usd":       monitor.BalanceUSD,
			"balance_available": monitor.BalanceAvailable,
			"group_count":       monitor.GroupCount,
			"pricing_count":     monitor.PricingCount,
			"groups_json":       monitor.GroupsJSON,
			"pricing_json":      monitor.PricingJSON,
			"last_synced_at":    monitor.LastSyncedAt,
			"last_error":        monitor.LastError,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		current, err := GetUpstreamMonitorByID(monitor.Id)
		if err != nil {
			return err
		}
		if current.NewAPIUserID == previousNewAPIUserID && current.AccessToken == previousAccessToken && current.RefreshToken == previousRefreshToken {
			return nil
		}
		return ErrUpstreamMonitorCredentialsChanged
	}
	return nil
}

func DeleteUpstreamMonitor(id int) error {
	return DB.Delete(&UpstreamMonitor{}, id).Error
}

func IsUpstreamMonitorNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}
