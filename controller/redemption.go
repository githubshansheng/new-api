package controller

import (
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

func GetAllRedemptions(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.GetAllRedemptions(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func SearchRedemptions(c *gin.Context) {
	keyword := c.Query("keyword")
	status := c.Query("status")
	reclaimStatus := c.Query("reclaim_status")
	expireStart, err := strconv.ParseInt(c.DefaultQuery("expire_start", "0"), 10, 64)
	if err != nil || expireStart < 0 {
		common.ApiError(c, errors.New("invalid expire_start"))
		return
	}
	expireEnd, err := strconv.ParseInt(c.DefaultQuery("expire_end", "0"), 10, 64)
	if err != nil || expireEnd < 0 {
		common.ApiError(c, errors.New("invalid expire_end"))
		return
	}
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.SearchRedemptionsWithReclaimFilters(
		keyword,
		status,
		reclaimStatus,
		expireStart,
		expireEnd,
		pageInfo.GetStartIdx(),
		pageInfo.GetPageSize(),
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetRedemption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redemption, err := model.GetRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    redemption,
	})
	return
}

func AddRedemption(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorI18n(c, i18n.MsgPaymentComplianceRequired)
		return
	}

	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if utf8.RuneCountInString(redemption.Name) == 0 || utf8.RuneCountInString(redemption.Name) > 20 {
		common.ApiErrorI18n(c, i18n.MsgRedemptionNameLength)
		return
	}
	if redemption.Count <= 0 {
		common.ApiErrorI18n(c, i18n.MsgRedemptionCountPositive)
		return
	}
	if redemption.Count > 100 {
		common.ApiErrorI18n(c, i18n.MsgRedemptionCountMax)
		return
	}
	if redemption.Quota <= 0 || redemption.Quota >= common.MaxQuota {
		common.ApiError(c, errors.New("兑换额度必须大于 0 且小于系统额度上限"))
		return
	}
	if valid, msg := validateExpiredTime(c, redemption.ExpiredTime); !valid {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
		return
	}
	var keys []string
	for i := 0; i < redemption.Count; i++ {
		key := common.GetUUID()
		cleanRedemption := model.Redemption{
			UserId:      c.GetInt("id"),
			Name:        redemption.Name,
			Key:         key,
			CreatedTime: common.GetTimestamp(),
			Quota:       redemption.Quota,
			ExpiredTime: redemption.ExpiredTime,
		}
		err = cleanRedemption.Insert()
		if err != nil {
			common.SysError("failed to insert redemption: " + err.Error())
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": i18n.T(c, i18n.MsgRedemptionCreateFailed),
				"data":    keys,
			})
			return
		}
		keys = append(keys, key)
	}
	recordManageAudit(c, "redemption.create", map[string]interface{}{
		"name":  redemption.Name,
		"count": redemption.Count,
		"quota": logger.LogQuota(redemption.Quota),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    keys,
	})
	return
}

func DeleteRedemption(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	err := model.DeleteRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func UpdateRedemption(c *gin.Context) {
	statusOnly := c.Query("status_only")
	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var cleanRedemption *model.Redemption
	if statusOnly != "" {
		if redemption.Status != common.RedemptionCodeStatusEnabled &&
			redemption.Status != common.RedemptionCodeStatusDisabled {
			common.ApiError(c, errors.New("无效的兑换码状态"))
			return
		}
		cleanRedemption, err = model.UpdateRedemptionStatusForAdmin(redemption.Id, redemption.Status)
	} else {
		if utf8.RuneCountInString(redemption.Name) == 0 || utf8.RuneCountInString(redemption.Name) > 20 {
			common.ApiErrorI18n(c, i18n.MsgRedemptionNameLength)
			return
		}
		if redemption.Quota <= 0 || redemption.Quota >= common.MaxQuota {
			common.ApiError(c, errors.New("兑换额度必须大于 0 且小于系统额度上限"))
			return
		}

		current, getErr := model.GetRedemptionById(redemption.Id)
		if getErr != nil {
			common.ApiError(c, getErr)
			return
		}
		if current.ReclaimStatus == model.RedemptionReclaimStatusDisabled ||
			current.ExpiredTime != redemption.ExpiredTime {
			if valid, msg := validateExpiredTime(c, redemption.ExpiredTime); !valid {
				c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
				return
			}
		}
		cleanRedemption, err = model.UpdateRedemptionForAdmin(
			redemption.Id,
			redemption.Name,
			redemption.Quota,
			redemption.ExpiredTime,
		)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    cleanRedemption,
	})
	return
}

func DeleteInvalidRedemption(c *gin.Context) {
	rows, err := model.DeleteInvalidRedemptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

func validateExpiredTime(c *gin.Context, expired int64) (bool, string) {
	if expired != 0 && expired < common.GetTimestamp() {
		return false, i18n.T(c, i18n.MsgRedemptionExpireTimeInvalid)
	}
	return true, ""
}
