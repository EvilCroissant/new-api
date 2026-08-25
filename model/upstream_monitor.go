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

import "gorm.io/gorm"

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

func DeleteUpstreamMonitor(id int) error {
	return DB.Delete(&UpstreamMonitor{}, id).Error
}

func IsUpstreamMonitorNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}
