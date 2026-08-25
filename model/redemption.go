package model

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

type Redemption struct {
	Id                    int            `json:"id"`
	UserId                int            `json:"user_id"`
	Key                   string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status                int            `json:"status" gorm:"default:1"`
	Name                  string         `json:"name" gorm:"index"`
	Quota                 int            `json:"quota" gorm:"default:100"`
	CreatedTime           int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime          int64          `json:"redeemed_time" gorm:"bigint"`
	Count                 int            `json:"count" gorm:"-:all"` // only for api request
	UsedUserId            int            `json:"used_user_id" gorm:"index:idx_redemption_reclaim_user,priority:1"`
	DeletedAt             gorm.DeletedAt `gorm:"index"`
	ExpiredTime           int64          `json:"expired_time" gorm:"bigint;index:idx_redemption_reclaim_due,priority:2"` // 过期时间，0 表示不过期
	ReclaimStatus         int            `json:"reclaim_status" gorm:"not null;default:0;index:idx_redemption_reclaim_due,priority:1;index:idx_redemption_reclaim_user,priority:2;index:idx_redemption_reclaim_completed,priority:1"`
	ReclaimRemainingQuota int            `json:"reclaim_remaining_quota" gorm:"not null;default:0"`
	ReclaimedQuota        int            `json:"reclaimed_quota" gorm:"not null;default:0"`
	ReclaimEnabledTime    int64          `json:"reclaim_enabled_time" gorm:"bigint;not null;default:0"`
	ReclaimedTime         int64          `json:"reclaimed_time" gorm:"bigint;not null;default:0;index:idx_redemption_reclaim_completed,priority:2"`
	ReclaimError          string         `json:"reclaim_error" gorm:"type:text"`
	ReclaimReviewedBy     int            `json:"reclaim_reviewed_by" gorm:"not null;default:0"`
	ReclaimReviewedTime   int64          `json:"reclaim_reviewed_time" gorm:"bigint;not null;default:0"`
	ReclaimReviewNote     string         `json:"reclaim_review_note" gorm:"type:text"`
}

var (
	ErrRedemptionReclaimProtected = errors.New("已启用限时额度回收的兑换码不能修改额度、截止时间或状态")
	ErrRedemptionDeleteProtected  = errors.New("已兑换或已进入限时回收流程的兑换码不能删除")
)

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取总数
	err = tx.Model(&Redemption{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func SearchRedemptions(keyword string, status string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	return SearchRedemptionsWithReclaimFilters(keyword, status, "", 0, 0, startIdx, num)
}

func SearchRedemptionsWithReclaimFilters(
	keyword string,
	status string,
	reclaimStatus string,
	expireStart int64,
	expireEnd int64,
	startIdx int,
	num int,
) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&Redemption{})

	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ? OR name LIKE ? OR "+commonKeyCol+" = ?", id, keyword+"%", keyword)
		} else {
			query = query.Where("name LIKE ? OR "+commonKeyCol+" = ?", keyword+"%", keyword)
		}
	}

	if reclaimStatus != "" {
		now := common.GetTimestamp()
		switch reclaimStatus {
		case "active":
			query = query.Where(
				"reclaim_status = ? AND used_user_id > 0 AND expired_time >= ?",
				RedemptionReclaimStatusPending,
				now,
			)
		case "waiting":
			query = query.Where("reclaim_status = ? AND used_user_id = 0", RedemptionReclaimStatusPending)
		case "due":
			query = query.Where(
				"reclaim_status = ? AND used_user_id > 0 AND expired_time > 0 AND expired_time < ?",
				RedemptionReclaimStatusPending,
				now,
			)
		case "attention":
			query = query.Where(
				"reclaim_status IN ? OR (reclaim_status = ? AND used_user_id > 0 AND expired_time > 0 AND expired_time < ?)",
				[]int{RedemptionReclaimStatusManualReview, RedemptionReclaimStatusError},
				RedemptionReclaimStatusPending,
				now,
			)
		default:
			if parsed, parseErr := strconv.Atoi(reclaimStatus); parseErr == nil &&
				parsed >= RedemptionReclaimStatusDisabled && parsed <= RedemptionReclaimStatusError {
				query = query.Where("reclaim_status = ?", parsed)
			}
		}
	}

	timeColumn := "expired_time"
	if reclaimStatus == strconv.Itoa(RedemptionReclaimStatusCompleted) {
		timeColumn = "reclaimed_time"
	}
	if expireStart > 0 {
		query = query.Where(timeColumn+" >= ?", expireStart)
	}
	if expireEnd > 0 {
		query = query.Where(timeColumn+" <= ?", expireEnd)
	}

	if status != "" {
		now := common.GetTimestamp()
		switch status {
		case "expired":
			query = query.Where(
				"status = ? AND expired_time != 0 AND expired_time < ?",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusEnabled):
			query = query.Where(
				"status = ? AND (expired_time = 0 OR expired_time >= ?)",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusDisabled):
			query = query.Where("status = ?", common.RedemptionCodeStatusDisabled)
		case strconv.Itoa(common.RedemptionCodeStatusUsed):
			query = query.Where("status = ?", common.RedemptionCodeStatusUsed)
		}
	}

	// Get total count
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated data
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	var err error = nil
	err = DB.First(&redemption, "id = ?", id).Error
	return &redemption, err
}

func Redeem(key string, userId int) (quota int, err error) {
	if key == "" {
		return 0, errors.New("未提供兑换码")
	}
	if userId == 0 {
		return 0, errors.New("无效的 user id")
	}
	redemption := &Redemption{}

	keyCol := "`key`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		keyCol = `"key"`
	}
	common.RandomSleep()
	reclaimedQuota := 0
	err = DB.Transaction(func(tx *gorm.DB) error {
		user, err := lockUserForQuotaUpdate(tx, userId)
		if err != nil {
			return err
		}
		reclaimedQuota, _, err = settleExpiredRedemptionQuotaTx(tx, userId, common.GetTimestamp())
		if err != nil {
			return err
		}
		err = lockForUpdate(tx).Where(keyCol+" = ?", key).First(redemption).Error
		if err != nil {
			return errors.New("无效的兑换码")
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("该兑换码已被使用")
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			return errors.New("该兑换码已过期")
		}
		if redemption.Quota <= 0 || redemption.Quota >= common.MaxQuota {
			return errors.New("兑换额度无效")
		}
		currentQuota := int64(user.Quota) - int64(reclaimedQuota)
		if currentQuota > int64(common.MaxQuota-1-redemption.Quota) {
			return errors.New("兑换后余额将超过系统额度上限")
		}
		// Compare-and-swap on status: only the transaction that flips
		// enabled -> used may credit quota, so a concurrent redeem of the
		// same code loses here even without a row lock (e.g. on SQLite).
		now := common.GetTimestamp()
		updates := map[string]interface{}{
			"redeemed_time": now,
			"status":        common.RedemptionCodeStatusUsed,
			"used_user_id":  userId,
		}
		if redemption.ReclaimStatus == RedemptionReclaimStatusPending {
			updates["reclaim_remaining_quota"] = redemption.Quota
		}
		result := tx.Model(&Redemption{}).
			Where("id = ? AND status = ?", redemption.Id, common.RedemptionCodeStatusEnabled).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("该兑换码已被使用")
		}
		return tx.Model(&User{}).Where("id = ?", userId).Update("quota", gorm.Expr("quota + ?", redemption.Quota)).Error
	})
	if err != nil {
		common.SysError("redemption failed: " + err.Error())
		return 0, ErrRedeemFailed
	}
	syncUserQuotaCacheDelta(
		userId,
		int64(redemption.Quota)-int64(reclaimedQuota),
		"redemption with expired limited quota settlement",
	)
	RecordLog(userId, LogTypeTopup, fmt.Sprintf("通过兑换码充值 %s，兑换码ID %d", logger.LogQuota(redemption.Quota), redemption.Id))
	return redemption.Quota, nil
}

func (redemption *Redemption) Insert() error {
	var err error
	err = DB.Create(redemption).Error
	return err
}

func (redemption *Redemption) SelectUpdate() error {
	// This can update zero values
	return DB.Model(redemption).Select("redeemed_time", "status").Updates(redemption).Error
}

// Update keeps the legacy model method safe for callers that edit redemption
// metadata. Status changes use UpdateRedemptionStatusForAdmin so reclaim
// protection is enforced atomically for both paths.
func (redemption *Redemption) Update() error {
	updated, err := UpdateRedemptionForAdmin(
		redemption.Id,
		redemption.Name,
		redemption.Quota,
		redemption.ExpiredTime,
	)
	if err != nil {
		return err
	}
	*redemption = *updated
	return nil
}

func (redemption *Redemption) Delete() error {
	var err error
	err = DB.Delete(redemption).Error
	return err
}

// UpdateRedemptionForAdmin atomically prevents quota or expiry edits once a
// redemption has entered the limited-quota reclaim flow. Name-only edits stay
// available for audit readability throughout the flow.
func UpdateRedemptionForAdmin(id int, name string, quota int, expiredTime int64) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}

	updated := &Redemption{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		current := &Redemption{}
		if err := lockForUpdate(tx).Where("id = ?", id).First(current).Error; err != nil {
			return err
		}

		if current.ReclaimStatus != RedemptionReclaimStatusDisabled {
			if current.Quota != quota || current.ExpiredTime != expiredTime {
				return ErrRedemptionReclaimProtected
			}
			if err := tx.Model(&Redemption{}).Where("id = ?", id).Update("name", name).Error; err != nil {
				return err
			}
			return tx.Where("id = ?", id).First(updated).Error
		}

		result := tx.Model(&Redemption{}).
			Where("id = ? AND reclaim_status = ?", id, RedemptionReclaimStatusDisabled).
			Updates(map[string]any{
				"name":         name,
				"quota":        quota,
				"expired_time": expiredTime,
			})
		if result.Error != nil {
			return result.Error
		}

		if err := lockForUpdate(tx).Where("id = ?", id).First(updated).Error; err != nil {
			return err
		}
		if updated.Quota != quota || updated.ExpiredTime != expiredTime {
			if updated.ReclaimStatus != RedemptionReclaimStatusDisabled {
				return ErrRedemptionReclaimProtected
			}
			return errors.New("兑换码已被并发修改，请刷新后重试")
		}
		if updated.Name != name {
			// A concurrent reclaim enable may win the conditional update on
			// SQLite. The protected fields still match, so only the name can be
			// safely applied after re-reading the row.
			if err := tx.Model(&Redemption{}).Where("id = ?", id).Update("name", name).Error; err != nil {
				return err
			}
			return tx.Where("id = ?", id).First(updated).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// UpdateRedemptionStatusForAdmin changes enablement only while the redemption
// is outside the limited-quota reclaim flow. The conditional update closes the
// SQLite check/update race while lockForUpdate serializes supported dialects.
func UpdateRedemptionStatusForAdmin(id int, status int) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}

	updated := &Redemption{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		current := &Redemption{}
		if err := lockForUpdate(tx).Where("id = ?", id).First(current).Error; err != nil {
			return err
		}
		if current.ReclaimStatus != RedemptionReclaimStatusDisabled {
			return ErrRedemptionReclaimProtected
		}

		result := tx.Model(&Redemption{}).
			Where("id = ? AND reclaim_status = ?", id, RedemptionReclaimStatusDisabled).
			Update("status", status)
		if result.Error != nil {
			return result.Error
		}
		if err := lockForUpdate(tx).Where("id = ?", id).First(updated).Error; err != nil {
			return err
		}
		if updated.ReclaimStatus != RedemptionReclaimStatusDisabled {
			return ErrRedemptionReclaimProtected
		}
		if updated.Status != status {
			return errors.New("兑换码已被并发修改，请刷新后重试")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func DeleteRedemptionById(id int) (err error) {
	if id == 0 {
		return errors.New("id 为空！")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		redemption := &Redemption{}
		if err := lockForUpdate(tx).Where("id = ?", id).First(redemption).Error; err != nil {
			return err
		}
		if redemption.UsedUserId != 0 || redemption.ReclaimStatus != RedemptionReclaimStatusDisabled {
			return ErrRedemptionDeleteProtected
		}

		result := tx.Where(
			"id = ? AND used_user_id = 0 AND reclaim_status = ?",
			id,
			RedemptionReclaimStatusDisabled,
		).Delete(&Redemption{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			var current Redemption
			if err := tx.Where("id = ?", id).First(&current).Error; err != nil {
				return err
			}
			return ErrRedemptionDeleteProtected
		}
		return nil
	})
}

func DeleteInvalidRedemptions() (int64, error) {
	now := common.GetTimestamp()
	result := DB.Where("used_user_id = 0 AND reclaim_status = ?", RedemptionReclaimStatusDisabled).
		Where("status = ? OR (status = ? AND expired_time != 0 AND expired_time < ?)", common.RedemptionCodeStatusDisabled, common.RedemptionCodeStatusEnabled, now).
		Delete(&Redemption{})
	return result.RowsAffected, result.Error
}
