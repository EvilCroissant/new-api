package controller

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func GetPerfMetricsSummary(c *gin.Context) {
	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	recentModels := c.QueryArray("recent_model")
	if len(recentModels) > perfmetrics.MaxRecentModels {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	result, err := perfmetrics.QuerySummaryAll(hours, visiblePerfGroups(c), recentModels...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

func GetPerfMetrics(c *gin.Context) {
	modelName := c.Query("model")
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "model is required",
		})
		return
	}

	hours := 24
	if rawHours := c.Query("hours"); rawHours != "" {
		if parsed, err := strconv.Atoi(rawHours); err == nil {
			hours = parsed
		}
	}

	result, err := perfmetrics.Query(perfmetrics.QueryParams{
		Model:         modelName,
		Group:         c.Query("group"),
		Hours:         hours,
		AllowedGroups: visiblePerfGroups(c),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

func visiblePerfGroups(c *gin.Context) []string {
	visible := visiblePerfGroupSet(c)
	groups := make([]string, 0, len(visible))
	for group := range visible {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

func visiblePerfGroupSet(c *gin.Context) map[string]struct{} {
	userGroup := ""
	if c != nil {
		userGroup = c.GetString("user_group")
	}
	usableGroups := service.GetUserUsableGroups(userGroup)
	visible := make(map[string]struct{})
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroups[group]; ok {
			visible[group] = struct{}{}
		}
	}
	if len(service.GetUserAutoGroup(userGroup)) > 0 {
		visible["auto"] = struct{}{}
	}
	return visible
}
