package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type redemptionReclaimPreviewRequest struct {
	Keys []string `json:"keys" binding:"required"`
}

type redemptionReclaimEnableRequest struct {
	Keys     []string `json:"keys" binding:"required"`
	Snapshot string   `json:"snapshot" binding:"required"`
}

type redemptionReclaimResolveRequest struct {
	RemainingQuota *int   `json:"remaining_quota"`
	Note           string `json:"note"`
}

func redemptionReclaimId(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的兑换码 ID",
		})
		return 0, false
	}
	return id, true
}

func writeRedemptionReclaimReviewError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrRedemptionReclaimReviewInvalid):
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "人工核对参数无效，请检查核定额度和审核说明",
		})
	case errors.Is(err, model.ErrRedemptionReclaimNotManualReview),
		errors.Is(err, model.ErrRedemptionReclaimPreviewConflict):
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"message": "记录状态或用户余额已变化，请刷新后重试",
		})
	case errors.Is(err, model.ErrRedemptionReclaimReconstruct):
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"success": false,
			"message": err.Error(),
		})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "兑换码或兑换用户不存在",
		})
	default:
		common.ApiError(c, err)
	}
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

func RetryManualRedemptionReclaim(c *gin.Context) {
	redemptionId, ok := redemptionReclaimId(c)
	if !ok {
		return
	}
	result, err := model.RetryManualRedemptionReclaims(
		redemptionId,
		c.GetInt("id"),
		common.GetTimestamp(),
	)
	if err != nil {
		writeRedemptionReclaimReviewError(c, err)
		return
	}
	recordManageAuditFor(c, result.UserId, "redemption.reclaim.review_retry", map[string]interface{}{
		"redemption_id":   redemptionId,
		"redemption_ids":  result.RedemptionIds,
		"updated_count":   result.UpdatedCount,
		"pending_count":   result.PendingCount,
		"completed_count": result.CompletedCount,
		"reclaimed_quota": result.ReclaimedQuota,
	})
	common.ApiSuccess(c, result)
}

func ResolveManualRedemptionReclaim(c *gin.Context) {
	redemptionId, ok := redemptionReclaimId(c)
	if !ok {
		return
	}
	request := redemptionReclaimResolveRequest{}
	if err := c.ShouldBindJSON(&request); err != nil || request.RemainingQuota == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "必须填写核定后的剩余限时额度和审核说明",
		})
		return
	}
	reviewNote := strings.TrimSpace(request.Note)
	result, err := model.ResolveManualRedemptionReclaim(
		redemptionId,
		c.GetInt("id"),
		*request.RemainingQuota,
		reviewNote,
		common.GetTimestamp(),
	)
	if err != nil {
		writeRedemptionReclaimReviewError(c, err)
		return
	}
	recordManageAuditFor(c, result.UserId, "redemption.reclaim.review_resolve", map[string]interface{}{
		"redemption_id":   redemptionId,
		"remaining_quota": logger.LogQuota(*request.RemainingQuota),
		"review_note":     reviewNote,
		"pending_count":   result.PendingCount,
		"completed_count": result.CompletedCount,
		"reclaimed_quota": result.ReclaimedQuota,
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
