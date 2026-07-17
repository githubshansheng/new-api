package model

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const testLiandongJUUID = "test-merchant-id"

type liandongRecordingGormLogger struct {
	statements strings.Builder
}

type legacyLiandongProduct struct {
	ID                  int    `gorm:"primaryKey"`
	BusinessType        string `gorm:"type:varchar(32);not null;index"`
	Name                string `gorm:"type:varchar(128);not null"`
	GoodsKey            string `gorm:"type:varchar(128);not null;uniqueIndex"`
	QuotaAmount         int64  `gorm:"type:bigint;not null"`
	PlanID              int    `gorm:"index"`
	ExpectedAmountMinor int64  `gorm:"type:bigint;not null"`
	Currency            string `gorm:"type:varchar(8);not null"`
	Enabled             bool
	SortOrder           int
	CreatedBy           int
	UpdatedBy           int
	CreatedAt           int64 `gorm:"type:bigint"`
	UpdatedAt           int64 `gorm:"type:bigint"`
}

func (legacyLiandongProduct) TableName() string {
	return "liandong_products"
}

type legacyLiandongOrder struct {
	ID                    int     `gorm:"primaryKey"`
	LocalTradeNo          string  `gorm:"type:varchar(128);not null;uniqueIndex"`
	ProviderTradeNo       *string `gorm:"type:varchar(128);uniqueIndex"`
	UserID                int     `gorm:"not null;index"`
	ProductID             int     `gorm:"not null;index"`
	ProductNameSnapshot   string  `gorm:"type:varchar(128);not null"`
	BusinessType          string  `gorm:"type:varchar(32);not null;index"`
	TargetID              int     `gorm:"index"`
	GoodsKeySnapshot      string  `gorm:"type:varchar(128);not null"`
	ContactSnapshot       string  `gorm:"type:varchar(12);not null;uniqueIndex"`
	JUUIDSnapshot         string  `gorm:"type:varchar(128);not null"`
	ExpectedAmountMinor   int64   `gorm:"type:bigint;not null"`
	CurrencySnapshot      string  `gorm:"type:varchar(8);not null"`
	FulfillmentSnapshot   string  `gorm:"type:text;not null"`
	PaymentStatus         string  `gorm:"type:varchar(32);not null;index"`
	FulfillmentStatus     string  `gorm:"type:varchar(32);not null;index"`
	LastCheckAt           int64   `gorm:"type:bigint"`
	NextCheckAt           int64   `gorm:"type:bigint;index"`
	CheckDeadlineAt       int64   `gorm:"type:bigint;index"`
	CheckCount            int
	ConsecutiveErrorCount int
	CheckLockUntil        int64  `gorm:"type:bigint;index"`
	ProviderSummary       string `gorm:"type:text"`
	LastError             string `gorm:"type:text"`
	PaidAt                int64  `gorm:"type:bigint"`
	FulfilledAt           int64  `gorm:"type:bigint"`
	CreatedAt             int64  `gorm:"type:bigint;index"`
	UpdatedAt             int64  `gorm:"type:bigint"`
}

func (legacyLiandongOrder) TableName() string {
	return "liandong_orders"
}

func (l *liandongRecordingGormLogger) LogMode(gormlogger.LogLevel) gormlogger.Interface {
	return l
}

func (l *liandongRecordingGormLogger) Info(context.Context, string, ...interface{}) {}

func (l *liandongRecordingGormLogger) Warn(context.Context, string, ...interface{}) {}

func (l *liandongRecordingGormLogger) Error(context.Context, string, ...interface{}) {}

func (l *liandongRecordingGormLogger) Trace(
	_ context.Context,
	_ time.Time,
	sql func() (string, int64),
	_ error,
) {
	statement, _ := sql()
	l.statements.WriteString(statement)
	l.statements.WriteByte('\n')
}

func createLiandongQuotaFixture(t *testing.T) (*User, *LiandongProduct) {
	t.Helper()
	user := &User{
		Username: fmt.Sprintf("liandong-user-%s", common.GetUUID()),
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, DB.Create(user).Error)
	product := &LiandongProduct{
		BusinessType:        LiandongBusinessTypeQuota,
		Name:                "Quota 100",
		GoodsKey:            fmt.Sprintf("goods-%s", common.GetUUID()),
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, DB.Create(product).Error)
	return user, product
}

func createLiandongSubscriptionOrder(t *testing.T, maxPurchasePerUser int) (*User, *SubscriptionPlan, *LiandongOrder) {
	t.Helper()
	user := &User{
		Username: fmt.Sprintf("liandong-sub-user-%s", common.GetUUID()),
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, DB.Create(user).Error)
	plan := &SubscriptionPlan{
		Title:              "Liandong Monthly Plan",
		PriceAmount:        9.99,
		Currency:           "CNY",
		DurationUnit:       SubscriptionDurationMonth,
		DurationValue:      1,
		Enabled:            true,
		MaxPurchasePerUser: maxPurchasePerUser,
		TotalAmount:        1000,
	}
	require.NoError(t, DB.Create(plan).Error)
	product := &LiandongProduct{
		BusinessType:        LiandongBusinessTypeSubscription,
		Name:                "Monthly Plan",
		GoodsKey:            fmt.Sprintf("subscription-goods-%s", common.GetUUID()),
		PlanID:              plan.Id,
		ExpectedAmountMinor: 1000,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, DB.Create(product).Error)
	createResult, err := CreateLiandongOrder(
		user.Id,
		product.ID,
		"123456789012",
		testLiandongJUUID,
	)
	require.NoError(t, err)
	return user, plan, createResult.Order
}

func createLiandongQuotaOrder(t *testing.T) (*User, *LiandongOrder) {
	t.Helper()
	user, product := createLiandongQuotaFixture(t)
	result, err := CreateLiandongOrder(
		user.Id,
		product.ID,
		"123456789012",
		testLiandongJUUID,
	)
	require.NoError(t, err)
	assert.Zero(t, result.Order.NextCheckAt)
	assert.Zero(t, result.Order.CheckDeadlineAt)
	return user, result.Order
}

func claimLiandongOrderForReconcile(t *testing.T, localTradeNo string) *LiandongOrder {
	t.Helper()
	order, err := ClaimLiandongPendingOrder(localTradeNo)
	require.NoError(t, err)
	require.Positive(t, order.CheckLockUntil)
	return order
}

func TestValidLiandongContact(t *testing.T) {
	assert.True(t, validLiandongContact("123456789012"))
	assert.False(t, validLiandongContact("023456789012"))
	assert.False(t, validLiandongContact("12345678901"))
	assert.False(t, validLiandongContact("12345678901x"))
}

func TestValidateLiandongProductRejectsZeroExpectedAmount(t *testing.T) {
	product := &LiandongProduct{
		BusinessType: LiandongBusinessTypeQuota,
		Name:         "Zero Amount",
		GoodsKey:     "zero-amount",
		QuotaAmount:  100,
		Currency:     "CNY",
	}

	err := ValidateLiandongProduct(product)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "positive")
}

func TestValidateLiandongProductAcceptsPositiveCentAmounts(t *testing.T) {
	for _, expectedAmountMinor := range []int64{1, 120, 123} {
		product := &LiandongProduct{
			BusinessType:        LiandongBusinessTypeQuota,
			Name:                "Fractional Amount",
			GoodsKey:            fmt.Sprintf("fractional-amount-%d", expectedAmountMinor),
			QuotaAmount:         100,
			ExpectedAmountMinor: expectedAmountMinor,
			Currency:            "CNY",
		}

		require.NoError(t, ValidateLiandongProduct(product))
	}
}

func TestValidateLiandongProductOnlyAllowsCNYAndUSD(t *testing.T) {
	product := &LiandongProduct{
		BusinessType:        LiandongBusinessTypeQuota,
		Name:                "Unsupported Currency",
		GoodsKey:            "unsupported-currency",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "EUR",
	}

	err := ValidateLiandongProduct(product)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "CNY or USD")
}

func TestLiandongProductMigrationAddsInventoryColumnsToLegacyTable(t *testing.T) {
	legacyDB, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "liandong-legacy.db")),
		&gorm.Config{},
	)
	require.NoError(t, err)
	sqlDB, err := legacyDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, legacyDB.AutoMigrate(&legacyLiandongProduct{}))
	require.NoError(t, legacyDB.Create(&legacyLiandongProduct{
		ID:                  1,
		BusinessType:        LiandongBusinessTypeQuota,
		Name:                "Legacy quota",
		GoodsKey:            "legacy-goods",
		QuotaAmount:         500000,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}).Error)

	require.NoError(t, prepareLiandongMigrations(legacyDB))
	require.NoError(t, legacyDB.AutoMigrate(&LiandongProduct{}))
	require.NoError(t, normalizeLiandongMigrationColumns(legacyDB))

	var product LiandongProduct
	require.NoError(t, legacyDB.First(&product, 1).Error)
	assert.Equal(t, "card", product.GoodsType)
	assert.Equal(t, LiandongInventoryModeUnlimited, product.InventoryMode)
	assert.Zero(t, product.InventoryCapacity)
	assert.Zero(t, product.ThumbnailVersion)
}

func TestLiandongOrderMigrationNormalizesNewColumns(t *testing.T) {
	legacyDB, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "liandong-order-legacy.db")),
		&gorm.Config{},
	)
	require.NoError(t, err)
	sqlDB, err := legacyDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, legacyDB.AutoMigrate(&legacyLiandongOrder{}))
	require.NoError(t, legacyDB.Create(&legacyLiandongOrder{
		ID:                  1,
		LocalTradeNo:        "LD-LEGACY-ORDER",
		UserID:              1,
		ProductID:           1,
		ProductNameSnapshot: "Legacy product",
		BusinessType:        LiandongBusinessTypeQuota,
		GoodsKeySnapshot:    "legacy-product",
		ContactSnapshot:     "138001380000",
		JUUIDSnapshot:       testLiandongJUUID,
		ExpectedAmountMinor: 100,
		CurrencySnapshot:    "CNY",
		FulfillmentSnapshot: "{}",
		PaymentStatus:       LiandongPaymentStatusPending,
		FulfillmentStatus:   LiandongFulfillmentStatusWaiting,
	}).Error)

	require.NoError(t, prepareLiandongMigrations(legacyDB))
	require.NoError(t, legacyDB.AutoMigrate(&LiandongOrder{}))
	require.NoError(t, normalizeLiandongMigrationColumns(legacyDB))

	var order LiandongOrder
	require.NoError(t, legacyDB.First(&order, 1).Error)
	assert.Zero(t, order.InventoryCodeID)
	assert.Zero(t, order.ExpiresAt)
	assert.Empty(t, order.ClosedReason)
	assert.False(t, order.LatePayment)

	for _, column := range []string{
		"inventory_code_id",
		"expires_at",
		"closed_reason",
		"late_payment",
	} {
		var count int64
		require.NoError(t, legacyDB.Raw(
			"SELECT COUNT(*) FROM liandong_orders WHERE "+column+" IS NULL",
		).Scan(&count).Error)
		assert.Zero(t, count)
	}
}

func TestListLiandongOrdersSearchesExactUserID(t *testing.T) {
	truncateTables(t)
	user, order := createLiandongQuotaOrder(t)
	otherUser, _ := createLiandongQuotaFixture(t)

	orders, total, err := ListLiandongOrders(
		&common.PageInfo{Page: 1, PageSize: 10},
		strconv.Itoa(user.Id),
	)

	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, orders, 1)
	assert.Equal(t, order.LocalTradeNo, orders[0].LocalTradeNo)
	assert.NotEqual(t, otherUser.Id, orders[0].UserID)
}

func TestCreateLiandongOrderReleasesPreviousReservationWithoutClosingOrder(t *testing.T) {
	truncateTables(t)
	user, product := createLiandongQuotaFixture(t)
	product.InventoryMode = LiandongInventoryModeRedemptionCode
	product.InventoryCapacity = 1
	require.NoError(t, UpdateLiandongProduct(product))
	_, err := AddLiandongInventoryCodes(product.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)

	first, err := CreateLiandongOrder(
		user.Id,
		product.ID,
		"100000000000",
		testLiandongJUUID,
	)
	require.NoError(t, err)
	require.NotNil(t, first)
	firstProviderTradeNo := "LDREPLACED001"
	require.NoError(t, MarkLiandongCreateResult(
		first.Order.LocalTradeNo,
		&firstProviderTradeNo,
		LiandongPaymentStatusPending,
		"",
	))

	second, err := CreateLiandongOrder(
		user.Id,
		product.ID,
		"100000000001",
		testLiandongJUUID,
	)
	require.NoError(t, err)
	require.NotNil(t, second)

	var orderCount int64
	require.NoError(t, DB.Model(&LiandongOrder{}).
		Where("user_id = ? AND product_id = ?", user.Id, product.ID).
		Count(&orderCount).Error)
	assert.EqualValues(t, 2, orderCount)

	firstReloaded, err := GetLiandongOrder(first.Order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusPending, firstReloaded.PaymentStatus)
	assert.Equal(t, LiandongFulfillmentStatusWaiting, firstReloaded.FulfillmentStatus)
	assert.Empty(t, firstReloaded.ClosedReason)
	assert.Equal(t, first.Order.InventoryCodeID, firstReloaded.InventoryCodeID)

	secondReloaded, err := GetLiandongOrder(second.Order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusCreating, secondReloaded.PaymentStatus)
	assert.Equal(t, firstReloaded.InventoryCodeID, secondReloaded.InventoryCodeID)

	var firstTopUp TopUp
	require.NoError(t, DB.Where("trade_no = ?", firstReloaded.LocalTradeNo).First(&firstTopUp).Error)
	assert.Equal(t, common.TopUpStatusPending, firstTopUp.Status)

	summaries, err := GetLiandongInventorySummaries([]int{product.ID})
	require.NoError(t, err)
	assert.Zero(t, summaries[product.ID].Available)
	assert.EqualValues(t, 1, summaries[product.ID].Reserved)
	assert.Zero(t, summaries[product.ID].Consumed)
}

func TestCreateLiandongOrderReleasesAllUserReservationsWithoutChangingOldApplication(t *testing.T) {
	truncateTables(t)
	user, product := createLiandongQuotaFixture(t)
	product.InventoryMode = LiandongInventoryModeRedemptionCode
	product.InventoryCapacity = 2
	require.NoError(t, UpdateLiandongProduct(product))
	_, err := AddLiandongInventoryCodes(product.ID, 2, "", common.RoleRootUser)
	require.NoError(t, err)

	first, err := CreateLiandongOrder(
		user.Id,
		product.ID,
		"100000000010",
		testLiandongJUUID,
	)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Positive(t, first.Order.InventoryCodeID)

	const closedReason = "preserve old order state"
	require.NoError(t, DB.Model(&LiandongOrder{}).
		Where("id = ?", first.Order.ID).
		Updates(map[string]any{
			"payment_status":    LiandongPaymentStatusClosed,
			"closed_reason":     closedReason,
			"inventory_code_id": 0,
		}).Error)

	second, err := CreateLiandongOrder(
		user.Id,
		product.ID,
		"100000000011",
		testLiandongJUUID,
	)
	require.NoError(t, err)
	require.NotNil(t, second)

	firstReloaded, err := GetLiandongOrder(first.Order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusClosed, firstReloaded.PaymentStatus)
	assert.Equal(t, closedReason, firstReloaded.ClosedReason)
	assert.Zero(t, firstReloaded.InventoryCodeID)

	var firstTopUp TopUp
	require.NoError(t, DB.Where("trade_no = ?", first.Order.LocalTradeNo).First(&firstTopUp).Error)
	assert.Equal(t, common.TopUpStatusPending, firstTopUp.Status)

	var reservedCodes []LiandongProductInventoryCode
	require.NoError(t, DB.
		Where("reserved_user_id = ? AND status = ?", user.Id, LiandongInventoryStatusReserved).
		Find(&reservedCodes).Error)
	require.Len(t, reservedCodes, 1)
	assert.Equal(t, second.Order.ID, reservedCodes[0].ReservedOrderID)
	assert.Equal(t, second.Order.LocalTradeNo, reservedCodes[0].ReservedTradeNo)
}

func TestLiandongUserOperationLeaseHonorsOwnershipAndExpiry(t *testing.T) {
	truncateTables(t)
	user, _ := createLiandongQuotaFixture(t)

	firstToken, acquired, err := TryAcquireLiandongUserOperationLease(user.Id, 30)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotEmpty(t, firstToken)

	secondToken, acquired, err := TryAcquireLiandongUserOperationLease(user.Id, 30)
	require.NoError(t, err)
	assert.False(t, acquired)
	assert.Empty(t, secondToken)
	assert.ErrorIs(
		t,
		ReleaseLiandongUserOperationLease(user.Id, common.GetUUID()),
		ErrLiandongOrderBusy,
	)

	require.NoError(t, DB.Model(&LiandongUserOperationLease{}).
		Where("user_id = ?", user.Id).
		Update("expires_at", common.GetTimestamp()-1).Error)
	replacementToken, acquired, err := TryAcquireLiandongUserOperationLease(user.Id, 30)
	require.NoError(t, err)
	require.True(t, acquired)
	assert.NotEqual(t, firstToken, replacementToken)
	assert.ErrorIs(
		t,
		ReleaseLiandongUserOperationLease(user.Id, firstToken),
		ErrLiandongOrderBusy,
	)
	require.NoError(t, ReleaseLiandongUserOperationLease(user.Id, replacementToken))

	finalToken, acquired, err := TryAcquireLiandongUserOperationLease(user.Id, 30)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, ReleaseLiandongUserOperationLease(user.Id, finalToken))
}

func TestCreateLiandongOrderConcurrentRequestsDoNotOversellInventory(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "liandong-concurrency.db")
	concurrentDB, err := gorm.Open(
		sqlite.Open(databasePath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"),
		&gorm.Config{},
	)
	require.NoError(t, err)
	require.NoError(t, concurrentDB.AutoMigrate(
		&User{},
		&TopUp{},
		&SubscriptionPlan{},
		&SubscriptionOrder{},
		&UserSubscription{},
		&LiandongProduct{},
		&LiandongOrder{},
		&LiandongProductInventoryCode{},
		&LiandongUserOperationLease{},
	))
	sqlDB, err := concurrentDB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)

	originalDB := DB
	DB = concurrentDB
	t.Cleanup(func() {
		DB = originalDB
		require.NoError(t, sqlDB.Close())
	})

	user, product := createLiandongQuotaFixture(t)
	product.InventoryMode = LiandongInventoryModeRedemptionCode
	product.InventoryCapacity = 1
	require.NoError(t, UpdateLiandongProduct(product))
	_, err = AddLiandongInventoryCodes(product.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)

	const workerCount = 8
	start := make(chan struct{})
	results := make(chan error, workerCount)
	var waitGroup sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			<-start
			_, createErr := CreateLiandongOrder(
				user.Id,
				product.ID,
				fmt.Sprintf("%012d", 900000000000+worker),
				testLiandongJUUID,
			)
			results <- createErr
		}(index)
	}
	close(start)
	waitGroup.Wait()
	close(results)

	successCount := 0
	for createErr := range results {
		if createErr == nil {
			successCount++
		}
	}
	require.GreaterOrEqual(t, successCount, 1)

	var activeOrderCount int64
	require.NoError(t, DB.Model(&LiandongOrder{}).
		Where("user_id = ? AND payment_status IN ?", user.Id, []string{
			LiandongPaymentStatusCreating,
			LiandongPaymentStatusPending,
			LiandongPaymentStatusCreateUnknown,
		}).
		Count(&activeOrderCount).Error)
	assert.EqualValues(t, successCount, activeOrderCount)

	var modifiedOrderCount int64
	require.NoError(t, DB.Model(&LiandongOrder{}).
		Where("user_id = ? AND closed_reason <> ?", user.Id, "").
		Count(&modifiedOrderCount).Error)
	assert.Zero(t, modifiedOrderCount)

	summaries, err := GetLiandongInventorySummaries([]int{product.ID})
	require.NoError(t, err)
	assert.Zero(t, summaries[product.ID].Available)
	assert.EqualValues(t, 1, summaries[product.ID].Reserved)
}

func TestCreateLiandongOrderSupportsInventoryModesAcrossBusinessTypes(t *testing.T) {
	tests := []struct {
		name          string
		businessType  string
		inventoryMode string
	}{
		{
			name:          "quota unlimited",
			businessType:  LiandongBusinessTypeQuota,
			inventoryMode: LiandongInventoryModeUnlimited,
		},
		{
			name:          "quota redemption code",
			businessType:  LiandongBusinessTypeQuota,
			inventoryMode: LiandongInventoryModeRedemptionCode,
		},
		{
			name:          "subscription unlimited",
			businessType:  LiandongBusinessTypeSubscription,
			inventoryMode: LiandongInventoryModeUnlimited,
		},
		{
			name:          "subscription redemption code",
			businessType:  LiandongBusinessTypeSubscription,
			inventoryMode: LiandongInventoryModeRedemptionCode,
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)
			user := &User{
				Username: fmt.Sprintf("liandong-inventory-mode-%d-%s", index, common.GetUUID()),
				Password: "password",
				Status:   common.UserStatusEnabled,
				Role:     common.RoleCommonUser,
				Group:    "default",
				AffCode:  common.GetRandomString(16),
			}
			require.NoError(t, DB.Create(user).Error)

			product := &LiandongProduct{
				BusinessType:        test.businessType,
				GoodsType:           "card",
				Name:                test.name,
				GoodsKey:            fmt.Sprintf("inventory-mode-%d-%s", index, common.GetUUID()),
				ExpectedAmountMinor: 100,
				Currency:            "CNY",
				InventoryMode:       test.inventoryMode,
				Enabled:             true,
			}
			if test.businessType == LiandongBusinessTypeQuota {
				product.QuotaAmount = 100
			} else {
				plan := &SubscriptionPlan{
					Title:         test.name,
					PriceAmount:   1,
					Currency:      "CNY",
					DurationUnit:  SubscriptionDurationMonth,
					DurationValue: 1,
					Enabled:       true,
					TotalAmount:   100,
				}
				require.NoError(t, DB.Create(plan).Error)
				product.PlanID = plan.Id
			}
			if test.inventoryMode == LiandongInventoryModeRedemptionCode {
				product.InventoryCapacity = 1
			}
			require.NoError(t, ValidateLiandongProduct(product))
			require.NoError(t, DB.Create(product).Error)
			if test.inventoryMode == LiandongInventoryModeRedemptionCode {
				_, err := AddLiandongInventoryCodes(product.ID, 1, "", common.RoleRootUser)
				require.NoError(t, err)
			}

			result, err := CreateLiandongOrder(
				user.Id,
				product.ID,
				fmt.Sprintf("%012d", 500000000000+index),
				testLiandongJUUID,
			)

			require.NoError(t, err)
			if test.inventoryMode == LiandongInventoryModeUnlimited {
				assert.Zero(t, result.Order.InventoryCodeID)
				return
			}
			assert.Positive(t, result.Order.InventoryCodeID)
			summaries, err := GetLiandongInventorySummaries([]int{product.ID})
			require.NoError(t, err)
			assert.Zero(t, summaries[product.ID].Available)
			assert.EqualValues(t, 1, summaries[product.ID].Reserved)
		})
	}
}

func TestFindPendingLiandongOrdersUsesFairGlobalBatch(t *testing.T) {
	truncateTables(t)

	expected := make([]LiandongOrder, 0, 105)
	for index := 0; index < 105; index++ {
		providerTradeNo := fmt.Sprintf("LDFAIR%03d", index)
		order := LiandongOrder{
			LocalTradeNo:      fmt.Sprintf("LDLOCAL%03d", index),
			BusinessType:      LiandongBusinessTypeQuota,
			ProviderTradeNo:   &providerTradeNo,
			ContactSnapshot:   fmt.Sprintf("%012d", 400000000000+index),
			PaymentStatus:     LiandongPaymentStatusPending,
			FulfillmentStatus: LiandongFulfillmentStatusWaiting,
			LastCheckAt:       int64((104 - index) / 2),
		}
		require.NoError(t, DB.Create(&order).Error)
		expected = append(expected, order)
	}
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].LastCheckAt == expected[j].LastCheckAt {
			return expected[i].ID < expected[j].ID
		}
		return expected[i].LastCheckAt < expected[j].LastCheckAt
	})

	orders, err := FindPendingLiandongOrders(1000)

	require.NoError(t, err)
	require.Len(t, orders, 100)
	for index, order := range orders {
		assert.Equal(t, expected[index].LocalTradeNo, order.LocalTradeNo)
	}
}

func TestHasLiandongWorkKeepsFutureFulfillmentRetryScheduled(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	order := &LiandongOrder{
		LocalTradeNo:      "LDFULFILLDUE001",
		BusinessType:      LiandongBusinessTypeQuota,
		PaymentStatus:     LiandongPaymentStatusPaid,
		FulfillmentStatus: LiandongFulfillmentStatusWaiting,
		NextCheckAt:       now + 60,
	}
	require.NoError(t, DB.Create(order).Error)

	assert.True(t, HasLiandongWork(false, true, false))
	dueOrders, err := FindDuePaidLiandongOrders(100)
	require.NoError(t, err)
	assert.Empty(t, dueOrders)

	require.NoError(t, DB.Model(order).Update("next_check_at", now).Error)
	dueOrders, err = FindDuePaidLiandongOrders(100)
	require.NoError(t, err)
	require.Len(t, dueOrders, 1)
	assert.Equal(t, order.LocalTradeNo, dueOrders[0].LocalTradeNo)
}

func TestHasLiandongWorkKeepsClosedProviderOrderScheduledForLatePaymentDetection(t *testing.T) {
	truncateTables(t)
	providerTradeNo := "LDLATEWORK001"
	order := &LiandongOrder{
		LocalTradeNo:      "LDLATELOCAL001",
		BusinessType:      LiandongBusinessTypeQuota,
		ProviderTradeNo:   &providerTradeNo,
		PaymentStatus:     LiandongPaymentStatusClosed,
		FulfillmentStatus: LiandongFulfillmentStatusFailed,
		ClosedReason:      "payment timeout",
	}
	require.NoError(t, DB.Create(order).Error)

	assert.True(t, HasLiandongWork(true, false, true))

	require.NoError(t, DB.Model(order).Updates(map[string]any{
		"late_payment":       true,
		"fulfillment_status": LiandongFulfillmentStatusReviewRequired,
		"paid_at":            common.GetTimestamp(),
	}).Error)
	assert.False(t, HasLiandongWork(true, false, true))
}

func TestMarkLiandongCreateFailureClosesApplication(t *testing.T) {
	truncateTables(t)
	_, order := createLiandongQuotaOrder(t)

	require.NoError(t, MarkLiandongCreateFailure(
		order.LocalTradeNo,
		LiandongPaymentStatusCreateFailed,
		"provider rejected request",
	))

	reloaded, err := GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusCreateFailed, reloaded.PaymentStatus)
	assert.Equal(t, LiandongFulfillmentStatusFailed, reloaded.FulfillmentStatus)

	var topUp TopUp
	require.NoError(t, DB.Where("trade_no = ?", order.LocalTradeNo).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusFailed, topUp.Status)
}

func TestMarkLiandongCreatePersistenceFailureHandlesDuplicateProviderTradeNo(t *testing.T) {
	truncateTables(t)
	firstUser, product := createLiandongQuotaFixture(t)
	secondUser := &User{
		Username: fmt.Sprintf("liandong-duplicate-user-%s", common.GetUUID()),
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, DB.Create(secondUser).Error)

	first, err := CreateLiandongOrder(
		firstUser.Id,
		product.ID,
		"123456789012",
		testLiandongJUUID,
	)
	require.NoError(t, err)
	second, err := CreateLiandongOrder(
		secondUser.Id,
		product.ID,
		"223456789012",
		testLiandongJUUID,
	)
	require.NoError(t, err)

	providerTradeNo := "LDDUPLICATE123"
	require.NoError(t, MarkLiandongCreateResult(
		first.Order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))

	require.NoError(t, MarkLiandongCreatePersistenceFailure(
		second.Order.LocalTradeNo,
		providerTradeNo,
		"initial persistence failed",
	))

	reloaded, err := GetLiandongOrder(second.Order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusCreateUnknown, reloaded.PaymentStatus)
	assert.Equal(t, LiandongFulfillmentStatusReviewRequired, reloaded.FulfillmentStatus)
	assert.Nil(t, reloaded.ProviderTradeNo)
	assert.Contains(t, reloaded.LastError, providerTradeNo)
}

func TestRequeueLiandongCreateUnknownWithProviderTradeNo(t *testing.T) {
	truncateTables(t)
	_, order := createLiandongQuotaOrder(t)
	providerTradeNo := "LDPROVIDER123"
	require.NoError(t, MarkLiandongCreatePersistenceFailure(
		order.LocalTradeNo,
		providerTradeNo,
		"local persistence failed",
	))

	require.NoError(t, RequeueLiandongOrder(order.LocalTradeNo))

	reloaded, err := GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusPending, reloaded.PaymentStatus)
	assert.Equal(t, LiandongFulfillmentStatusWaiting, reloaded.FulfillmentStatus)
	assert.Equal(t, providerTradeNo, *reloaded.ProviderTradeNo)
	assert.Zero(t, reloaded.NextCheckAt)
}

func TestRequeueLiandongOrderRejectsActiveCheckLock(t *testing.T) {
	truncateTables(t)
	_, order := createLiandongQuotaOrder(t)
	providerTradeNo := "LDLOCKED123"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	require.NoError(t, DB.Model(&LiandongOrder{}).
		Where("local_trade_no = ?", order.LocalTradeNo).
		Update("check_lock_until", common.GetTimestamp()+60).Error)

	err := RequeueLiandongOrder(order.LocalTradeNo)

	assert.ErrorIs(t, err, ErrLiandongOrderBusy)
}

func TestRequeueLiandongOrderAllowsAnotherActiveOrderForUser(t *testing.T) {
	truncateTables(t)
	user, firstProduct := createLiandongQuotaFixture(t)
	firstResult, err := CreateLiandongOrder(
		user.Id,
		firstProduct.ID,
		"123456789012",
		testLiandongJUUID,
	)
	require.NoError(t, err)
	firstProviderTradeNo := "LDREQUEUEFIRST"
	require.NoError(t, MarkLiandongCreateResult(
		firstResult.Order.LocalTradeNo,
		&firstProviderTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	require.NoError(t, CloseLiandongOrder(firstResult.Order.LocalTradeNo))

	secondProduct := &LiandongProduct{
		BusinessType:        LiandongBusinessTypeQuota,
		Name:                "Quota 200",
		GoodsKey:            fmt.Sprintf("goods-%s", common.GetUUID()),
		QuotaAmount:         200,
		ExpectedAmountMinor: 200,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, DB.Create(secondProduct).Error)
	secondResult, err := CreateLiandongOrder(
		user.Id,
		secondProduct.ID,
		"223456789012",
		testLiandongJUUID,
	)
	require.NoError(t, err)

	err = RequeueLiandongOrder(firstResult.Order.LocalTradeNo)

	require.NoError(t, err)
	firstReloaded, reloadErr := GetLiandongOrder(firstResult.Order.LocalTradeNo)
	require.NoError(t, reloadErr)
	assert.Equal(t, LiandongPaymentStatusPending, firstReloaded.PaymentStatus)
	secondReloaded, reloadErr := GetLiandongOrder(secondResult.Order.LocalTradeNo)
	require.NoError(t, reloadErr)
	assert.Equal(t, LiandongPaymentStatusCreating, secondReloaded.PaymentStatus)
}

func TestApplyLiandongPaidTradeNoMarksReplacedOrderLateWithoutConsumingInventory(t *testing.T) {
	truncateTables(t)
	user, product := createLiandongQuotaFixture(t)
	product.InventoryMode = LiandongInventoryModeRedemptionCode
	product.InventoryCapacity = 1
	require.NoError(t, UpdateLiandongProduct(product))
	_, err := AddLiandongInventoryCodes(product.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)

	first, err := CreateLiandongOrder(
		user.Id,
		product.ID,
		"123456789012",
		testLiandongJUUID,
	)
	require.NoError(t, err)
	providerTradeNo := "LDREPLACEDLATE001"
	require.NoError(t, MarkLiandongCreateResult(
		first.Order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	second, err := CreateLiandongOrder(
		user.Id,
		product.ID,
		"223456789012",
		testLiandongJUUID,
	)
	require.NoError(t, err)

	transition, err := ApplyLiandongPaidTradeNo(
		providerTradeNo,
		`{"trade_no":"LDREPLACEDLATE001","status":1}`,
	)

	require.NoError(t, err)
	require.NotNil(t, transition)
	assert.True(t, transition.Late)
	assert.False(t, transition.NewlyPaid)

	firstReloaded, err := GetLiandongOrder(first.Order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusPending, firstReloaded.PaymentStatus)
	assert.Equal(t, LiandongFulfillmentStatusReviewRequired, firstReloaded.FulfillmentStatus)
	assert.Empty(t, firstReloaded.ClosedReason)
	assert.True(t, firstReloaded.LatePayment)
	assert.Positive(t, firstReloaded.PaidAt)
	assert.Zero(t, firstReloaded.NextCheckAt)

	secondReloaded, err := GetLiandongOrder(second.Order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusCreating, secondReloaded.PaymentStatus)

	summaries, err := GetLiandongInventorySummaries([]int{product.ID})
	require.NoError(t, err)
	assert.Zero(t, summaries[product.ID].Available)
	assert.EqualValues(t, 1, summaries[product.ID].Reserved)
	assert.Zero(t, summaries[product.ID].Consumed)

	var reloadedUser User
	require.NoError(t, DB.First(&reloadedUser, user.Id).Error)
	assert.Zero(t, reloadedUser.Quota)
	var topUp TopUp
	require.NoError(t, DB.Where("trade_no = ?", firstReloaded.LocalTradeNo).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
}

func TestCloseLiandongOrderFailsApplicationAndCannotBeReopenedByCheck(t *testing.T) {
	truncateTables(t)
	_, order := createLiandongQuotaOrder(t)
	providerTradeNo := "LDPROVIDER456"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))

	require.NoError(t, CloseLiandongOrder(order.LocalTradeNo))
	err := CompleteLiandongOrderCheck(
		order.LocalTradeNo,
		1,
		LiandongPaymentStatusPaid,
		`{"trade_no":"LDPROVIDER456","status":1}`,
	)
	assert.ErrorIs(t, err, ErrLiandongOrderBusy)

	reloaded, getErr := GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, getErr)
	assert.Equal(t, LiandongPaymentStatusClosed, reloaded.PaymentStatus)
	assert.Zero(t, reloaded.PaidAt)

	var topUp TopUp
	require.NoError(t, DB.Where("trade_no = ?", order.LocalTradeNo).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusFailed, topUp.Status)
}

func TestCompleteLiandongOrderCheckRejectsStaleClaim(t *testing.T) {
	truncateTables(t)
	_, order := createLiandongQuotaOrder(t)
	providerTradeNo := "LDSTALECLAIM123"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))

	staleLockUntil := common.GetTimestamp() - 1
	require.NoError(t, DB.Model(&LiandongOrder{}).
		Where("local_trade_no = ?", order.LocalTradeNo).
		Update("check_lock_until", staleLockUntil).Error)
	currentClaim := claimLiandongOrderForReconcile(t, order.LocalTradeNo)
	require.NotEqual(t, staleLockUntil, currentClaim.CheckLockUntil)

	err := CompleteLiandongOrderCheck(
		order.LocalTradeNo,
		staleLockUntil,
		LiandongPaymentStatusPaid,
		`{"trade_no":"LDSTALECLAIM123","status":1}`,
	)
	assert.ErrorIs(t, err, ErrLiandongOrderBusy)

	reloaded, getErr := GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, getErr)
	assert.Equal(t, LiandongPaymentStatusPending, reloaded.PaymentStatus)
	assert.Equal(t, currentClaim.CheckLockUntil, reloaded.CheckLockUntil)

	require.NoError(t, CompleteLiandongOrderCheck(
		order.LocalTradeNo,
		currentClaim.CheckLockUntil,
		LiandongPaymentStatusPaid,
		`{"trade_no":"LDSTALECLAIM123","status":1}`,
	))
	reloaded, getErr = GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, getErr)
	assert.Equal(t, LiandongPaymentStatusPaid, reloaded.PaymentStatus)
	assert.Zero(t, reloaded.CheckLockUntil)
}

func TestApplyClaimedLiandongPaidTradeNoRejectsAnotherCheckClaim(t *testing.T) {
	truncateTables(t)
	_, order := createLiandongQuotaOrder(t)
	providerTradeNo := "LDSHAREDCHECKLOCK"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	exactClaim := claimLiandongOrderForReconcile(t, order.LocalTradeNo)

	_, err := ClaimLiandongOrderByProviderTradeNo(providerTradeNo)
	assert.ErrorIs(t, err, ErrLiandongOrderBusy)
	_, err = ApplyClaimedLiandongPaidTradeNo(
		providerTradeNo,
		exactClaim.CheckLockUntil-1,
		`{"trade_no":"LDSHAREDCHECKLOCK","status":1}`,
	)
	assert.ErrorIs(t, err, ErrLiandongOrderBusy)

	reloaded, reloadErr := GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, reloadErr)
	assert.Equal(t, LiandongPaymentStatusPending, reloaded.PaymentStatus)
	assert.Equal(t, exactClaim.CheckLockUntil, reloaded.CheckLockUntil)

	transition, err := ApplyClaimedLiandongPaidTradeNo(
		providerTradeNo,
		exactClaim.CheckLockUntil,
		`{"trade_no":"LDSHAREDCHECKLOCK","status":1}`,
	)
	require.NoError(t, err)
	assert.True(t, transition.NewlyPaid)
}

func TestRequeueClosedLiandongOrderRestoresQuotaApplication(t *testing.T) {
	truncateTables(t)
	_, order := createLiandongQuotaOrder(t)
	providerTradeNo := "LDPROVIDERRESTORE"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	require.NoError(t, CloseLiandongOrder(order.LocalTradeNo))

	require.NoError(t, RequeueLiandongOrder(order.LocalTradeNo))

	reloaded, err := GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusPending, reloaded.PaymentStatus)
	assert.Equal(t, LiandongFulfillmentStatusWaiting, reloaded.FulfillmentStatus)
	assert.Zero(t, reloaded.NextCheckAt)

	var topUp TopUp
	require.NoError(t, DB.Where("trade_no = ?", order.LocalTradeNo).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
	assert.Zero(t, topUp.CompleteTime)
}

func TestRequeueClosedLiandongOrderRenewsExpiryAndClearsCloseState(t *testing.T) {
	truncateTables(t)
	_, order := createLiandongQuotaOrder(t)
	providerTradeNo := "LDREQUEUEEXPIRY"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	require.NoError(t, DB.Model(&LiandongOrder{}).
		Where("id = ?", order.ID).
		Update("expires_at", common.GetTimestamp()-60).Error)
	require.NoError(t, CloseLiandongOrderForUser(
		order.UserID,
		order.LocalTradeNo,
		"payment timeout",
	))
	before := common.GetTimestamp()

	require.NoError(t, RequeueLiandongOrderWithTimeout(order.LocalTradeNo, 9))

	reloaded, err := GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusPending, reloaded.PaymentStatus)
	assert.Equal(t, LiandongFulfillmentStatusWaiting, reloaded.FulfillmentStatus)
	assert.Empty(t, reloaded.ClosedReason)
	assert.False(t, reloaded.LatePayment)
	assert.Zero(t, reloaded.PaidAt)
	assert.GreaterOrEqual(t, reloaded.ExpiresAt, before+9*60)
	assert.LessOrEqual(t, reloaded.ExpiresAt, common.GetTimestamp()+9*60)
}

func TestRequeueClosedLiandongOrderRestoresSubscriptionApplication(t *testing.T) {
	truncateTables(t)
	_, _, order := createLiandongSubscriptionOrder(t, 0)
	providerTradeNo := "LDSUBRESTORE"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	require.NoError(t, CloseLiandongOrder(order.LocalTradeNo))

	var closedApplication SubscriptionOrder
	require.NoError(t, DB.Where("trade_no = ?", order.LocalTradeNo).First(&closedApplication).Error)
	assert.Equal(t, common.TopUpStatusFailed, closedApplication.Status)
	assert.Greater(t, closedApplication.CompleteTime, int64(0))

	require.NoError(t, RequeueLiandongOrder(order.LocalTradeNo))

	reloaded, err := GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusPending, reloaded.PaymentStatus)
	assert.Equal(t, LiandongFulfillmentStatusWaiting, reloaded.FulfillmentStatus)

	var restoredApplication SubscriptionOrder
	require.NoError(t, DB.Where("trade_no = ?", order.LocalTradeNo).First(&restoredApplication).Error)
	assert.Equal(t, common.TopUpStatusPending, restoredApplication.Status)
	assert.Zero(t, restoredApplication.CompleteTime)
}

func TestRequeueClosedFiniteInventoryOrderRollsBackWhenStockIsUnavailable(t *testing.T) {
	truncateTables(t)
	user, product := createLiandongQuotaFixture(t)
	product.InventoryMode = LiandongInventoryModeRedemptionCode
	product.InventoryCapacity = 1
	require.NoError(t, UpdateLiandongProduct(product))
	_, err := AddLiandongInventoryCodes(product.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)
	createResult, err := CreateLiandongOrder(
		user.Id,
		product.ID,
		"123456789012",
		testLiandongJUUID,
	)
	require.NoError(t, err)
	providerTradeNo := "LDREQUEUEOUTOFSTOCK"
	require.NoError(t, MarkLiandongCreateResult(
		createResult.Order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	require.NoError(t, CloseLiandongOrder(createResult.Order.LocalTradeNo))
	require.NoError(t, DisableLiandongAvailableInventoryCodes(product.ID, 1))

	err = RequeueLiandongOrder(createResult.Order.LocalTradeNo)

	require.ErrorIs(t, err, ErrLiandongInventoryUnavailable)
	reloaded, reloadErr := GetLiandongOrder(createResult.Order.LocalTradeNo)
	require.NoError(t, reloadErr)
	assert.Equal(t, LiandongPaymentStatusClosed, reloaded.PaymentStatus)
	assert.Equal(t, LiandongFulfillmentStatusFailed, reloaded.FulfillmentStatus)
	var topUp TopUp
	require.NoError(t, DB.Where("trade_no = ?", reloaded.LocalTradeNo).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusFailed, topUp.Status)
	summaries, summaryErr := GetLiandongInventorySummaries([]int{product.ID})
	require.NoError(t, summaryErr)
	assert.Zero(t, summaries[product.ID].Available)
	assert.Zero(t, summaries[product.ID].Reserved)
	assert.EqualValues(t, 1, summaries[product.ID].Disabled)
}

func TestRequeueClosedLiandongOrderRequiresProviderTradeNo(t *testing.T) {
	truncateTables(t)
	_, order := createLiandongQuotaOrder(t)
	require.NoError(t, CloseLiandongOrder(order.LocalTradeNo))

	err := RequeueLiandongOrder(order.LocalTradeNo)

	assert.ErrorIs(t, err, ErrLiandongOrderNotFound)
	var topUp TopUp
	require.NoError(t, DB.Where("trade_no = ?", order.LocalTradeNo).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusFailed, topUp.Status)
}

func TestCloseLiandongOrderRejectsPaidOrder(t *testing.T) {
	truncateTables(t)
	_, order := createLiandongQuotaOrder(t)
	providerTradeNo := "LDPROVIDER789"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	claimedOrder := claimLiandongOrderForReconcile(t, order.LocalTradeNo)
	require.NoError(t, CompleteLiandongOrderCheck(
		order.LocalTradeNo,
		claimedOrder.CheckLockUntil,
		LiandongPaymentStatusPaid,
		`{"trade_no":"LDPROVIDER789","status":1}`,
	))

	err := CloseLiandongOrder(order.LocalTradeNo)
	assert.True(t, errors.Is(err, ErrLiandongOrderNotFound) || errors.Is(err, ErrLiandongOrderBusy))
}

func TestFulfillLiandongQuotaOrderIsIdempotent(t *testing.T) {
	truncateTables(t)
	user, order := createLiandongQuotaOrder(t)
	providerTradeNo := "LDPROVIDER999"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	claimedOrder := claimLiandongOrderForReconcile(t, order.LocalTradeNo)
	require.NoError(t, CompleteLiandongOrderCheck(
		order.LocalTradeNo,
		claimedOrder.CheckLockUntil,
		LiandongPaymentStatusPaid,
		`{"trade_no":"LDPROVIDER999","status":1}`,
	))

	first, err := FulfillLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.EqualValues(t, 100, first.QuotaAdded)
	second, err := FulfillLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.True(t, second.AlreadyFulfilled)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, 100, reloaded.Quota)
}

func TestApplyLiandongPaidTradeNoAcceptsExpiredButOpenOrder(t *testing.T) {
	truncateTables(t)
	user, product := createLiandongQuotaFixture(t)
	product.InventoryMode = LiandongInventoryModeRedemptionCode
	product.InventoryCapacity = 1
	require.NoError(t, UpdateLiandongProduct(product))
	_, err := AddLiandongInventoryCodes(product.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)
	createResult, err := CreateLiandongOrderWithTimeout(
		user.Id,
		product.ID,
		"123456789012",
		testLiandongJUUID,
		1,
	)
	require.NoError(t, err)
	providerTradeNo := "LDLATEPAYMENT001"
	require.NoError(t, MarkLiandongCreateResult(
		createResult.Order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	require.NoError(t, DB.Model(&LiandongOrder{}).
		Where("id = ?", createResult.Order.ID).
		Update("expires_at", common.GetTimestamp()-1).Error)

	transition, err := ApplyLiandongPaidTradeNo(
		providerTradeNo,
		`{"trade_no":"LDLATEPAYMENT001","status":1}`,
	)

	require.NoError(t, err)
	require.NotNil(t, transition)
	assert.False(t, transition.Late)
	assert.True(t, transition.NewlyPaid)
	reloaded, err := GetLiandongOrder(createResult.Order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusPaid, reloaded.PaymentStatus)
	assert.Equal(t, LiandongFulfillmentStatusWaiting, reloaded.FulfillmentStatus)
	assert.False(t, reloaded.LatePayment)
	assert.Positive(t, reloaded.PaidAt)

	summaries, err := GetLiandongInventorySummaries([]int{product.ID})
	require.NoError(t, err)
	assert.Zero(t, summaries[product.ID].Available)
	assert.Zero(t, summaries[product.ID].Reserved)
	assert.EqualValues(t, 1, summaries[product.ID].Consumed)

	var reloadedUser User
	require.NoError(t, DB.First(&reloadedUser, user.Id).Error)
	assert.Zero(t, reloadedUser.Quota)
	var topUp TopUp
	require.NoError(t, DB.Where("trade_no = ?", reloaded.LocalTradeNo).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
}

func TestApplyLiandongPaidTradeNoMarksClosedOrderLate(t *testing.T) {
	truncateTables(t)
	user, product := createLiandongQuotaFixture(t)
	product.InventoryMode = LiandongInventoryModeRedemptionCode
	product.InventoryCapacity = 1
	require.NoError(t, UpdateLiandongProduct(product))
	_, err := AddLiandongInventoryCodes(product.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)
	createResult, err := CreateLiandongOrderWithTimeout(
		user.Id,
		product.ID,
		"123456789012",
		testLiandongJUUID,
		1,
	)
	require.NoError(t, err)
	providerTradeNo := "LDLATEPAYMENT002"
	require.NoError(t, MarkLiandongCreateResult(
		createResult.Order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	require.NoError(t, CloseLiandongOrderForUser(
		user.Id,
		createResult.Order.LocalTradeNo,
		"payment timeout",
	))

	transition, err := ApplyLiandongPaidTradeNo(
		providerTradeNo,
		`{"trade_no":"LDLATEPAYMENT002","status":1}`,
	)

	require.NoError(t, err)
	require.NotNil(t, transition)
	assert.True(t, transition.Late)
	assert.False(t, transition.NewlyPaid)
	reloaded, err := GetLiandongOrder(createResult.Order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongPaymentStatusClosed, reloaded.PaymentStatus)
	assert.Equal(t, LiandongFulfillmentStatusReviewRequired, reloaded.FulfillmentStatus)
	assert.True(t, reloaded.LatePayment)
	assert.Positive(t, reloaded.PaidAt)

	summaries, err := GetLiandongInventorySummaries([]int{product.ID})
	require.NoError(t, err)
	assert.EqualValues(t, 1, summaries[product.ID].Available)
	assert.Zero(t, summaries[product.ID].Reserved)
	assert.Zero(t, summaries[product.ID].Consumed)

	var reloadedUser User
	require.NoError(t, DB.First(&reloadedUser, user.Id).Error)
	assert.Zero(t, reloadedUser.Quota)
	var topUp TopUp
	require.NoError(t, DB.Where("trade_no = ?", reloaded.LocalTradeNo).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusFailed, topUp.Status)
}

func TestFulfillLiandongSubscriptionOrderIsIdempotent(t *testing.T) {
	truncateTables(t)
	user, plan, order := createLiandongSubscriptionOrder(t, 0)
	providerTradeNo := "LDSUBIDEMPOTENT"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	claimedOrder := claimLiandongOrderForReconcile(t, order.LocalTradeNo)
	require.NoError(t, CompleteLiandongOrderCheck(
		order.LocalTradeNo,
		claimedOrder.CheckLockUntil,
		LiandongPaymentStatusPaid,
		`{"trade_no":"LDSUBIDEMPOTENT","status":1}`,
	))

	first, err := FulfillLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.False(t, first.AlreadyFulfilled)
	second, err := FulfillLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.True(t, second.AlreadyFulfilled)

	var subscriptions []UserSubscription
	require.NoError(t, DB.
		Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).
		Find(&subscriptions).Error)
	require.Len(t, subscriptions, 1)
	assert.Equal(t, PaymentMethodLiandong, subscriptions[0].Source)

	var subscriptionOrder SubscriptionOrder
	require.NoError(t, DB.Where("trade_no = ?", order.LocalTradeNo).First(&subscriptionOrder).Error)
	assert.Equal(t, common.TopUpStatusSuccess, subscriptionOrder.Status)

	var topUp TopUp
	require.NoError(t, DB.Where("trade_no = ?", order.LocalTradeNo).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusSuccess, topUp.Status)

	reloaded, err := GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, LiandongFulfillmentStatusFulfilled, reloaded.FulfillmentStatus)
}

func TestRequeueLiandongOrderRejectsFulfilledOrder(t *testing.T) {
	truncateTables(t)
	_, order := createLiandongQuotaOrder(t)
	providerTradeNo := "LDFULFILLED123"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	claimedOrder := claimLiandongOrderForReconcile(t, order.LocalTradeNo)
	require.NoError(t, CompleteLiandongOrderCheck(
		order.LocalTradeNo,
		claimedOrder.CheckLockUntil,
		LiandongPaymentStatusPaid,
		`{"trade_no":"LDFULFILLED123","status":1}`,
	))
	_, err := FulfillLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)

	err = RequeueLiandongOrder(order.LocalTradeNo)

	assert.ErrorIs(t, err, ErrLiandongOrderNotFound)
}

func TestUpdateOptionMapDoesNotCacheLiandongMerchantToken(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = map[string]string{
		"LiandongMerchantToken": "stale-secret-token",
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	require.NoError(t, updateOptionMap("LiandongMerchantToken", "super-secret-token"))

	common.OptionMapRWMutex.RLock()
	_, cached := common.OptionMap["LiandongMerchantToken"]
	common.OptionMapRWMutex.RUnlock()
	assert.False(t, cached)
}

func TestUpdateOptionsBulkDoesNotLogLiandongMerchantToken(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Option{}))

	originalDB := DB
	recorder := &liandongRecordingGormLogger{}
	DB = DB.Session(&gorm.Session{Logger: recorder})
	t.Cleanup(func() {
		DB = originalDB
		originalDB.Where("key IN ?", []string{
			"LiandongMerchantToken",
			"LiandongPollIntervalSeconds",
		}).Delete(&Option{})
	})

	secret := "merchant-token-must-not-appear"
	visibleValue := "visible-poll-value"
	require.NoError(t, UpdateOptionsBulk(map[string]string{
		"LiandongMerchantToken":       secret,
		"LiandongPollIntervalSeconds": visibleValue,
	}))

	loggedStatements := recorder.statements.String()
	assert.Contains(t, loggedStatements, visibleValue)
	assert.NotContains(t, loggedStatements, secret)
}

func TestDisableLiandongInventoryRejectsOversizedBatchWithoutMutation(t *testing.T) {
	truncateTables(t)
	_, product := createLiandongQuotaFixture(t)
	product.InventoryMode = LiandongInventoryModeRedemptionCode
	product.InventoryCapacity = 2
	require.NoError(t, UpdateLiandongProduct(product))
	_, err := AddLiandongInventoryCodes(product.ID, 2, "", common.RoleRootUser)
	require.NoError(t, err)

	err = DisableLiandongAvailableInventoryCodes(
		product.ID,
		LiandongInventoryBatchLimit+1,
	)

	require.Error(t, err)
	summaries, summaryErr := GetLiandongInventorySummaries([]int{product.ID})
	require.NoError(t, summaryErr)
	assert.EqualValues(t, 2, summaries[product.ID].Available)
	assert.Zero(t, summaries[product.ID].Reserved)
	assert.Zero(t, summaries[product.ID].Disabled)
}

func TestFulfillLiandongSubscriptionEnforcesPurchaseLimit(t *testing.T) {
	truncateTables(t)
	user, plan, order := createLiandongSubscriptionOrder(t, 1)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := CreateUserSubscriptionFromPlanTx(tx, user.Id, plan, "existing")
		return err
	}))

	providerTradeNo := "LDSUBPAID"
	require.NoError(t, MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		LiandongPaymentStatusPending,
		"",
	))
	claimedOrder := claimLiandongOrderForReconcile(t, order.LocalTradeNo)
	require.NoError(t, CompleteLiandongOrderCheck(
		order.LocalTradeNo,
		claimedOrder.CheckLockUntil,
		LiandongPaymentStatusPaid,
		`{"trade_no":"LDSUBPAID","status":1}`,
	))

	result, err := FulfillLiandongOrder(order.LocalTradeNo)

	require.ErrorIs(t, err, ErrSubscriptionPurchaseLimit)
	assert.Nil(t, result)
	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).
		Count(&subscriptionCount).Error)
	assert.EqualValues(t, 1, subscriptionCount)

	var subscriptionOrder SubscriptionOrder
	require.NoError(t, DB.Where("trade_no = ?", order.LocalTradeNo).First(&subscriptionOrder).Error)
	assert.Equal(t, common.TopUpStatusPending, subscriptionOrder.Status)

	reloaded, getErr := GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, getErr)
	assert.Equal(t, LiandongFulfillmentStatusWaiting, reloaded.FulfillmentStatus)
}
