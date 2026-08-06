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
	Enabled *bool `json:"enabled" binding:"required"`
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
	config, err := service.SetChannelProfitMonitoring(channelId, *request.Enabled)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	action := "channel_profit.enable"
	if !*request.Enabled {
		action = "channel_profit.disable"
	}
	recordManageAudit(c, action, map[string]interface{}{"id": channelId})
	common.ApiSuccess(c, config)
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
