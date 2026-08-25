package model

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

const (
	RedemptionReclaimStatusDisabled = iota
	RedemptionReclaimStatusPending
	RedemptionReclaimStatusCompleted
	RedemptionReclaimStatusManualReview
	RedemptionReclaimStatusError
)

type WalletQuotaAllocationSegment struct {
	RedemptionId int   `json:"redemption_id,omitempty"`
	Quota        int   `json:"quota"`
	ExpiredTime  int64 `json:"expired_time,omitempty"`
}

// WalletQuotaAllocation records attribution for an already-applied wallet
// debit. It never determines the amount charged; the billing path supplies the
// real quota delta and this value only allows a later refund to restore the
// same unexpired redemption lots.
type WalletQuotaAllocation struct {
	Segments []WalletQuotaAllocationSegment `json:"segments,omitempty"`
}

func (allocation WalletQuotaAllocation) Value() (driver.Value, error) {
	if len(allocation.Segments) == 0 {
		return nil, nil
	}
	data, err := common.Marshal(allocation)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func (allocation *WalletQuotaAllocation) Scan(value any) error {
	if allocation == nil {
		return errors.New("wallet quota allocation is nil")
	}
	if value == nil {
		*allocation = WalletQuotaAllocation{}
		return nil
	}
	var data []byte
	switch typed := value.(type) {
	case []byte:
		data = typed
	case string:
		data = []byte(typed)
	default:
		return fmt.Errorf("unsupported wallet quota allocation type %T", value)
	}
	if len(data) == 0 {
		*allocation = WalletQuotaAllocation{}
		return nil
	}
	return common.Unmarshal(data, allocation)
}

func (allocation WalletQuotaAllocation) Clone() WalletQuotaAllocation {
	segments := make([]WalletQuotaAllocationSegment, len(allocation.Segments))
	copy(segments, allocation.Segments)
	return WalletQuotaAllocation{Segments: segments}
}

func (allocation *WalletQuotaAllocation) Append(other WalletQuotaAllocation) {
	if allocation == nil || len(other.Segments) == 0 {
		return
	}
	allocation.Segments = append(allocation.Segments, other.Segments...)
}

type LimitedQuotaGroup struct {
	ExpiredTime    int64 `json:"expired_time"`
	RemainingQuota int   `json:"remaining_quota"`
}

type LimitedQuotaSummary struct {
	Total  int                 `json:"total"`
	Groups []LimitedQuotaGroup `json:"groups"`
}

type RedemptionReclaimBatchResult struct {
	ProcessedUsers int `json:"processed_users"`
	ProcessedCodes int `json:"processed_codes"`
	ReclaimedQuota int `json:"reclaimed_quota"`
	FailedUsers    int `json:"failed_users"`
}

func lockUserForQuotaUpdate(tx *gorm.DB, userId int) (*User, error) {
	user := &User{}
	if err := lockForUpdate(tx).Where("id = ?", userId).First(user).Error; err != nil {
		return nil, err
	}
	return user, nil
}

func settleExpiredRedemptionQuotaTx(tx *gorm.DB, userId int, now int64) (int, int, error) {
	var due []Redemption
	err := lockForUpdate(tx).
		Where("used_user_id = ? AND reclaim_status IN ? AND expired_time > 0 AND expired_time < ?",
			userId,
			[]int{RedemptionReclaimStatusPending, RedemptionReclaimStatusError},
			now,
		).
		Order("expired_time, id").
		Find(&due).Error
	if err != nil {
		return 0, 0, err
	}
	return settleRedemptionQuotaRowsTx(tx, userId, due, now)
}

func settleRedemptionQuotaRowsTx(tx *gorm.DB, userId int, due []Redemption, now int64) (int, int, error) {
	if len(due) == 0 {
		return 0, 0, nil
	}

	total64 := int64(0)
	for i := range due {
		if due[i].Quota <= 0 || due[i].Quota >= common.MaxQuota ||
			due[i].ReclaimRemainingQuota < 0 || due[i].ReclaimRemainingQuota > due[i].Quota ||
			due[i].ReclaimedQuota < 0 ||
			int64(due[i].ReclaimedQuota)+int64(due[i].ReclaimRemainingQuota) > int64(due[i].Quota) {
			return 0, 0, errors.New("limited quota attribution is invalid")
		}
		total64 += int64(due[i].ReclaimRemainingQuota)
		if total64 > int64(common.MaxQuota) {
			return 0, 0, errors.New("limited quota reclaim exceeds the supported quota range")
		}
	}
	total := int(total64)
	for i := range due {
		remaining := due[i].ReclaimRemainingQuota
		updates := map[string]any{
			"reclaim_status":          RedemptionReclaimStatusCompleted,
			"reclaim_remaining_quota": 0,
			"reclaimed_quota":         gorm.Expr("reclaimed_quota + ?", remaining),
			"reclaimed_time":          now,
			"reclaim_error":           "",
		}
		result := tx.Model(&Redemption{}).
			Where(
				"id = ? AND reclaim_status IN ? AND reclaim_remaining_quota = ?",
				due[i].Id,
				[]int{RedemptionReclaimStatusPending, RedemptionReclaimStatusError},
				remaining,
			).
			Updates(updates)
		if result.Error != nil {
			return 0, 0, result.Error
		}
		if result.RowsAffected != 1 {
			return 0, 0, errors.New("limited quota reclaim changed concurrently")
		}
	}

	if total > 0 {
		var currentQuota int
		if err := tx.Model(&User{}).Select("quota").Where("id = ?", userId).Scan(&currentQuota).Error; err != nil {
			return 0, 0, err
		}
		if int64(currentQuota)-total64 < int64(common.MinQuota) {
			return 0, 0, errors.New("limited quota reclaim would exceed the supported negative balance range")
		}
		result := tx.Model(&User{}).Where("id = ?", userId).
			Update("quota", gorm.Expr("quota - ?", total))
		if result.Error != nil {
			return 0, 0, result.Error
		}
		if result.RowsAffected != 1 {
			return 0, 0, gorm.ErrRecordNotFound
		}
	}

	return total, len(due), nil
}

func allocateTimedQuotaTx(tx *gorm.DB, userId int, amount int, now int64) (WalletQuotaAllocation, error) {
	allocation := WalletQuotaAllocation{}
	if amount <= 0 {
		return allocation, nil
	}

	var redemptions []Redemption
	err := lockForUpdate(tx).
		Where("used_user_id = ? AND reclaim_status = ? AND expired_time >= ? AND reclaim_remaining_quota > 0",
			userId, RedemptionReclaimStatusPending, now).
		Order("expired_time, redeemed_time, id").
		Find(&redemptions).Error
	if err != nil {
		return allocation, err
	}

	remaining := amount
	for i := range redemptions {
		if redemptions[i].Quota <= 0 || redemptions[i].Quota >= common.MaxQuota ||
			redemptions[i].ReclaimRemainingQuota < 0 ||
			redemptions[i].ReclaimRemainingQuota > redemptions[i].Quota {
			return WalletQuotaAllocation{}, errors.New("limited quota attribution is invalid")
		}
		if remaining <= 0 {
			break
		}
		take := redemptions[i].ReclaimRemainingQuota
		if take > remaining {
			take = remaining
		}
		if take <= 0 {
			continue
		}
		result := tx.Model(&Redemption{}).
			Where("id = ? AND reclaim_status = ? AND reclaim_remaining_quota >= ?",
				redemptions[i].Id, RedemptionReclaimStatusPending, take).
			Update("reclaim_remaining_quota", gorm.Expr("reclaim_remaining_quota - ?", take))
		if result.Error != nil {
			return WalletQuotaAllocation{}, result.Error
		}
		if result.RowsAffected != 1 {
			return WalletQuotaAllocation{}, errors.New("limited quota attribution changed concurrently")
		}
		allocation.Segments = append(allocation.Segments, WalletQuotaAllocationSegment{
			RedemptionId: redemptions[i].Id,
			Quota:        take,
			ExpiredTime:  redemptions[i].ExpiredTime,
		})
		remaining -= take
	}
	if remaining > 0 {
		allocation.Segments = append(allocation.Segments, WalletQuotaAllocationSegment{Quota: remaining})
	}
	return allocation, nil
}

func refundTimedQuotaTx(tx *gorm.DB, userId int, amount int, allocation *WalletQuotaAllocation, now int64) error {
	if amount <= 0 || allocation == nil || len(allocation.Segments) == 0 {
		return nil
	}

	remaining := amount
	for i := len(allocation.Segments) - 1; i >= 0 && remaining > 0; i-- {
		segment := &allocation.Segments[i]
		if segment.Quota <= 0 {
			continue
		}
		refunded := segment.Quota
		if refunded > remaining {
			refunded = remaining
		}
		segment.Quota -= refunded
		remaining -= refunded

		if segment.RedemptionId == 0 || segment.ExpiredTime < now {
			continue
		}
		redemption := &Redemption{}
		err := lockForUpdate(tx).Where("id = ? AND used_user_id = ?", segment.RedemptionId, userId).
			First(redemption).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		if redemption.ReclaimStatus != RedemptionReclaimStatusPending || redemption.ExpiredTime < now {
			continue
		}
		if redemption.Quota <= 0 || redemption.Quota >= common.MaxQuota ||
			redemption.ReclaimRemainingQuota < 0 || redemption.ReclaimRemainingQuota > redemption.Quota {
			return errors.New("limited quota attribution is invalid")
		}
		capacity := redemption.Quota - redemption.ReclaimRemainingQuota
		if capacity <= 0 {
			continue
		}
		restore := refunded
		if restore > capacity {
			restore = capacity
		}
		if restore > 0 {
			result := tx.Model(&Redemption{}).
				Where(
					"id = ? AND reclaim_status = ? AND reclaim_remaining_quota = ?",
					redemption.Id,
					RedemptionReclaimStatusPending,
					redemption.ReclaimRemainingQuota,
				).
				Update("reclaim_remaining_quota", gorm.Expr("reclaim_remaining_quota + ?", restore))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("limited quota refund attribution changed concurrently")
			}
		}
	}

	segments := allocation.Segments[:0]
	for i := range allocation.Segments {
		if allocation.Segments[i].Quota > 0 {
			segments = append(segments, allocation.Segments[i])
		}
	}
	allocation.Segments = segments
	return nil
}

// ReserveUserQuotaWithTimedAllocation atomically checks and debits the wallet
// while attributing the real debit to unexpired redemption lots. Expired lots
// are reclaimed before the balance check.
func ReserveUserQuotaWithTimedAllocation(userId int, amount int) (WalletQuotaAllocation, bool, error) {
	if userId <= 0 || amount < 0 {
		return WalletQuotaAllocation{}, false, errors.New("invalid wallet quota reservation")
	}
	if amount == 0 {
		return WalletQuotaAllocation{}, true, nil
	}

	now := common.GetTimestamp()
	allocation := WalletQuotaAllocation{}
	reserved := false
	reclaimed := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		user, err := lockUserForQuotaUpdate(tx, userId)
		if err != nil {
			return err
		}
		reclaimed, _, err = settleExpiredRedemptionQuotaTx(tx, userId, now)
		if err != nil {
			return err
		}
		if int64(user.Quota)-int64(reclaimed) < int64(amount) {
			return nil
		}
		allocation, err = allocateTimedQuotaTx(tx, userId, amount, now)
		if err != nil {
			return err
		}
		result := tx.Model(&User{}).Where("id = ?", userId).
			Update("quota", gorm.Expr("quota - ?", amount))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		reserved = true
		return nil
	})
	if err != nil {
		return WalletQuotaAllocation{}, false, err
	}
	cacheDelta := reclaimed
	if reserved {
		cacheDelta += amount
	}
	if cacheDelta > 0 {
		syncUserQuotaCacheDelta(userId, -int64(cacheDelta), "wallet quota reservation")
	}
	return allocation, reserved, nil
}

// DebitUserQuotaWithTimedAllocation applies an unconditional wallet debit used
// by settlement deltas. The user balance may become negative, matching the
// existing post-settlement accounting behavior.
func DebitUserQuotaWithTimedAllocation(userId int, amount int) (WalletQuotaAllocation, error) {
	if userId <= 0 || amount < 0 {
		return WalletQuotaAllocation{}, errors.New("invalid wallet quota debit")
	}
	if amount == 0 {
		return WalletQuotaAllocation{}, nil
	}

	now := common.GetTimestamp()
	allocation := WalletQuotaAllocation{}
	reclaimed := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		user, err := lockUserForQuotaUpdate(tx, userId)
		if err != nil {
			return err
		}
		reclaimed, _, err = settleExpiredRedemptionQuotaTx(tx, userId, now)
		if err != nil {
			return err
		}
		if int64(user.Quota)-int64(reclaimed)-int64(amount) < int64(common.MinQuota) {
			return errors.New("wallet debit would exceed the supported negative balance range")
		}
		allocation, err = allocateTimedQuotaTx(tx, userId, amount, now)
		if err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", userId).
			Update("quota", gorm.Expr("quota - ?", amount)).Error
	})
	if err != nil {
		return WalletQuotaAllocation{}, err
	}
	syncUserQuotaCacheDelta(userId, -int64(reclaimed+amount), "wallet quota debit")
	return allocation, nil
}

// RefundUserQuotaWithTimedAllocation credits the wallet once and restores only
// attribution segments that belong to the original debit and have not expired.
func RefundUserQuotaWithTimedAllocation(userId int, amount int, allocation *WalletQuotaAllocation) error {
	if userId <= 0 || amount < 0 {
		return errors.New("invalid wallet quota refund")
	}
	if amount == 0 {
		return nil
	}

	now := common.GetTimestamp()
	reclaimed := 0
	working := WalletQuotaAllocation{}
	if allocation != nil {
		working = allocation.Clone()
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		user, err := lockUserForQuotaUpdate(tx, userId)
		if err != nil {
			return err
		}
		reclaimed, _, err = settleExpiredRedemptionQuotaTx(tx, userId, now)
		if err != nil {
			return err
		}
		if int64(user.Quota)-int64(reclaimed)+int64(amount) >= int64(common.MaxQuota) {
			return errors.New("wallet refund would exceed the supported balance range")
		}
		if err := refundTimedQuotaTx(tx, userId, amount, &working, now); err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", userId).
			Update("quota", gorm.Expr("quota + ?", amount)).Error
	})
	if err != nil {
		return err
	}
	if allocation != nil {
		*allocation = working
	}
	cacheDelta := amount - reclaimed
	syncUserQuotaCacheDelta(userId, int64(cacheDelta), "wallet quota refund")
	return nil
}

func SettleExpiredRedemptionQuotaForUser(userId int, now int64) (int, int, error) {
	if userId <= 0 {
		return 0, 0, errors.New("invalid user id")
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	reclaimed := 0
	processed := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		if _, err := lockUserForQuotaUpdate(tx, userId); err != nil {
			return err
		}
		var err error
		reclaimed, processed, err = settleExpiredRedemptionQuotaTx(tx, userId, now)
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	if reclaimed > 0 {
		syncUserQuotaCacheDelta(userId, -int64(reclaimed), "limited quota reclaim")
	}
	return reclaimed, processed, nil
}

func SumActiveLimitedQuotaTx(tx *gorm.DB, userId int, now int64, lock bool) (int, error) {
	query := tx.Model(&Redemption{}).
		Where("used_user_id = ? AND reclaim_status = ? AND expired_time >= ? AND reclaim_remaining_quota > 0",
			userId, RedemptionReclaimStatusPending, now)
	if lock {
		query = lockForUpdate(query)
	}
	var redemptions []Redemption
	if err := query.Select("id", "quota", "reclaim_remaining_quota").Find(&redemptions).Error; err != nil {
		return 0, err
	}
	total64 := int64(0)
	for i := range redemptions {
		if redemptions[i].Quota <= 0 || redemptions[i].Quota >= common.MaxQuota ||
			redemptions[i].ReclaimRemainingQuota < 0 ||
			redemptions[i].ReclaimRemainingQuota > redemptions[i].Quota {
			return 0, errors.New("limited quota attribution is invalid")
		}
		total64 += int64(redemptions[i].ReclaimRemainingQuota)
		if total64 > int64(common.MaxQuota) {
			return 0, errors.New("active limited quota exceeds the supported quota range")
		}
	}
	return int(total64), nil
}

func GetLimitedQuotaSummary(userId int, now int64) (LimitedQuotaSummary, error) {
	if userId <= 0 {
		return LimitedQuotaSummary{}, errors.New("invalid user id")
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	var rows []Redemption
	err := DB.Select("quota", "expired_time", "reclaim_remaining_quota").
		Where("used_user_id = ? AND reclaim_status = ? AND expired_time >= ? AND reclaim_remaining_quota > 0",
			userId, RedemptionReclaimStatusPending, now).
		Order("expired_time").
		Find(&rows).Error
	if err != nil {
		return LimitedQuotaSummary{}, err
	}

	grouped := make(map[int64]int)
	for i := range rows {
		if rows[i].Quota <= 0 || rows[i].Quota >= common.MaxQuota ||
			rows[i].ReclaimRemainingQuota < 0 || rows[i].ReclaimRemainingQuota > rows[i].Quota {
			return LimitedQuotaSummary{}, errors.New("limited quota attribution is invalid")
		}
		grouped[rows[i].ExpiredTime] += rows[i].ReclaimRemainingQuota
	}
	expirations := make([]int64, 0, len(grouped))
	for expiredTime := range grouped {
		expirations = append(expirations, expiredTime)
	}
	sort.Slice(expirations, func(i, j int) bool { return expirations[i] < expirations[j] })

	summary := LimitedQuotaSummary{Groups: make([]LimitedQuotaGroup, 0, len(expirations))}
	for _, expiredTime := range expirations {
		quota := grouped[expiredTime]
		if quota < 0 || int64(summary.Total)+int64(quota) > int64(common.MaxQuota) {
			return LimitedQuotaSummary{}, errors.New("limited quota summary exceeds the supported quota range")
		}
		summary.Total += quota
		summary.Groups = append(summary.Groups, LimitedQuotaGroup{
			ExpiredTime:    expiredTime,
			RemainingQuota: quota,
		})
	}
	return summary, nil
}

func HasDueRedemptionQuota(now int64) bool {
	if now <= 0 {
		now = common.GetTimestamp()
	}
	var count int64
	err := DB.Model(&Redemption{}).
		Where("reclaim_status IN ? AND expired_time > 0 AND expired_time < ?",
			[]int{RedemptionReclaimStatusPending, RedemptionReclaimStatusError}, now).
		Limit(1).
		Count(&count).Error
	return err == nil && count > 0
}

func markRedemptionReclaimError(userId int, now int64, err error) {
	if err == nil {
		return
	}
	message := err.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	_ = DB.Model(&Redemption{}).
		Where("used_user_id = ? AND reclaim_status IN ? AND expired_time > 0 AND expired_time < ?",
			userId, []int{RedemptionReclaimStatusPending, RedemptionReclaimStatusError}, now).
		Updates(map[string]any{
			"reclaim_status": RedemptionReclaimStatusError,
			"reclaim_error":  message,
		}).Error
}

func ReclaimExpiredRedemptionsBatch(ctx context.Context, now int64, limit int) (RedemptionReclaimBatchResult, error) {
	if now <= 0 {
		now = common.GetTimestamp()
	}
	if limit <= 0 {
		limit = 100
	}
	result := RedemptionReclaimBatchResult{}

	var userIds []int
	err := DB.WithContext(ctx).Model(&Redemption{}).
		Distinct("used_user_id").
		Where("used_user_id > 0 AND reclaim_status IN ? AND expired_time > 0 AND expired_time < ?",
			[]int{RedemptionReclaimStatusPending, RedemptionReclaimStatusError}, now).
		Order("used_user_id").
		Limit(limit).
		Pluck("used_user_id", &userIds).Error
	if err != nil {
		return result, err
	}

	for _, userId := range userIds {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		reclaimed, processed, reclaimErr := SettleExpiredRedemptionQuotaForUser(userId, now)
		if reclaimErr != nil {
			result.FailedUsers++
			markRedemptionReclaimError(userId, now, reclaimErr)
			continue
		}
		result.ProcessedUsers++
		result.ProcessedCodes += processed
		result.ReclaimedQuota += reclaimed
	}

	unused := []Redemption{}
	if err := DB.WithContext(ctx).
		Where("used_user_id = 0 AND reclaim_status IN ? AND expired_time > 0 AND expired_time < ?",
			[]int{RedemptionReclaimStatusPending, RedemptionReclaimStatusError}, now).
		Order("id").Limit(limit).Find(&unused).Error; err != nil {
		return result, err
	}
	if len(unused) > 0 {
		ids := make([]int, 0, len(unused))
		for i := range unused {
			ids = append(ids, unused[i].Id)
		}
		update := DB.WithContext(ctx).Model(&Redemption{}).
			Where(
				"id IN ? AND used_user_id = 0 AND reclaim_status IN ? AND expired_time > 0 AND expired_time < ?",
				ids,
				[]int{RedemptionReclaimStatusPending, RedemptionReclaimStatusError},
				now,
			).
			Updates(map[string]any{
				"reclaim_status":          RedemptionReclaimStatusCompleted,
				"reclaim_remaining_quota": 0,
				"reclaimed_time":          now,
				"reclaim_error":           "",
			})
		if update.Error != nil {
			return result, update.Error
		}
		result.ProcessedCodes += int(update.RowsAffected)
	}
	return result, nil
}

func StartOfLocalDay(timestamp int64) int64 {
	current := time.Unix(timestamp, 0).In(time.Local)
	year, month, day := current.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, current.Location()).Unix()
}
