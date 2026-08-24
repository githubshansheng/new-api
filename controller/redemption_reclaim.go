package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type redemptionReclaimPreviewRequest struct {
	Keys []string `json:"keys" binding:"required"`
}

type redemptionReclaimEnableRequest struct {
	Keys     []string `json:"keys" binding:"required"`
	Snapshot string   `json:"snapshot" binding:"required"`
}

func PreviewRedemptionReclaim(c *gin.Context) {
	request := redemptionReclaimPreviewRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	preview, err := model.PreviewRedemptionReclaims(request.Keys, common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, preview)
}

func EnableRedemptionReclaim(c *gin.Context) {
	request := redemptionReclaimEnableRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := model.EnableRedemptionReclaims(request.Keys, request.Snapshot, common.GetTimestamp())
	if err != nil {
		if errors.Is(err, model.ErrRedemptionReclaimPreviewConflict) {
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"message": "兑换码状态或额度已变化，请重新预览",
			})
			return
		}
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "redemption.reclaim.enable", map[string]interface{}{
		"enabled_count":       result.EnabledCount,
		"manual_review_count": result.ManualReviewCount,
		"skipped_count":       result.SkippedCount,
	})
	common.ApiSuccess(c, result)
}

func GetRedemptionReclaimStats(c *gin.Context) {
	stats, err := model.GetRedemptionReclaimStats(common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, stats)
}

func GetLimitedQuotaSummary(c *gin.Context) {
	userId := c.GetInt("id")
	now := common.GetTimestamp()
	if _, _, err := model.SettleExpiredRedemptionQuotaForUser(userId, now); err != nil {
		common.ApiError(c, err)
		return
	}
	summary, err := model.GetLimitedQuotaSummary(userId, now)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, summary)
}
