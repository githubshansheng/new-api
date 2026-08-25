package model

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type legacyRedemptionForReclaimMigration struct {
	Id          int    `gorm:"primaryKey"`
	Key         string `gorm:"type:char(32);uniqueIndex"`
	Status      int
	Name        string
	Quota       int
	ExpiredTime int64
}

func (legacyRedemptionForReclaimMigration) TableName() string {
	return "redemptions"
}

func setupRedemptionReclaimFixture(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	tables := []any{
		&Redemption{},
		&SubscriptionOrder{},
		&UserSubscription{},
		&SubscriptionPlan{},
		&Log{},
		&User{},
	}
	for _, table := range tables {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(table).Error)
	}
	t.Cleanup(func() {
		for _, table := range tables {
			require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(table).Error)
		}
	})
}

func setupConcurrentRedemptionReclaimFixture(t *testing.T) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "redemption-reclaim-concurrency.db")
	concurrentDB, err := gorm.Open(
		sqlite.Open(databasePath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"),
		&gorm.Config{},
	)
	require.NoError(t, err)
	require.NoError(t, concurrentDB.AutoMigrate(&User{}, &Redemption{}))
	sqlDB, err := concurrentDB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)

	originalDB := DB
	originalLogDB := LOG_DB
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	DB = concurrentDB
	LOG_DB = concurrentDB
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = originalDB
		LOG_DB = originalLogDB
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		require.NoError(t, sqlDB.Close())
	})
}

func TestRedemptionReclaimMigrationBackfillsLegacyRows(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "redemption-reclaim-migration.db")
	legacyDB, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, legacyDB.AutoMigrate(&legacyRedemptionForReclaimMigration{}))

	now := common.GetTimestamp()
	legacy := &legacyRedemptionForReclaimMigration{
		Key:         common.GetUUID(),
		Status:      common.RedemptionCodeStatusEnabled,
		Name:        "legacy-limited-quota",
		Quota:       50,
		ExpiredTime: now + 600,
	}
	require.NoError(t, legacyDB.Create(legacy).Error)
	require.NoError(t, prepareRedemptionReclaimMigrations(legacyDB))
	require.NoError(t, legacyDB.AutoMigrate(&Redemption{}))

	sqlDB, err := legacyDB.DB()
	require.NoError(t, err)
	originalDB := DB
	originalLogDB := LOG_DB
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	DB = legacyDB
	LOG_DB = legacyDB
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = originalDB
		LOG_DB = originalLogDB
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		require.NoError(t, sqlDB.Close())
	})

	var nullCount int64
	require.NoError(t, DB.Table("redemptions").Where("reclaim_status IS NULL").Count(&nullCount).Error)
	assert.Zero(t, nullCount)
	preview, err := PreviewRedemptionReclaims([]string{legacy.Key}, now)
	require.NoError(t, err)
	require.Len(t, preview.Items, 1)
	assert.Equal(t, RedemptionReclaimPreviewEligible, preview.Items[0].Result)

	result, err := EnableRedemptionReclaims([]string{legacy.Key}, preview.Snapshot, now)
	require.NoError(t, err)
	assert.Equal(t, 1, result.EnabledCount)
	var stored Redemption
	require.NoError(t, DB.First(&stored, legacy.Id).Error)
	assert.Equal(t, RedemptionReclaimStatusPending, stored.ReclaimStatus)
}

func TestNormalizeRedemptionReclaimKeysEnforcesInputBounds(t *testing.T) {
	tooMany := make([]string, maxRedemptionReclaimPreviewKeys+1)
	for i := range tooMany {
		tooMany[i] = "duplicate"
	}
	_, err := normalizeRedemptionReclaimKeys(tooMany)
	require.Error(t, err)

	_, err = normalizeRedemptionReclaimKeys([]string{strings.Repeat("x", maxRedemptionReclaimKeyBytes+1)})
	require.Error(t, err)
}

func createTimedQuotaFixture(t *testing.T, ordinaryQuota int, limitedQuotas ...int) (*User, []Redemption, int64) {
	t.Helper()
	now := common.GetTimestamp()
	total := ordinaryQuota
	for _, quota := range limitedQuotas {
		total += quota
	}
	user := &User{
		Username: "limited-quota-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    total,
	}
	require.NoError(t, DB.Create(user).Error)
	redemptions := make([]Redemption, 0, len(limitedQuotas))
	for i, quota := range limitedQuotas {
		redemptions = append(redemptions, Redemption{
			Key:                   common.GetUUID(),
			Name:                  "limited-quota",
			Status:                common.RedemptionCodeStatusUsed,
			Quota:                 quota,
			RedeemedTime:          now - 60,
			UsedUserId:            user.Id,
			ExpiredTime:           now + int64((i+1)*600),
			ReclaimStatus:         RedemptionReclaimStatusPending,
			ReclaimRemainingQuota: quota,
			ReclaimEnabledTime:    now - 120,
		})
	}
	require.NoError(t, DB.Create(&redemptions).Error)
	return user, redemptions, now
}

func TestTimedQuotaWalletConsumptionAndReclaimPreservesOrdinaryBalance(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	user, redemptions, _ := createTimedQuotaFixture(t, 40, 50)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("used_quota", 17).Error)

	allocation, reserved, err := ReserveUserQuotaWithTimedAllocation(user.Id, 30)
	require.NoError(t, err)
	require.True(t, reserved)
	require.Len(t, allocation.Segments, 1)
	assert.Equal(t, redemptions[0].Id, allocation.Segments[0].RedemptionId)

	var afterConsume User
	require.NoError(t, DB.First(&afterConsume, user.Id).Error)
	assert.Equal(t, 60, afterConsume.Quota)
	assert.Equal(t, 17, afterConsume.UsedQuota, "attribution must not change usage accounting")
	var redemption Redemption
	require.NoError(t, DB.First(&redemption, redemptions[0].Id).Error)
	assert.Equal(t, 20, redemption.ReclaimRemainingQuota)

	reclaimed, processed, err := SettleExpiredRedemptionQuotaForUser(user.Id, redemptions[0].ExpiredTime+1)
	require.NoError(t, err)
	assert.Equal(t, 20, reclaimed)
	assert.Equal(t, 1, processed)
	require.NoError(t, DB.First(&afterConsume, user.Id).Error)
	assert.Equal(t, 40, afterConsume.Quota)
	assert.Equal(t, 17, afterConsume.UsedQuota)

	reclaimed, processed, err = SettleExpiredRedemptionQuotaForUser(user.Id, redemptions[0].ExpiredTime+2)
	require.NoError(t, err)
	assert.Zero(t, reclaimed)
	assert.Zero(t, processed, "reclaim must be idempotent")
}

func TestRedeemSettlesExpiredLimitedQuotaBeforeCreditingNewCode(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	user, redemptions, now := createTimedQuotaFixture(t, 40, 50)
	require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", redemptions[0].Id).
		Update("expired_time", now-1).Error)
	newRedemption := &Redemption{
		Key:                common.GetUUID(),
		Name:               "new-limited-quota",
		Status:             common.RedemptionCodeStatusEnabled,
		Quota:              10,
		ExpiredTime:        now + 600,
		ReclaimStatus:      RedemptionReclaimStatusPending,
		ReclaimEnabledTime: now - 60,
	}
	require.NoError(t, DB.Create(newRedemption).Error)

	quota, err := Redeem(newRedemption.Key, user.Id)
	require.NoError(t, err)
	assert.Equal(t, 10, quota)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, 50, storedUser.Quota)
	var expired Redemption
	require.NoError(t, DB.First(&expired, redemptions[0].Id).Error)
	assert.Equal(t, RedemptionReclaimStatusCompleted, expired.ReclaimStatus)
	assert.Equal(t, 50, expired.ReclaimedQuota)
	var credited Redemption
	require.NoError(t, DB.First(&credited, newRedemption.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, credited.Status)
	assert.Equal(t, 10, credited.ReclaimRemainingQuota)
}

func TestTimedQuotaConsumptionUsesEarliestDeadlineFirst(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	user, redemptions, _ := createTimedQuotaFixture(t, 10, 20, 20)

	allocation, err := DebitUserQuotaWithTimedAllocation(user.Id, 30)
	require.NoError(t, err)
	require.Len(t, allocation.Segments, 2)
	assert.Equal(t, redemptions[0].Id, allocation.Segments[0].RedemptionId)
	assert.Equal(t, 20, allocation.Segments[0].Quota)
	assert.Equal(t, redemptions[1].Id, allocation.Segments[1].RedemptionId)
	assert.Equal(t, 10, allocation.Segments[1].Quota)

	var rows []Redemption
	require.NoError(t, DB.Order("expired_time").Find(&rows).Error)
	assert.Equal(t, 0, rows[0].ReclaimRemainingQuota)
	assert.Equal(t, 10, rows[1].ReclaimRemainingQuota)
}

func TestTimedQuotaRefundRestoresOnlyBeforeDeadline(t *testing.T) {
	t.Run("before deadline restores original attribution", func(t *testing.T) {
		setupRedemptionReclaimFixture(t)
		user, redemptions, _ := createTimedQuotaFixture(t, 40, 50)
		allocation, err := DebitUserQuotaWithTimedAllocation(user.Id, 30)
		require.NoError(t, err)
		require.NoError(t, RefundUserQuotaWithTimedAllocation(user.Id, 10, &allocation))

		var redemption Redemption
		require.NoError(t, DB.First(&redemption, redemptions[0].Id).Error)
		assert.Equal(t, 30, redemption.ReclaimRemainingQuota)
	})

	t.Run("after deadline refund becomes ordinary wallet quota", func(t *testing.T) {
		setupRedemptionReclaimFixture(t)
		user, redemptions, _ := createTimedQuotaFixture(t, 40, 50)
		allocation, err := DebitUserQuotaWithTimedAllocation(user.Id, 30)
		require.NoError(t, err)
		_, _, err = SettleExpiredRedemptionQuotaForUser(user.Id, redemptions[0].ExpiredTime+1)
		require.NoError(t, err)

		for i := range allocation.Segments {
			allocation.Segments[i].ExpiredTime = redemptions[0].ExpiredTime
		}
		// The public refund uses wall-clock time. Mark the allocation deadline in
		// the past to model a task refund that arrives after expiry.
		for i := range allocation.Segments {
			allocation.Segments[i].ExpiredTime = common.GetTimestamp() - 1
		}
		require.NoError(t, RefundUserQuotaWithTimedAllocation(user.Id, 10, &allocation))

		var afterRefund User
		require.NoError(t, DB.First(&afterRefund, user.Id).Error)
		assert.Equal(t, 50, afterRefund.Quota)
		var redemption Redemption
		require.NoError(t, DB.First(&redemption, redemptions[0].Id).Error)
		assert.Zero(t, redemption.ReclaimRemainingQuota)
	})
}

func TestTimedQuotaRemainsValidAtExactDeadline(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	user, redemptions, _ := createTimedQuotaFixture(t, 40, 50)

	reclaimed, processed, err := SettleExpiredRedemptionQuotaForUser(user.Id, redemptions[0].ExpiredTime)
	require.NoError(t, err)
	assert.Zero(t, reclaimed)
	assert.Zero(t, processed)

	err = DB.Transaction(func(tx *gorm.DB) error {
		if _, lockErr := lockUserForQuotaUpdate(tx, user.Id); lockErr != nil {
			return lockErr
		}
		_, allocateErr := allocateTimedQuotaTx(tx, user.Id, 10, redemptions[0].ExpiredTime)
		return allocateErr
	})
	require.NoError(t, err)
	var redemption Redemption
	require.NoError(t, DB.First(&redemption, redemptions[0].Id).Error)
	assert.Equal(t, 40, redemption.ReclaimRemainingQuota)
}

func TestSubscriptionBalancePurchaseExcludesActiveLimitedQuota(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	user, redemptions, _ := createTimedQuotaFixture(t, 40, 50)
	allowBalance := true
	expensive := &SubscriptionPlan{
		Title:           "expensive",
		PriceAmount:     60,
		Currency:        "CNY",
		DurationUnit:    "day",
		DurationValue:   1,
		Enabled:         true,
		AllowBalancePay: &allowBalance,
		TotalAmount:     100,
	}
	require.NoError(t, DB.Create(expensive).Error)
	InvalidateSubscriptionPlanCache(expensive.Id)
	require.Error(t, PurchaseSubscriptionWithBalance(user.Id, expensive.Id))

	affordable := *expensive
	affordable.Id = 0
	affordable.Title = "affordable"
	affordable.PriceAmount = 30
	require.NoError(t, DB.Create(&affordable).Error)
	InvalidateSubscriptionPlanCache(affordable.Id)
	require.NoError(t, PurchaseSubscriptionWithBalance(user.Id, affordable.Id))

	var afterPurchase User
	require.NoError(t, DB.First(&afterPurchase, user.Id).Error)
	assert.Equal(t, 60, afterPurchase.Quota)
	var redemption Redemption
	require.NoError(t, DB.First(&redemption, redemptions[0].Id).Error)
	assert.Equal(t, 50, redemption.ReclaimRemainingQuota, "subscription payment must not consume limited attribution")
}

func TestPreviewHistoricalReclaimUsesWalletFlowMarkers(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	user := &User{
		Username:  "historical-reclaim-user",
		Password:  "password",
		Status:    common.UserStatusEnabled,
		Quota:     75,
		UsedQuota: 35,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Key:          common.GetUUID(),
		Name:         "historical",
		Status:       common.RedemptionCodeStatusUsed,
		Quota:        50,
		RedeemedTime: now - 100,
		UsedUserId:   user.Id,
		ExpiredTime:  now + 600,
	}
	require.NoError(t, DB.Create(redemption).Error)
	logs := []Log{
		{UserId: user.Id, CreatedAt: now - 90, Type: LogTypeConsume, Quota: 30, RequestId: "a"},
		{UserId: user.Id, CreatedAt: now - 80, Type: LogTypeConsume, Quota: 20, RequestId: "b", Other: common.MapToJsonStr(map[string]interface{}{"billing_source": "subscription"})},
		{UserId: user.Id, CreatedAt: now - 70, Type: LogTypeRefund, Quota: 10, RequestId: "c", Other: common.MapToJsonStr(map[string]interface{}{"billing_source": "wallet"})},
		{UserId: user.Id, CreatedAt: now - 60, Type: LogTypeRefund, Quota: 5, RequestId: "d"},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	preview, err := PreviewRedemptionReclaims([]string{redemption.Key}, now)
	require.NoError(t, err)
	require.Len(t, preview.Items, 1)
	assert.Equal(t, RedemptionReclaimPreviewEligible, preview.Items[0].Result)
	assert.Equal(t, 30, preview.Items[0].RemainingQuota)
	assert.NotEmpty(t, preview.Snapshot)
}

func TestPreviewHistoricalReclaimAllowsExpiredUsedCodeAndReclaimsRemainder(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	user := &User{
		Username:  "expired-historical-reclaim-user",
		Password:  "password",
		Status:    common.UserStatusEnabled,
		Quota:     60,
		UsedQuota: 30,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Key:          common.GetUUID(),
		Name:         "expired-historical",
		Status:       common.RedemptionCodeStatusUsed,
		Quota:        50,
		RedeemedTime: now - 100,
		UsedUserId:   user.Id,
		ExpiredTime:  now - 10,
	}
	require.NoError(t, DB.Create(redemption).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:    user.Id,
		CreatedAt: now - 50,
		Type:      LogTypeConsume,
		Quota:     30,
		RequestId: "expired-wallet-consume",
		Other: common.MapToJsonStr(map[string]interface{}{
			"billing_source": "wallet",
		}),
	}).Error)

	preview, err := PreviewRedemptionReclaims([]string{redemption.Key}, now)
	require.NoError(t, err)
	require.Len(t, preview.Items, 1)
	assert.Equal(t, RedemptionReclaimPreviewEligible, preview.Items[0].Result)
	assert.Equal(t, 20, preview.Items[0].RemainingQuota)

	result, err := EnableRedemptionReclaims([]string{redemption.Key}, preview.Snapshot, now)
	require.NoError(t, err)
	assert.Equal(t, 1, result.EnabledCount)

	reclaimed, processed, err := SettleExpiredRedemptionQuotaForUser(user.Id, now)
	require.NoError(t, err)
	assert.Equal(t, 20, reclaimed)
	assert.Equal(t, 1, processed)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, 40, storedUser.Quota)
	assert.Equal(t, 30, storedUser.UsedQuota)

	var storedRedemption Redemption
	require.NoError(t, DB.First(&storedRedemption, redemption.Id).Error)
	assert.Equal(t, RedemptionReclaimStatusCompleted, storedRedemption.ReclaimStatus)
	assert.Equal(t, 20, storedRedemption.ReclaimedQuota)
}

func TestRetryManualReviewReconstructsExpiredRemainderAtomically(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	user := &User{
		Username:  "retry-manual-review-user",
		Password:  "password",
		Status:    common.UserStatusEnabled,
		Quota:     60,
		UsedQuota: 30,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Key:                   common.GetUUID(),
		Name:                  "retry-manual-review",
		Status:                common.RedemptionCodeStatusUsed,
		Quota:                 50,
		RedeemedTime:          now - 100,
		UsedUserId:            user.Id,
		ExpiredTime:           now - 10,
		ReclaimStatus:         RedemptionReclaimStatusManualReview,
		ReclaimEnabledTime:    now - 20,
		ReclaimRemainingQuota: 0,
		ReclaimError:          "historical data could not be verified",
	}
	require.NoError(t, DB.Create(redemption).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:    user.Id,
		CreatedAt: now - 50,
		Type:      LogTypeConsume,
		Quota:     30,
		RequestId: "retry-wallet-consume",
		Other: common.MapToJsonStr(map[string]interface{}{
			"billing_source": "wallet",
		}),
	}).Error)

	result, err := RetryManualRedemptionReclaims(redemption.Id, 7, now)
	require.NoError(t, err)
	assert.Equal(t, user.Id, result.UserId)
	assert.Equal(t, []int{redemption.Id}, result.RedemptionIds)
	assert.Equal(t, 1, result.UpdatedCount)
	assert.Equal(t, 1, result.CompletedCount)
	assert.Zero(t, result.PendingCount)
	assert.Equal(t, 20, result.ReclaimedQuota)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, 40, storedUser.Quota)
	assert.Equal(t, 30, storedUser.UsedQuota)

	var storedRedemption Redemption
	require.NoError(t, DB.First(&storedRedemption, redemption.Id).Error)
	assert.Equal(t, RedemptionReclaimStatusCompleted, storedRedemption.ReclaimStatus)
	assert.Zero(t, storedRedemption.ReclaimRemainingQuota)
	assert.Equal(t, 20, storedRedemption.ReclaimedQuota)
	assert.Equal(t, 7, storedRedemption.ReclaimReviewedBy)
	assert.Equal(t, now, storedRedemption.ReclaimReviewedTime)
	assert.Empty(t, storedRedemption.ReclaimReviewNote)
}

func TestResolveManualReviewReclaimsAdminConfirmedRemainderWithoutUsageLog(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	user := &User{
		Username:  "resolve-manual-review-user",
		Password:  "password",
		Status:    common.UserStatusEnabled,
		Quota:     65,
		UsedQuota: 35,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Key:                common.GetUUID(),
		Name:               "resolve-manual-review",
		Status:             common.RedemptionCodeStatusUsed,
		Quota:              50,
		RedeemedTime:       now - 100,
		UsedUserId:         user.Id,
		ExpiredTime:        now - 10,
		ReclaimStatus:      RedemptionReclaimStatusManualReview,
		ReclaimEnabledTime: now - 20,
		ReclaimError:       "wallet history is ambiguous",
	}
	require.NoError(t, DB.Create(redemption).Error)

	result, err := ResolveManualRedemptionReclaim(
		redemption.Id,
		9,
		15,
		"Confirmed against the external billing ledger.",
		now,
	)
	require.NoError(t, err)
	assert.Equal(t, user.Id, result.UserId)
	assert.Equal(t, []int{redemption.Id}, result.RedemptionIds)
	assert.Equal(t, 1, result.UpdatedCount)
	assert.Equal(t, 1, result.CompletedCount)
	assert.Equal(t, 15, result.ReclaimedQuota)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, 50, storedUser.Quota)
	assert.Equal(t, 35, storedUser.UsedQuota)

	var logCount int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("user_id = ?", user.Id).Count(&logCount).Error)
	assert.Zero(t, logCount)

	var storedRedemption Redemption
	require.NoError(t, DB.First(&storedRedemption, redemption.Id).Error)
	assert.Equal(t, RedemptionReclaimStatusCompleted, storedRedemption.ReclaimStatus)
	assert.Equal(t, 15, storedRedemption.ReclaimedQuota)
	assert.Equal(t, 9, storedRedemption.ReclaimReviewedBy)
	assert.Equal(t, now, storedRedemption.ReclaimReviewedTime)
	assert.Equal(t, "Confirmed against the external billing ledger.", storedRedemption.ReclaimReviewNote)
	assert.Empty(t, storedRedemption.ReclaimError)
}

func TestResolveManualReviewKeepsFutureRemainderActive(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	user := &User{
		Username: "resolve-future-manual-review-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    70,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Key:                common.GetUUID(),
		Name:               "resolve-future-manual-review",
		Status:             common.RedemptionCodeStatusUsed,
		Quota:              50,
		RedeemedTime:       now - 100,
		UsedUserId:         user.Id,
		ExpiredTime:        now + 600,
		ReclaimStatus:      RedemptionReclaimStatusManualReview,
		ReclaimEnabledTime: now - 20,
		ReclaimError:       "wallet history is ambiguous",
	}
	require.NoError(t, DB.Create(redemption).Error)

	result, err := ResolveManualRedemptionReclaim(redemption.Id, 9, 25, "Verified remainder.", now)
	require.NoError(t, err)
	assert.Equal(t, user.Id, result.UserId)
	assert.Equal(t, []int{redemption.Id}, result.RedemptionIds)
	assert.Equal(t, 1, result.PendingCount)
	assert.Zero(t, result.CompletedCount)
	assert.Zero(t, result.ReclaimedQuota)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, 70, storedUser.Quota)

	var storedRedemption Redemption
	require.NoError(t, DB.First(&storedRedemption, redemption.Id).Error)
	assert.Equal(t, RedemptionReclaimStatusPending, storedRedemption.ReclaimStatus)
	assert.Equal(t, 25, storedRedemption.ReclaimRemainingQuota)
	assert.Zero(t, storedRedemption.ReclaimedQuota)
}

func TestResolveManualReviewWithoutRedeemedUserOnlyAllowsZero(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	redemption := &Redemption{
		Key:                common.GetUUID(),
		Name:               "missing-redeemed-user",
		Status:             common.RedemptionCodeStatusUsed,
		Quota:              50,
		RedeemedTime:       now - 100,
		ExpiredTime:        now - 10,
		ReclaimStatus:      RedemptionReclaimStatusManualReview,
		ReclaimEnabledTime: now - 20,
		ReclaimError:       "redemption user is unavailable",
	}
	require.NoError(t, DB.Create(redemption).Error)

	_, err := ResolveManualRedemptionReclaim(redemption.Id, 9, 1, "Invalid remainder.", now)
	require.ErrorIs(t, err, ErrRedemptionReclaimReviewInvalid)

	result, err := ResolveManualRedemptionReclaim(redemption.Id, 9, 0, "Verified missing user record.", now)
	require.NoError(t, err)
	assert.Zero(t, result.UserId)
	assert.Equal(t, []int{redemption.Id}, result.RedemptionIds)
	assert.Equal(t, 1, result.CompletedCount)
	assert.Zero(t, result.ReclaimedQuota)

	var stored Redemption
	require.NoError(t, DB.First(&stored, redemption.Id).Error)
	assert.Equal(t, RedemptionReclaimStatusCompleted, stored.ReclaimStatus)
	assert.Equal(t, 9, stored.ReclaimReviewedBy)
	assert.Equal(t, "Verified missing user record.", stored.ReclaimReviewNote)
}

func TestResolveManualReviewRejectsInvalidRemainderWithoutMutation(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	user := &User{
		Username: "invalid-manual-review-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    50,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Key:                common.GetUUID(),
		Name:               "invalid-manual-review",
		Status:             common.RedemptionCodeStatusUsed,
		Quota:              50,
		RedeemedTime:       now - 100,
		UsedUserId:         user.Id,
		ExpiredTime:        now - 10,
		ReclaimStatus:      RedemptionReclaimStatusManualReview,
		ReclaimEnabledTime: now - 20,
		ReclaimError:       "wallet history is ambiguous",
	}
	require.NoError(t, DB.Create(redemption).Error)

	_, err := ResolveManualRedemptionReclaim(redemption.Id, 9, 51, "Invalid amount.", now)
	require.Error(t, err)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, 50, storedUser.Quota)
	var storedRedemption Redemption
	require.NoError(t, DB.First(&storedRedemption, redemption.Id).Error)
	assert.Equal(t, RedemptionReclaimStatusManualReview, storedRedemption.ReclaimStatus)
}

func TestEnableRedemptionReclaimRejectsChangedPreview(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	redemption := &Redemption{
		Key:         common.GetUUID(),
		Name:        "preview-conflict",
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       50,
		ExpiredTime: now + 600,
	}
	require.NoError(t, DB.Create(redemption).Error)
	preview, err := PreviewRedemptionReclaims([]string{redemption.Key}, now)
	require.NoError(t, err)

	require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", redemption.Id).Update("quota", 60).Error)
	_, err = EnableRedemptionReclaims([]string{redemption.Key}, preview.Snapshot, now)
	require.ErrorIs(t, err, ErrRedemptionReclaimPreviewConflict)

	var stored Redemption
	require.NoError(t, DB.First(&stored, redemption.Id).Error)
	assert.Equal(t, RedemptionReclaimStatusDisabled, stored.ReclaimStatus)
}

func TestEnableRedemptionReclaimRejectsChangedUserBalance(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	user := &User{
		Username:  "preview-balance-conflict-user",
		Password:  "password",
		Status:    common.UserStatusEnabled,
		Quota:     50,
		UsedQuota: 0,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Key:          common.GetUUID(),
		Name:         "preview-balance-conflict",
		Status:       common.RedemptionCodeStatusUsed,
		Quota:        50,
		RedeemedTime: now - 60,
		UsedUserId:   user.Id,
		ExpiredTime:  now + 600,
	}
	require.NoError(t, DB.Create(redemption).Error)

	preview, err := PreviewRedemptionReclaims([]string{redemption.Key}, now)
	require.NoError(t, err)
	require.Equal(t, RedemptionReclaimPreviewEligible, preview.Items[0].Result)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("quota", 40).Error)
	_, err = EnableRedemptionReclaims([]string{redemption.Key}, preview.Snapshot, now)
	require.ErrorIs(t, err, ErrRedemptionReclaimPreviewConflict)

	var stored Redemption
	require.NoError(t, DB.First(&stored, redemption.Id).Error)
	assert.Equal(t, RedemptionReclaimStatusDisabled, stored.ReclaimStatus)
}

func TestEnableRedemptionReclaimMarksIncompleteHistoricalRecordForReview(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	redemption := &Redemption{
		Key:          common.GetUUID(),
		Name:         "missing-user",
		Status:       common.RedemptionCodeStatusUsed,
		Quota:        50,
		RedeemedTime: now - 60,
		UsedUserId:   999,
		ExpiredTime:  now + 600,
	}
	require.NoError(t, DB.Create(redemption).Error)

	preview, err := PreviewRedemptionReclaims([]string{redemption.Key}, now)
	require.NoError(t, err)
	require.Len(t, preview.Items, 1)
	require.Equal(t, RedemptionReclaimPreviewManualReview, preview.Items[0].Result)
	assert.True(t, preview.Items[0].CanEnable)

	result, err := EnableRedemptionReclaims([]string{redemption.Key}, preview.Snapshot, now)
	require.NoError(t, err)
	assert.Equal(t, 1, result.ManualReviewCount)
	assert.Zero(t, result.EnabledCount)

	var stored Redemption
	require.NoError(t, DB.First(&stored, redemption.Id).Error)
	assert.Equal(t, RedemptionReclaimStatusManualReview, stored.ReclaimStatus)
	assert.NotEmpty(t, stored.ReclaimError)
}

func TestEnableRedemptionReclaimIgnoresBalanceChangesForSkippedCode(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	user := &User{
		Username: "already-enabled-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    50,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Key:                   common.GetUUID(),
		Name:                  "already-enabled",
		Status:                common.RedemptionCodeStatusUsed,
		Quota:                 50,
		RedeemedTime:          now - 60,
		UsedUserId:            user.Id,
		ExpiredTime:           now + 600,
		ReclaimStatus:         RedemptionReclaimStatusPending,
		ReclaimRemainingQuota: 50,
		ReclaimEnabledTime:    now - 30,
	}
	require.NoError(t, DB.Create(redemption).Error)

	preview, err := PreviewRedemptionReclaims([]string{redemption.Key}, now)
	require.NoError(t, err)
	require.Equal(t, RedemptionReclaimPreviewAlreadyEnabled, preview.Items[0].Result)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("quota", 40).Error)

	result, err := EnableRedemptionReclaims([]string{redemption.Key}, preview.Snapshot, now)
	require.NoError(t, err)
	assert.Equal(t, 1, result.SkippedCount)
	assert.Zero(t, result.EnabledCount)
}

func TestUpdateRedemptionForAdminAllowsOnlyNameAfterReclaimEnabled(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	redemption := &Redemption{
		Key:                common.GetUUID(),
		Name:               "before",
		Status:             common.RedemptionCodeStatusEnabled,
		Quota:              50,
		ExpiredTime:        now + 600,
		ReclaimStatus:      RedemptionReclaimStatusPending,
		ReclaimEnabledTime: now - 10,
	}
	require.NoError(t, DB.Create(redemption).Error)

	updated, err := UpdateRedemptionForAdmin(
		redemption.Id,
		"after",
		redemption.Quota,
		redemption.ExpiredTime,
	)
	require.NoError(t, err)
	assert.Equal(t, "after", updated.Name)
	assert.Equal(t, redemption.Quota, updated.Quota)
	assert.Equal(t, redemption.ExpiredTime, updated.ExpiredTime)

	_, err = UpdateRedemptionForAdmin(
		redemption.Id,
		"blocked-quota",
		redemption.Quota+1,
		redemption.ExpiredTime,
	)
	require.ErrorIs(t, err, ErrRedemptionReclaimProtected)

	_, err = UpdateRedemptionForAdmin(
		redemption.Id,
		"blocked-expiry",
		redemption.Quota,
		redemption.ExpiredTime+1,
	)
	require.ErrorIs(t, err, ErrRedemptionReclaimProtected)

	_, err = UpdateRedemptionStatusForAdmin(redemption.Id, common.RedemptionCodeStatusDisabled)
	require.ErrorIs(t, err, ErrRedemptionReclaimProtected)

	var stored Redemption
	require.NoError(t, DB.First(&stored, redemption.Id).Error)
	assert.Equal(t, "after", stored.Name)
	assert.Equal(t, 50, stored.Quota)
	assert.Equal(t, redemption.ExpiredTime, stored.ExpiredTime)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, stored.Status)
}

func TestDeleteRedemptionByIdProtectsRedeemedAndReclaimRecords(t *testing.T) {
	t.Run("redeemed code", func(t *testing.T) {
		setupRedemptionReclaimFixture(t)
		redemption := &Redemption{
			Key:        common.GetUUID(),
			Name:       "redeemed",
			Status:     common.RedemptionCodeStatusUsed,
			Quota:      50,
			UsedUserId: 123,
		}
		require.NoError(t, DB.Create(redemption).Error)

		err := DeleteRedemptionById(redemption.Id)
		require.ErrorIs(t, err, ErrRedemptionDeleteProtected)
		require.NoError(t, DB.First(&Redemption{}, redemption.Id).Error)
	})

	t.Run("reclaim-enabled code", func(t *testing.T) {
		setupRedemptionReclaimFixture(t)
		redemption := &Redemption{
			Key:           common.GetUUID(),
			Name:          "limited",
			Status:        common.RedemptionCodeStatusEnabled,
			Quota:         50,
			ExpiredTime:   common.GetTimestamp() + 600,
			ReclaimStatus: RedemptionReclaimStatusPending,
		}
		require.NoError(t, DB.Create(redemption).Error)

		err := DeleteRedemptionById(redemption.Id)
		require.ErrorIs(t, err, ErrRedemptionDeleteProtected)
		require.NoError(t, DB.First(&Redemption{}, redemption.Id).Error)
	})
}

func TestRedemptionReclaimStatsExcludeUnusedCodesFromUsersAndKeepDeepLinkStatus(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	startOfToday := StartOfLocalDay(now)
	rows := []Redemption{
		{
			Key:            common.GetUUID(),
			Name:           "reclaimed-used",
			Status:         common.RedemptionCodeStatusUsed,
			Quota:          50,
			UsedUserId:     42,
			ExpiredTime:    now - 100,
			ReclaimStatus:  RedemptionReclaimStatusCompleted,
			ReclaimedQuota: 20,
			ReclaimedTime:  startOfToday + 10,
		},
		{
			Key:           common.GetUUID(),
			Name:          "reclaimed-unused",
			Status:        common.RedemptionCodeStatusEnabled,
			Quota:         50,
			ExpiredTime:   now - 100,
			ReclaimStatus: RedemptionReclaimStatusCompleted,
			ReclaimedTime: startOfToday + 20,
		},
		{
			Key:                   common.GetUUID(),
			Name:                  "error",
			Status:                common.RedemptionCodeStatusUsed,
			Quota:                 10,
			UsedUserId:            42,
			ExpiredTime:           now - 30,
			ReclaimStatus:         RedemptionReclaimStatusError,
			ReclaimRemainingQuota: 10,
		},
		{
			Key:                   common.GetUUID(),
			Name:                  "due",
			Status:                common.RedemptionCodeStatusUsed,
			Quota:                 10,
			UsedUserId:            42,
			ExpiredTime:           now - 20,
			ReclaimStatus:         RedemptionReclaimStatusPending,
			ReclaimRemainingQuota: 10,
		},
		{
			Key:           common.GetUUID(),
			Name:          "manual",
			Status:        common.RedemptionCodeStatusUsed,
			Quota:         10,
			UsedUserId:    42,
			ExpiredTime:   now + 300,
			ReclaimStatus: RedemptionReclaimStatusManualReview,
		},
		{
			Key:                   common.GetUUID(),
			Name:                  "active",
			Status:                common.RedemptionCodeStatusUsed,
			Quota:                 10,
			UsedUserId:            42,
			ExpiredTime:           now + 600,
			ReclaimStatus:         RedemptionReclaimStatusPending,
			ReclaimRemainingQuota: 10,
		},
	}
	require.NoError(t, DB.Create(&rows).Error)

	stats, err := GetRedemptionReclaimStats(now)
	require.NoError(t, err)
	assert.Equal(t, startOfToday, stats.TodayStart)
	assert.Equal(t, int64(20), stats.ReclaimedTodayQuota)
	assert.Equal(t, int64(1), stats.ReclaimedTodayUsers)
	require.Len(t, stats.Trend, 7)
	var trendQuota int64
	var trendCodes int64
	var trendUsers int64
	for i := range stats.Trend {
		trendQuota += stats.Trend[i].Quota
		trendCodes += stats.Trend[i].CodeCount
		trendUsers += stats.Trend[i].UserCount
	}
	assert.Equal(t, int64(10), trendQuota)
	assert.Equal(t, int64(1), trendCodes)
	assert.Equal(t, int64(1), trendUsers)
	require.Len(t, stats.Attention, 4)
	assert.Equal(t, []string{"4", "due", "3", "active"}, []string{
		stats.Attention[0].Status,
		stats.Attention[1].Status,
		stats.Attention[2].Status,
		stats.Attention[3].Status,
	})
}

func TestCompletedReclaimSearchFiltersByReclaimedTime(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	user := &User{
		Username: "completed-reclaim-search-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    40,
	}
	require.NoError(t, DB.Create(user).Error)
	target := Redemption{
		Key:            common.GetUUID(),
		Name:           "reclaimed-today",
		Status:         common.RedemptionCodeStatusUsed,
		Quota:          50,
		UsedUserId:     user.Id,
		ExpiredTime:    now - 10_000,
		ReclaimStatus:  RedemptionReclaimStatusCompleted,
		ReclaimedQuota: 50,
		ReclaimedTime:  now - 10,
	}
	decoy := target
	decoy.Id = 0
	decoy.Key = common.GetUUID()
	decoy.Name = "expired-today-but-reclaimed-earlier"
	decoy.ExpiredTime = now - 10
	decoy.ReclaimedTime = now - 10_000
	require.NoError(t, DB.Create(&[]Redemption{target, decoy}).Error)

	rows, total, err := SearchRedemptionsWithReclaimFilters(
		"",
		"",
		strconv.Itoa(RedemptionReclaimStatusCompleted),
		now-60,
		now,
		0,
		10,
	)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "reclaimed-today", rows[0].Name)
}

func TestTimedQuotaPartialRefundRestoresLatestDebitAttribution(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	user, redemptions, _ := createTimedQuotaFixture(t, 40, 20, 30)

	allocation, err := DebitUserQuotaWithTimedAllocation(user.Id, 60)
	require.NoError(t, err)
	require.Len(t, allocation.Segments, 3)
	assert.Equal(t, redemptions[0].Id, allocation.Segments[0].RedemptionId)
	assert.Equal(t, 20, allocation.Segments[0].Quota)
	assert.Equal(t, redemptions[1].Id, allocation.Segments[1].RedemptionId)
	assert.Equal(t, 30, allocation.Segments[1].Quota)
	assert.Zero(t, allocation.Segments[2].RedemptionId)
	assert.Equal(t, 10, allocation.Segments[2].Quota)

	require.NoError(t, RefundUserQuotaWithTimedAllocation(user.Id, 25, &allocation))
	require.Len(t, allocation.Segments, 2)
	assert.Equal(t, redemptions[0].Id, allocation.Segments[0].RedemptionId)
	assert.Equal(t, 20, allocation.Segments[0].Quota)
	assert.Equal(t, redemptions[1].Id, allocation.Segments[1].RedemptionId)
	assert.Equal(t, 15, allocation.Segments[1].Quota)

	var userAfterRefund User
	require.NoError(t, DB.First(&userAfterRefund, user.Id).Error)
	assert.Equal(t, 55, userAfterRefund.Quota)
	var stored []Redemption
	require.NoError(t, DB.Order("expired_time").Find(&stored).Error)
	require.Len(t, stored, 2)
	assert.Zero(t, stored[0].ReclaimRemainingQuota)
	assert.Equal(t, 15, stored[1].ReclaimRemainingQuota)

	reclaimed, processed, err := SettleExpiredRedemptionQuotaForUser(user.Id, redemptions[0].ExpiredTime+1)
	require.NoError(t, err)
	assert.Zero(t, reclaimed)
	assert.Equal(t, 1, processed)
	reclaimed, processed, err = SettleExpiredRedemptionQuotaForUser(user.Id, redemptions[1].ExpiredTime+1)
	require.NoError(t, err)
	assert.Equal(t, 15, reclaimed)
	assert.Equal(t, 1, processed)
	require.NoError(t, DB.First(&userAfterRefund, user.Id).Error)
	assert.Equal(t, 40, userAfterRefund.Quota)
}

func TestSubscriptionBalancePurchaseAfterLimitedQuotaReclaim(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 1
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	user, redemptions, _ := createTimedQuotaFixture(t, 40, 50)
	_, _, err := SettleExpiredRedemptionQuotaForUser(user.Id, redemptions[0].ExpiredTime+1)
	require.NoError(t, err)

	allowBalance := true
	plan := &SubscriptionPlan{
		Title:           "after-reclaim",
		PriceAmount:     30,
		Currency:        "CNY",
		DurationUnit:    "day",
		DurationValue:   1,
		Enabled:         true,
		AllowBalancePay: &allowBalance,
		TotalAmount:     100,
	}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	require.NoError(t, PurchaseSubscriptionWithBalance(user.Id, plan.Id))

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, 10, storedUser.Quota)
	limitedQuota, err := GetLimitedQuotaSummary(user.Id, common.GetTimestamp())
	require.NoError(t, err)
	assert.Zero(t, limitedQuota.Total)
}

func TestReclaimBatchMarksCorruptAttributionWithoutChangingBalance(t *testing.T) {
	setupRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	user := &User{
		Username: "corrupt-reclaim-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    90,
	}
	require.NoError(t, DB.Create(user).Error)
	redemption := &Redemption{
		Key:                   common.GetUUID(),
		Name:                  "corrupt-remainder",
		Status:                common.RedemptionCodeStatusUsed,
		Quota:                 50,
		RedeemedTime:          now - 120,
		UsedUserId:            user.Id,
		ExpiredTime:           now - 1,
		ReclaimStatus:         RedemptionReclaimStatusPending,
		ReclaimRemainingQuota: -1,
		ReclaimEnabledTime:    now - 180,
	}
	require.NoError(t, DB.Create(redemption).Error)

	result, err := ReclaimExpiredRedemptionsBatch(context.Background(), now, 100)
	require.NoError(t, err)
	assert.Equal(t, 1, result.FailedUsers)
	assert.Zero(t, result.ProcessedUsers)
	assert.Zero(t, result.ProcessedCodes)
	assert.Zero(t, result.ReclaimedQuota)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, 90, storedUser.Quota)
	var storedRedemption Redemption
	require.NoError(t, DB.First(&storedRedemption, redemption.Id).Error)
	assert.Equal(t, RedemptionReclaimStatusError, storedRedemption.ReclaimStatus)
	assert.Equal(t, -1, storedRedemption.ReclaimRemainingQuota)
	assert.Zero(t, storedRedemption.ReclaimedQuota)
	assert.NotEmpty(t, storedRedemption.ReclaimError)
}

func TestTimedQuotaBoundaryFailuresAreAtomic(t *testing.T) {
	t.Run("wallet debit underflow", func(t *testing.T) {
		setupRedemptionReclaimFixture(t)
		user := &User{
			Username: "debit-underflow-user",
			Password: "password",
			Status:   common.UserStatusEnabled,
			Quota:    common.MinQuota + 5,
		}
		require.NoError(t, DB.Create(user).Error)

		_, err := DebitUserQuotaWithTimedAllocation(user.Id, 10)
		require.Error(t, err)
		var stored User
		require.NoError(t, DB.First(&stored, user.Id).Error)
		assert.Equal(t, common.MinQuota+5, stored.Quota)
	})

	t.Run("wallet refund overflow", func(t *testing.T) {
		setupRedemptionReclaimFixture(t)
		now := common.GetTimestamp()
		user := &User{
			Username: "refund-overflow-user",
			Password: "password",
			Status:   common.UserStatusEnabled,
			Quota:    common.MaxQuota - 5,
		}
		require.NoError(t, DB.Create(user).Error)
		redemption := &Redemption{
			Key:                common.GetUUID(),
			Name:               "refund-overflow",
			Status:             common.RedemptionCodeStatusUsed,
			Quota:              10,
			RedeemedTime:       now - 60,
			UsedUserId:         user.Id,
			ExpiredTime:        now + 600,
			ReclaimStatus:      RedemptionReclaimStatusPending,
			ReclaimEnabledTime: now - 120,
		}
		require.NoError(t, DB.Create(redemption).Error)
		allocation := WalletQuotaAllocation{Segments: []WalletQuotaAllocationSegment{{
			RedemptionId: redemption.Id,
			Quota:        10,
			ExpiredTime:  redemption.ExpiredTime,
		}}}

		err := RefundUserQuotaWithTimedAllocation(user.Id, 10, &allocation)
		require.Error(t, err)
		var storedUser User
		require.NoError(t, DB.First(&storedUser, user.Id).Error)
		assert.Equal(t, common.MaxQuota-5, storedUser.Quota)
		var storedRedemption Redemption
		require.NoError(t, DB.First(&storedRedemption, redemption.Id).Error)
		assert.Zero(t, storedRedemption.ReclaimRemainingQuota)
		require.Len(t, allocation.Segments, 1)
		assert.Equal(t, 10, allocation.Segments[0].Quota)
	})

	t.Run("expired reclaim underflow", func(t *testing.T) {
		setupRedemptionReclaimFixture(t)
		now := common.GetTimestamp()
		user := &User{
			Username: "reclaim-underflow-user",
			Password: "password",
			Status:   common.UserStatusEnabled,
			Quota:    common.MinQuota + 5,
		}
		require.NoError(t, DB.Create(user).Error)
		redemption := &Redemption{
			Key:                   common.GetUUID(),
			Name:                  "reclaim-underflow",
			Status:                common.RedemptionCodeStatusUsed,
			Quota:                 10,
			RedeemedTime:          now - 120,
			UsedUserId:            user.Id,
			ExpiredTime:           now - 1,
			ReclaimStatus:         RedemptionReclaimStatusPending,
			ReclaimRemainingQuota: 10,
			ReclaimEnabledTime:    now - 180,
		}
		require.NoError(t, DB.Create(redemption).Error)

		_, _, err := SettleExpiredRedemptionQuotaForUser(user.Id, now)
		require.Error(t, err)
		var storedUser User
		require.NoError(t, DB.First(&storedUser, user.Id).Error)
		assert.Equal(t, common.MinQuota+5, storedUser.Quota)
		var storedRedemption Redemption
		require.NoError(t, DB.First(&storedRedemption, redemption.Id).Error)
		assert.Equal(t, RedemptionReclaimStatusPending, storedRedemption.ReclaimStatus)
		assert.Equal(t, 10, storedRedemption.ReclaimRemainingQuota)
		assert.Zero(t, storedRedemption.ReclaimedQuota)
	})
}

func TestConcurrentTimedQuotaReservationsPreserveBalanceAndAttribution(t *testing.T) {
	setupConcurrentRedemptionReclaimFixture(t)
	user, redemptions, _ := createTimedQuotaFixture(t, 0, 50)

	type reservationResult struct {
		allocation WalletQuotaAllocation
		reserved   bool
		err        error
	}
	const workerCount = 2
	start := make(chan struct{})
	results := make(chan reservationResult, workerCount)
	var waitGroup sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			allocation, reserved, err := ReserveUserQuotaWithTimedAllocation(user.Id, 40)
			results <- reservationResult{allocation: allocation, reserved: reserved, err: err}
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	successCount := 0
	for result := range results {
		if result.err != nil || !result.reserved {
			continue
		}
		successCount++
		require.Len(t, result.allocation.Segments, 1)
		assert.Equal(t, redemptions[0].Id, result.allocation.Segments[0].RedemptionId)
		assert.Equal(t, 40, result.allocation.Segments[0].Quota)
	}
	assert.Equal(t, 1, successCount)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, 10, storedUser.Quota)
	var storedRedemption Redemption
	require.NoError(t, DB.First(&storedRedemption, redemptions[0].Id).Error)
	assert.Equal(t, 10, storedRedemption.ReclaimRemainingQuota)
}

func TestConcurrentTimedQuotaReclaimIsAppliedOnce(t *testing.T) {
	setupConcurrentRedemptionReclaimFixture(t)
	user, redemptions, now := createTimedQuotaFixture(t, 40, 50)
	require.NoError(t, DB.Model(&Redemption{}).Where("id = ?", redemptions[0].Id).
		Update("expired_time", now-1).Error)

	type reclaimResult struct {
		reclaimed int
		processed int
		err       error
	}
	const workerCount = 2
	start := make(chan struct{})
	results := make(chan reclaimResult, workerCount)
	var waitGroup sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			reclaimed, processed, err := SettleExpiredRedemptionQuotaForUser(user.Id, now)
			results <- reclaimResult{reclaimed: reclaimed, processed: processed, err: err}
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	successfulCalls := 0
	totalReclaimed := 0
	totalProcessed := 0
	for result := range results {
		if result.err != nil {
			continue
		}
		successfulCalls++
		totalReclaimed += result.reclaimed
		totalProcessed += result.processed
	}
	require.GreaterOrEqual(t, successfulCalls, 1)
	assert.Equal(t, 50, totalReclaimed)
	assert.Equal(t, 1, totalProcessed)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, 40, storedUser.Quota)
	var storedRedemption Redemption
	require.NoError(t, DB.First(&storedRedemption, redemptions[0].Id).Error)
	assert.Equal(t, RedemptionReclaimStatusCompleted, storedRedemption.ReclaimStatus)
	assert.Zero(t, storedRedemption.ReclaimRemainingQuota)
	assert.Equal(t, 50, storedRedemption.ReclaimedQuota)
}

func TestConcurrentUnusedRedemptionBatchCountsEachCodeOnce(t *testing.T) {
	setupConcurrentRedemptionReclaimFixture(t)
	now := common.GetTimestamp()
	redemptions := []Redemption{
		{
			Key:                common.GetUUID(),
			Name:               "unused-expired-one",
			Status:             common.RedemptionCodeStatusEnabled,
			Quota:              50,
			ExpiredTime:        now - 1,
			ReclaimStatus:      RedemptionReclaimStatusPending,
			ReclaimEnabledTime: now - 60,
		},
		{
			Key:                common.GetUUID(),
			Name:               "unused-expired-two",
			Status:             common.RedemptionCodeStatusEnabled,
			Quota:              50,
			ExpiredTime:        now - 1,
			ReclaimStatus:      RedemptionReclaimStatusPending,
			ReclaimEnabledTime: now - 60,
		},
	}
	require.NoError(t, DB.Create(&redemptions).Error)

	type batchResult struct {
		result RedemptionReclaimBatchResult
		err    error
	}
	const workerCount = 2
	start := make(chan struct{})
	results := make(chan batchResult, workerCount)
	var waitGroup sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			result, err := ReclaimExpiredRedemptionsBatch(context.Background(), now, 100)
			results <- batchResult{result: result, err: err}
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	successfulCalls := 0
	totalProcessed := 0
	for result := range results {
		if result.err != nil {
			continue
		}
		successfulCalls++
		totalProcessed += result.result.ProcessedCodes
	}
	require.GreaterOrEqual(t, successfulCalls, 1)
	assert.Equal(t, len(redemptions), totalProcessed)

	var stored []Redemption
	require.NoError(t, DB.Order("id").Find(&stored).Error)
	require.Len(t, stored, len(redemptions))
	for i := range stored {
		assert.Equal(t, RedemptionReclaimStatusCompleted, stored[i].ReclaimStatus)
		assert.Zero(t, stored[i].ReclaimRemainingQuota)
		assert.Equal(t, now, stored[i].ReclaimedTime)
	}

	third, err := ReclaimExpiredRedemptionsBatch(context.Background(), now, 100)
	require.NoError(t, err)
	assert.Zero(t, third.ProcessedCodes)
}
