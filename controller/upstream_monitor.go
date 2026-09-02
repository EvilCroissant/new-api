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

package controller

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type upstreamMonitorDetectRequest struct {
	BaseURL string `json:"base_url" binding:"required"`
}

type upstreamMonitorCreateRequest struct {
	Name         string `json:"name"`
	BaseURL      string `json:"base_url" binding:"required"`
	Provider     string `json:"provider" binding:"required"`
	NewAPIUserID int    `json:"new_api_user_id"`
	AccessToken  string `json:"access_token" binding:"required"`
	RefreshToken string `json:"refresh_token"`
}

type upstreamMonitorUpdateRequest struct {
	NewAPIUserID *int    `json:"new_api_user_id"`
	AccessToken  *string `json:"access_token"`
	RefreshToken *string `json:"refresh_token"`
}

func ListUpstreamMonitors(c *gin.Context) {
	monitors, err := service.ListUpstreamMonitorDetails()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, monitors)
}

func GetUpstreamMonitor(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid upstream monitor ID"))
		return
	}
	monitor, err := service.GetUpstreamMonitorDetail(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, monitor)
}

func DetectUpstreamMonitor(c *gin.Context) {
	request := upstreamMonitorDetectRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := service.DetectUpstreamMonitor(c.Request.Context(), request.BaseURL)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func CreateUpstreamMonitor(c *gin.Context) {
	request := upstreamMonitorCreateRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	monitor, err := service.CreateUpstreamMonitor(service.UpstreamMonitorCreateInput{
		Name:         request.Name,
		BaseURL:      request.BaseURL,
		Provider:     request.Provider,
		NewAPIUserID: request.NewAPIUserID,
		AccessToken:  request.AccessToken,
		RefreshToken: request.RefreshToken,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_monitor.create", map[string]interface{}{
		"id":       monitor.Id,
		"provider": monitor.Provider,
		"base_url": monitor.BaseURL,
	})
	common.ApiSuccess(c, monitor)
}

func UpdateUpstreamMonitor(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid upstream monitor ID"))
		return
	}
	request := upstreamMonitorUpdateRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	monitor, err := service.UpdateUpstreamMonitorCredentials(c.Request.Context(), id, service.UpstreamMonitorUpdateInput{
		NewAPIUserID: request.NewAPIUserID,
		AccessToken:  request.AccessToken,
		RefreshToken: request.RefreshToken,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_monitor.update_credentials", map[string]interface{}{
		"id":                    id,
		"new_api_user_changed":  request.NewAPIUserID != nil,
		"access_token_changed":  request.AccessToken != nil,
		"refresh_token_changed": request.RefreshToken != nil,
	})
	common.ApiSuccess(c, monitor)
}

func SyncUpstreamMonitor(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid upstream monitor ID"))
		return
	}
	monitor, err := service.SyncUpstreamMonitorByIDWithContext(c.Request.Context(), id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_monitor.sync", map[string]interface{}{
		"id": id,
	})
	common.ApiSuccess(c, monitor)
}

func DeleteUpstreamMonitor(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid upstream monitor ID"))
		return
	}
	if err := service.DeleteUpstreamMonitor(id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_monitor.delete", map[string]interface{}{
		"id": id,
	})
	common.ApiSuccess(c, nil)
}
