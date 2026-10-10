package controller

import (
	"errors"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type channelProfitConfigRequest struct {
	CostFactor          *float64 `json:"cost_factor"`
	RequestCostUSD      *float64 `json:"request_cost_usd"`
	ClearRequestCost    bool     `json:"clear_request_cost"`
	Enabled             *bool    `json:"enabled"`
	DisplayName         *string  `json:"display_name"`
	SyncIntervalMinutes *int     `json:"sync_interval_minutes"`
	AccessToken         *string  `json:"access_token"`
}

func GetChannelProfit(c *gin.Context) {
	usageDate := c.Query("date")
	if usageDate == "" {
		usageDate = time.Now().In(time.Local).Format("2006-01-02")
	}
	summary, err := service.GetChannelProfitSummary(
		usageDate,
		c.GetInt("role") >= common.RoleRootUser,
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, summary)
}

func UpdateChannelProfitConfig(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelId <= 0 {
		common.ApiError(c, errors.New("invalid channel ID"))
		return
	}
	request := channelProfitConfigRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.Enabled == nil && request.DisplayName == nil && request.SyncIntervalMinutes == nil && request.AccessToken == nil && request.CostFactor == nil && request.RequestCostUSD == nil && !request.ClearRequestCost {
		common.ApiError(c, errors.New("at least one configuration field is required"))
		return
	}
	config, err := service.UpdateChannelProfitConfig(channelId, service.ChannelProfitConfigUpdate{
		CostFactor: request.CostFactor, RequestCostUSD: request.RequestCostUSD, ClearRequestCost: request.ClearRequestCost,
		Enabled:             request.Enabled,
		DisplayName:         request.DisplayName,
		SyncIntervalMinutes: request.SyncIntervalMinutes,
		AccessToken:         request.AccessToken,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	action := "channel_profit.update"
	if request.Enabled != nil {
		if *request.Enabled {
			action = "channel_profit.enable"
		} else {
			action = "channel_profit.disable"
		}
	}
	recordManageAudit(c, action, map[string]interface{}{
		"id":                   channelId,
		"display_name_changed": request.DisplayName != nil,
		"interval_changed":     request.SyncIntervalMinutes != nil,
		"access_token_changed": request.AccessToken != nil,
	})
	common.ApiSuccess(c, config)
}

func SyncChannelProfitGroup(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelId <= 0 {
		common.ApiError(c, errors.New("invalid channel ID"))
		return
	}
	task, created, err := service.StartChannelProfitGroupSync(channelId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "channel_profit.sync_group", map[string]interface{}{
		"id":      channelId,
		"taskId":  task.TaskID,
		"created": created,
	})
	common.ApiSuccess(c, gin.H{
		"created": created,
		"task":    task.ToResponse(),
	})
}

func SyncChannelProfit(c *gin.Context) {
	task, created, err := service.StartChannelProfitSync()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "channel_profit.sync", map[string]interface{}{
		"taskId":  task.TaskID,
		"created": created,
	})
	common.ApiSuccess(c, gin.H{
		"created": created,
		"task":    task.ToResponse(),
	})
}
