package model

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	LiandongBusinessTypeQuota        = "quota"
	LiandongBusinessTypeSubscription = "subscription"

	LiandongInventoryModeUnlimited      = "unlimited"
	LiandongInventoryModeRedemptionCode = "redemption_code"

	LiandongInventoryStatusAvailable = "available"
	LiandongInventoryStatusReserved  = "reserved"
	LiandongInventoryStatusConsumed  = "consumed"
	LiandongInventoryStatusDisabled  = "disabled"

	LiandongPaymentStatusCreating       = "creating"
	LiandongPaymentStatusPending        = "pending"
	LiandongPaymentStatusPaid           = "paid"
	LiandongPaymentStatusCreateFailed   = "create_failed"
	LiandongPaymentStatusCreateUnknown  = "create_unknown"
	LiandongPaymentStatusExpired        = "expired"
	LiandongPaymentStatusReviewRequired = "review_required"
	LiandongPaymentStatusClosed         = "closed"

	LiandongFulfillmentStatusWaiting        = "waiting"
	LiandongFulfillmentStatusFulfilled      = "fulfilled"
	LiandongFulfillmentStatusFailed         = "failed"
	LiandongFulfillmentStatusReviewRequired = "review_required"
)

var (
	ErrLiandongOrderNotFound        = errors.New("liandong order not found")
	ErrLiandongOrderBusy            = errors.New("liandong order is being checked")
	ErrLiandongOrderNotPaid         = errors.New("liandong order is not paid")
	ErrLiandongOrderReviewRequired  = errors.New("liandong order requires review")
	ErrLiandongContactConflict      = errors.New("liandong contact already exists")
	ErrLiandongInventoryUnavailable = errors.New("liandong product inventory is unavailable")
	ErrLiandongInventoryCapacity    = errors.New("liandong product inventory capacity exceeded")
)

type LiandongProduct struct {
	ID                  int    `json:"id" gorm:"primaryKey"`
	BusinessType        string `json:"business_type" gorm:"type:varchar(32);not null;index"`
	GoodsType           string `json:"goods_type" gorm:"type:varchar(32);not null;index"`
	Name                string `json:"name" gorm:"type:varchar(128);not null"`
	GoodsKey            string `json:"goods_key,omitempty" gorm:"type:varchar(128);not null;uniqueIndex"`
	QuotaAmount         int64  `json:"quota_amount" gorm:"type:bigint;not null"`
	PlanID              int    `json:"plan_id" gorm:"index"`
	ExpectedAmountMinor int64  `json:"expected_amount_minor" gorm:"type:bigint;not null"`
	Currency            string `json:"currency" gorm:"type:varchar(8);not null"`
	InventoryMode       string `json:"inventory_mode" gorm:"type:varchar(32);not null;index"`
	InventoryCapacity   int    `json:"inventory_capacity" gorm:"not null"`
	ThumbnailVersion    int64  `json:"thumbnail_version" gorm:"type:bigint;not null"`
	Enabled             bool   `json:"enabled"`
	SortOrder           int    `json:"sort_order"`
	CreatedBy           int    `json:"created_by"`
	UpdatedBy           int    `json:"updated_by"`
	CreatedAt           int64  `json:"created_at" gorm:"type:bigint"`
	UpdatedAt           int64  `json:"updated_at" gorm:"type:bigint"`
}

func (p *LiandongProduct) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	return nil
}

func (p *LiandongProduct) BeforeUpdate(_ *gorm.DB) error {
	p.UpdatedAt = common.GetTimestamp()
	return nil
}

type LiandongOrder struct {
	ID                    int     `json:"id" gorm:"primaryKey"`
	LocalTradeNo          string  `json:"local_trade_no" gorm:"type:varchar(128);not null;uniqueIndex"`
	ProviderTradeNo       *string `json:"provider_trade_no,omitempty" gorm:"type:varchar(128);uniqueIndex"`
	UserID                int     `json:"user_id" gorm:"not null;index"`
	ProductID             int     `json:"product_id" gorm:"not null;index"`
	ProductNameSnapshot   string  `json:"product_name" gorm:"type:varchar(128);not null"`
	BusinessType          string  `json:"business_type" gorm:"type:varchar(32);not null;index"`
	TargetID              int     `json:"target_id" gorm:"index"`
	GoodsKeySnapshot      string  `json:"-" gorm:"type:varchar(128);not null"`
	ContactSnapshot       string  `json:"-" gorm:"type:varchar(12);not null;uniqueIndex"`
	JUUIDSnapshot         string  `json:"-" gorm:"type:varchar(128);not null"`
	ExpectedAmountMinor   int64   `json:"expected_amount_minor" gorm:"type:bigint;not null"`
	CurrencySnapshot      string  `json:"currency" gorm:"type:varchar(8);not null"`
	FulfillmentSnapshot   string  `json:"-" gorm:"type:text;not null"`
	InventoryCodeID       int     `json:"inventory_code_id,omitempty" gorm:"not null;index"`
	PaymentStatus         string  `json:"payment_status" gorm:"type:varchar(32);not null;index"`
	FulfillmentStatus     string  `json:"fulfillment_status" gorm:"type:varchar(32);not null;index"`
	LastCheckAt           int64   `json:"last_check_at" gorm:"type:bigint"`
	NextCheckAt           int64   `json:"next_check_at" gorm:"type:bigint;index"`
	CheckDeadlineAt       int64   `json:"check_deadline_at" gorm:"type:bigint;index"`
	CheckCount            int     `json:"check_count"`
	ConsecutiveErrorCount int     `json:"consecutive_error_count"`
	CheckLockUntil        int64   `json:"-" gorm:"type:bigint;index"`
	ProviderSummary       string  `json:"-" gorm:"type:text"`
	LastError             string  `json:"-" gorm:"type:text"`
	ExpiresAt             int64   `json:"expires_at" gorm:"type:bigint;not null;index"`
	ClosedReason          string  `json:"closed_reason,omitempty" gorm:"type:varchar(64);not null;index"`
	LatePayment           bool    `json:"late_payment" gorm:"not null;index"`
	PaidAt                int64   `json:"paid_at" gorm:"type:bigint"`
	FulfilledAt           int64   `json:"fulfilled_at" gorm:"type:bigint"`
	CreatedAt             int64   `json:"created_at" gorm:"type:bigint;index"`
	UpdatedAt             int64   `json:"updated_at" gorm:"type:bigint"`
}

type LiandongUserOperationLease struct {
	UserID    int    `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Token     string `json:"-" gorm:"type:char(32);not null"`
	ExpiresAt int64  `json:"-" gorm:"type:bigint;not null;index"`
	UpdatedAt int64  `json:"-" gorm:"type:bigint"`
}

func (o *LiandongOrder) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if o.CreatedAt == 0 {
		o.CreatedAt = now
	}
	o.UpdatedAt = now
	return nil
}

func (o *LiandongOrder) BeforeUpdate(_ *gorm.DB) error {
	o.UpdatedAt = common.GetTimestamp()
	return nil
}

func TryAcquireLiandongUserOperationLease(
	userID int,
	ttlSeconds int64,
) (string, bool, error) {
	if userID <= 0 || ttlSeconds <= 0 {
		return "", false, errors.New("invalid liandong operation lease")
	}
	token := common.GetUUID()
	now := common.GetTimestamp()
	acquired := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userID).First(&user).Error; err != nil {
			return err
		}

		var lease LiandongUserOperationLease
		err := lockForUpdate(tx).Where("user_id = ?", userID).First(&lease).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).
				Create(&LiandongUserOperationLease{
					UserID:    userID,
					Token:     token,
					ExpiresAt: now + ttlSeconds,
					UpdatedAt: now,
				})
			if result.Error != nil {
				return result.Error
			}
			acquired = result.RowsAffected == 1
			return nil
		}
		if err != nil {
			return err
		}
		if lease.ExpiresAt > now {
			return nil
		}
		result := tx.Model(&LiandongUserOperationLease{}).
			Where("user_id = ? AND expires_at <= ?", userID, now).
			Updates(map[string]any{
				"token":      token,
				"expires_at": now + ttlSeconds,
				"updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		acquired = result.RowsAffected == 1
		return nil
	})
	if err != nil {
		return "", false, err
	}
	if !acquired {
		return "", false, nil
	}
	return token, true, nil
}

func ReleaseLiandongUserOperationLease(userID int, token string) error {
	if userID <= 0 || strings.TrimSpace(token) == "" {
		return ErrLiandongOrderBusy
	}
	result := DB.Where("user_id = ? AND token = ?", userID, token).
		Delete(&LiandongUserOperationLease{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLiandongOrderBusy
	}
	return nil
}

type LiandongQuotaSnapshot struct {
	QuotaAmount int64 `json:"quota_amount"`
}

type LiandongSubscriptionSnapshot struct {
	Plan SubscriptionPlan `json:"plan"`
}

type LiandongFulfillmentResult struct {
	UserID           int
	QuotaAdded       int64
	UpgradeGroup     string
	ProductName      string
	Money            float64
	AlreadyFulfilled bool
}

type LiandongOrderCreateResult struct {
	Order *LiandongOrder
}

func GetLiandongPaymentSettingsFromDB() (setting.LiandongPaymentSettings, error) {
	settingsSnapshot := setting.LiandongPaymentSettings{
		BaseURL:                   setting.DefaultLiandongBaseURL,
		ProxyTimeoutSeconds:       setting.DefaultLiandongProxyTimeoutSeconds,
		PollIntervalSeconds:       setting.DefaultLiandongPollIntervalSeconds,
		ClientPollIntervalSeconds: setting.DefaultLiandongClientPollIntervalSeconds,
		ReconcileBatchSize:        setting.DefaultLiandongReconcileBatchSize,
		PaymentTimeoutMinutes:     setting.DefaultLiandongPaymentTimeoutMinutes,
		JUUID:                     setting.DefaultLiandongJUUID,
		AuthMode:                  setting.LiandongAuthModeManualToken,
	}
	keys := []string{
		"LiandongEnabled",
		"LiandongCreateEnabled",
		"LiandongReconcileEnabled",
		"LiandongFulfillEnabled",
		"LiandongIframeEnabled",
		"LiandongBaseURL",
		"LiandongProxyEnabled",
		"LiandongProxyURL",
		"LiandongProxyUsername",
		"LiandongProxyPassword",
		"LiandongProxyTimeoutSeconds",
		"LiandongPollIntervalSeconds",
		"LiandongClientPollIntervalSeconds",
		"LiandongReconcileBatchSize",
		"LiandongPaymentTimeoutMinutes",
		"LiandongJUUID",
		"LiandongAuthMode",
		"LiandongUsername",
		"LiandongPassword",
		"LiandongMerchantToken",
	}
	var options []Option
	if err := DB.Where(map[string]any{"key": keys}).Find(&options).Error; err != nil {
		return settingsSnapshot, err
	}
	for _, option := range options {
		switch option.Key {
		case "LiandongEnabled":
			settingsSnapshot.Enabled = option.Value == "true"
		case "LiandongCreateEnabled":
			settingsSnapshot.CreateEnabled = option.Value == "true"
		case "LiandongReconcileEnabled":
			settingsSnapshot.ReconcileEnabled = option.Value == "true"
		case "LiandongFulfillEnabled":
			settingsSnapshot.FulfillEnabled = option.Value == "true"
		case "LiandongIframeEnabled":
			settingsSnapshot.IframeEnabled = option.Value == "true"
		case "LiandongBaseURL":
			if normalized, err := setting.NormalizeLiandongBaseURL(option.Value); err == nil {
				settingsSnapshot.BaseURL = normalized
			}
		case "LiandongProxyEnabled":
			settingsSnapshot.ProxyEnabled = option.Value == "true"
		case "LiandongProxyURL":
			if normalized, err := setting.NormalizeLiandongProxyURL(option.Value); err == nil {
				settingsSnapshot.ProxyURL = normalized
			}
		case "LiandongProxyUsername":
			settingsSnapshot.ProxyUsername = strings.TrimSpace(option.Value)
		case "LiandongProxyPassword":
			settingsSnapshot.ProxyPassword = option.Value
		case "LiandongProxyTimeoutSeconds":
			seconds, err := strconv.Atoi(option.Value)
			if err == nil &&
				seconds >= setting.MinLiandongProxyTimeoutSeconds &&
				seconds <= setting.MaxLiandongProxyTimeoutSeconds {
				settingsSnapshot.ProxyTimeoutSeconds = seconds
			}
		case "LiandongPollIntervalSeconds":
			seconds, err := strconv.Atoi(option.Value)
			if err == nil &&
				seconds >= setting.MinLiandongPollIntervalSeconds &&
				seconds <= setting.MaxLiandongPollIntervalSeconds {
				settingsSnapshot.PollIntervalSeconds = seconds
			}
		case "LiandongClientPollIntervalSeconds":
			seconds, err := strconv.Atoi(option.Value)
			if err == nil &&
				seconds >= setting.MinLiandongClientPollIntervalSeconds &&
				seconds <= setting.MaxLiandongClientPollIntervalSeconds {
				settingsSnapshot.ClientPollIntervalSeconds = seconds
			}
		case "LiandongReconcileBatchSize":
			size, err := strconv.Atoi(option.Value)
			if err == nil &&
				size >= setting.MinLiandongReconcileBatchSize &&
				size <= setting.MaxLiandongReconcileBatchSize {
				settingsSnapshot.ReconcileBatchSize = size
			}
		case "LiandongPaymentTimeoutMinutes":
			minutes, err := strconv.Atoi(option.Value)
			if err == nil &&
				minutes >= setting.MinLiandongPaymentTimeoutMinutes &&
				minutes <= setting.MaxLiandongPaymentTimeoutMinutes {
				settingsSnapshot.PaymentTimeoutMinutes = minutes
			}
		case "LiandongJUUID":
			settingsSnapshot.JUUID = strings.TrimSpace(option.Value)
		case "LiandongAuthMode":
			authMode := strings.TrimSpace(option.Value)
			if authMode == setting.LiandongAuthModeManualToken ||
				authMode == setting.LiandongAuthModeCredentials {
				settingsSnapshot.AuthMode = authMode
			}
		case "LiandongUsername":
			settingsSnapshot.Username = strings.TrimSpace(option.Value)
		case "LiandongPassword":
			settingsSnapshot.Password = option.Value
		case "LiandongMerchantToken":
			settingsSnapshot.MerchantToken = strings.TrimSpace(option.Value)
		}
	}
	return settingsSnapshot, nil
}

func CreateLiandongOrder(
	userID int,
	productID int,
	contact string,
	juuid string,
) (*LiandongOrderCreateResult, error) {
	return CreateLiandongOrderWithTimeout(
		userID,
		productID,
		contact,
		juuid,
		setting.DefaultLiandongPaymentTimeoutMinutes,
	)
}

func CreateLiandongOrderWithTimeout(
	userID int,
	productID int,
	contact string,
	juuid string,
	timeoutMinutes int,
) (*LiandongOrderCreateResult, error) {
	if userID <= 0 || productID <= 0 {
		return nil, errors.New("invalid user or product")
	}
	if !validLiandongContact(contact) || strings.TrimSpace(juuid) == "" {
		return nil, errors.New("invalid liandong order identity")
	}
	if timeoutMinutes < setting.MinLiandongPaymentTimeoutMinutes ||
		timeoutMinutes > setting.MaxLiandongPaymentTimeoutMinutes {
		return nil, errors.New("invalid liandong payment timeout")
	}
	var createdOrder LiandongOrder
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userID).First(&user).Error; err != nil {
			return err
		}

		now := common.GetTimestamp()
		if err := tx.Model(&LiandongProductInventoryCode{}).
			Where(
				"reserved_user_id = ? AND status = ?",
				userID,
				LiandongInventoryStatusReserved,
			).
			Updates(map[string]any{
				"status":            LiandongInventoryStatusAvailable,
				"reserved_order_id": 0,
				"reserved_trade_no": "",
				"reserved_user_id":  0,
				"reserved_at":       0,
				"updated_at":        now,
			}).Error; err != nil {
			return err
		}

		var contactCount int64
		if err := tx.Model(&LiandongOrder{}).Where("contact_snapshot = ?", contact).Count(&contactCount).Error; err != nil {
			return err
		}
		if contactCount > 0 {
			return ErrLiandongContactConflict
		}

		var product LiandongProduct
		if err := lockForUpdate(tx).Where("id = ? AND enabled = ?", productID, true).First(&product).Error; err != nil {
			return err
		}
		if err := ValidateLiandongProduct(&product); err != nil {
			return err
		}

		var fulfillmentSnapshot []byte
		targetID := 0
		switch product.BusinessType {
		case LiandongBusinessTypeQuota:
			data, err := common.Marshal(LiandongQuotaSnapshot{QuotaAmount: product.QuotaAmount})
			if err != nil {
				return err
			}
			fulfillmentSnapshot = data
		case LiandongBusinessTypeSubscription:
			var plan SubscriptionPlan
			if err := lockForUpdate(tx).Where("id = ? AND enabled = ?", product.PlanID, true).First(&plan).Error; err != nil {
				return err
			}
			plan.NormalizeDefaults()
			if plan.MaxPurchasePerUser > 0 {
				var fulfilledCount int64
				if err := tx.Model(&UserSubscription{}).
					Where("user_id = ? AND plan_id = ?", userID, plan.Id).
					Count(&fulfilledCount).Error; err != nil {
					return err
				}
				var reservedCount int64
				if err := tx.Model(&LiandongOrder{}).
					Where("user_id = ? AND target_id = ? AND business_type = ?", userID, plan.Id, LiandongBusinessTypeSubscription).
					Where("payment_status = ? AND fulfillment_status <> ?",
						LiandongPaymentStatusPaid,
						LiandongFulfillmentStatusFulfilled).
					Count(&reservedCount).Error; err != nil {
					return err
				}
				if fulfilledCount+reservedCount >= int64(plan.MaxPurchasePerUser) {
					return ErrSubscriptionPurchaseLimit
				}
			}
			data, err := common.Marshal(LiandongSubscriptionSnapshot{Plan: plan})
			if err != nil {
				return err
			}
			fulfillmentSnapshot = data
			targetID = plan.Id
		default:
			return errors.New("invalid business type")
		}

		createdOrder = LiandongOrder{
			LocalTradeNo:        fmt.Sprintf("LDUSR%d%s", userID, common.GetUUID()),
			UserID:              userID,
			ProductID:           product.ID,
			ProductNameSnapshot: product.Name,
			BusinessType:        product.BusinessType,
			TargetID:            targetID,
			GoodsKeySnapshot:    product.GoodsKey,
			ContactSnapshot:     contact,
			JUUIDSnapshot:       strings.TrimSpace(juuid),
			ExpectedAmountMinor: product.ExpectedAmountMinor,
			CurrencySnapshot:    product.Currency,
			FulfillmentSnapshot: string(fulfillmentSnapshot),
			ExpiresAt:           now + int64(timeoutMinutes*60),
			PaymentStatus:       LiandongPaymentStatusCreating,
			FulfillmentStatus:   LiandongFulfillmentStatusWaiting,
			NextCheckAt:         0,
			CheckDeadlineAt:     0,
		}
		if err := tx.Create(&createdOrder).Error; err != nil {
			return err
		}
		if err := reserveLiandongInventoryTx(tx, &createdOrder); err != nil {
			return err
		}

		money := float64(product.ExpectedAmountMinor) / 100
		switch product.BusinessType {
		case LiandongBusinessTypeQuota:
			return tx.Create(&TopUp{
				UserId:          userID,
				Amount:          0,
				Money:           money,
				TradeNo:         createdOrder.LocalTradeNo,
				PaymentMethod:   PaymentMethodLiandong,
				PaymentProvider: PaymentProviderLiandong,
				CreateTime:      now,
				Status:          common.TopUpStatusPending,
			}).Error
		case LiandongBusinessTypeSubscription:
			return tx.Create(&SubscriptionOrder{
				UserId:          userID,
				PlanId:          targetID,
				Money:           money,
				TradeNo:         createdOrder.LocalTradeNo,
				PaymentMethod:   PaymentMethodLiandong,
				PaymentProvider: PaymentProviderLiandong,
				Status:          common.TopUpStatusPending,
				CreateTime:      now,
			}).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &LiandongOrderCreateResult{Order: &createdOrder}, nil
}

func validLiandongContact(contact string) bool {
	if len(contact) != 12 || contact[0] == '0' {
		return false
	}
	for _, char := range contact {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func ValidateLiandongProduct(product *LiandongProduct) error {
	if product == nil {
		return errors.New("product is required")
	}
	product.BusinessType = strings.TrimSpace(product.BusinessType)
	product.GoodsType = strings.TrimSpace(product.GoodsType)
	product.Name = strings.TrimSpace(product.Name)
	product.GoodsKey = strings.TrimSpace(product.GoodsKey)
	product.Currency = strings.ToUpper(strings.TrimSpace(product.Currency))
	product.InventoryMode = strings.TrimSpace(product.InventoryMode)
	if product.Name == "" || product.GoodsKey == "" {
		return errors.New("product name and goods key are required")
	}
	if len(product.Name) > 128 || len(product.GoodsKey) > 128 {
		return errors.New("product name or goods key is too long")
	}
	if product.ExpectedAmountMinor <= 0 {
		return errors.New("expected amount must be positive")
	}
	if product.Currency == "" {
		product.Currency = "CNY"
	}
	if product.Currency != "CNY" && product.Currency != "USD" {
		return errors.New("currency must be CNY or USD")
	}
	if product.GoodsType == "" {
		product.GoodsType = "card"
	}
	switch product.GoodsType {
	case "article", "card", "resource", "equity":
	default:
		return errors.New("invalid goods type")
	}
	if product.InventoryMode == "" {
		product.InventoryMode = LiandongInventoryModeUnlimited
	}
	switch product.InventoryMode {
	case LiandongInventoryModeUnlimited:
		product.InventoryCapacity = 0
	case LiandongInventoryModeRedemptionCode:
		if product.InventoryCapacity <= 0 {
			return errors.New("inventory capacity must be positive")
		}
	default:
		return errors.New("invalid inventory mode")
	}
	switch product.BusinessType {
	case LiandongBusinessTypeQuota:
		if product.QuotaAmount <= 0 || product.QuotaAmount > math.MaxInt32 {
			return errors.New("quota amount must be between 1 and 2147483647")
		}
		product.PlanID = 0
	case LiandongBusinessTypeSubscription:
		if product.PlanID <= 0 {
			return errors.New("subscription product requires a plan")
		}
		product.QuotaAmount = 0
	default:
		return errors.New("invalid business type")
	}
	return nil
}

func ListEnabledLiandongProducts() ([]LiandongProduct, error) {
	var products []LiandongProduct
	err := DB.Where("enabled = ?", true).Order("sort_order desc, id desc").Find(&products).Error
	return products, err
}

func ListAllLiandongProducts() ([]LiandongProduct, error) {
	var products []LiandongProduct
	err := DB.Order("sort_order desc, id desc").Find(&products).Error
	return products, err
}

func GetLiandongOrderForUser(userID int, localTradeNo string) (*LiandongOrder, error) {
	var order LiandongOrder
	err := DB.Where("user_id = ? AND local_trade_no = ?", userID, localTradeNo).First(&order).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrLiandongOrderNotFound
		}
		return nil, err
	}
	return &order, nil
}

func GetLiandongOrder(localTradeNo string) (*LiandongOrder, error) {
	var order LiandongOrder
	if err := DB.Where("local_trade_no = ?", localTradeNo).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrLiandongOrderNotFound
		}
		return nil, err
	}
	return &order, nil
}

func ListLiandongOrders(pageInfo *common.PageInfo, keyword string) ([]LiandongOrder, int64, error) {
	query := DB.Model(&LiandongOrder{})
	keyword = strings.TrimSpace(keyword)
	if keyword != "" {
		pattern, err := sanitizeLikePattern(keyword)
		if err != nil {
			return nil, 0, err
		}
		condition := "(local_trade_no LIKE ? ESCAPE '!' OR provider_trade_no LIKE ? ESCAPE '!' OR product_name_snapshot LIKE ? ESCAPE '!')"
		args := []any{pattern, pattern, pattern}
		if userID, err := strconv.Atoi(keyword); err == nil && userID > 0 {
			condition = "(local_trade_no LIKE ? ESCAPE '!' OR provider_trade_no LIKE ? ESCAPE '!' OR product_name_snapshot LIKE ? ESCAPE '!' OR user_id = ?)"
			args = append(args, userID)
		}
		query = query.Where(condition, args...)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var orders []LiandongOrder
	if err := query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

func ClaimLiandongPendingOrder(localTradeNo string) (*LiandongOrder, error) {
	return ClaimLiandongPendingOrderAfter(localTradeNo, 0)
}

func ClaimLiandongPendingOrderAfter(localTradeNo string, minimumIntervalSeconds int) (*LiandongOrder, error) {
	now := common.GetTimestamp()
	query := DB.Model(&LiandongOrder{}).
		Where(
			"local_trade_no = ? AND payment_status IN ? AND provider_trade_no IS NOT NULL AND (check_lock_until = 0 OR check_lock_until < ?)",
			localTradeNo,
			[]string{LiandongPaymentStatusPending, LiandongPaymentStatusCreateUnknown},
			now,
		)
	if minimumIntervalSeconds > 0 {
		query = query.Where(
			"(last_check_at = 0 OR last_check_at <= ?)",
			now-int64(minimumIntervalSeconds),
		)
	}
	result := query.
		Updates(map[string]any{
			"check_lock_until": now + 30,
			"updated_at":       now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, liandongClaimError(localTradeNo)
	}
	return GetLiandongOrder(localTradeNo)
}

func ClaimLiandongUnsettledOrder(localTradeNo string) (*LiandongOrder, error) {
	now := common.GetTimestamp()
	result := DB.Model(&LiandongOrder{}).
		Where(
			"local_trade_no = ? AND payment_status IN ? AND provider_trade_no IS NOT NULL AND (check_lock_until = 0 OR check_lock_until < ?)",
			localTradeNo,
			[]string{LiandongPaymentStatusPending, LiandongPaymentStatusCreateUnknown},
			now,
		).
		Updates(map[string]any{
			"check_lock_until": now + 30,
			"updated_at":       now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, liandongClaimError(localTradeNo)
	}
	return GetLiandongOrder(localTradeNo)
}

func ClaimLiandongOrderByProviderTradeNo(providerTradeNo string) (*LiandongOrder, error) {
	providerTradeNo = strings.TrimSpace(providerTradeNo)
	if providerTradeNo == "" {
		return nil, ErrLiandongOrderNotFound
	}
	now := common.GetTimestamp()
	result := DB.Model(&LiandongOrder{}).
		Where(
			"provider_trade_no = ? AND payment_status <> ? AND late_payment = ? AND (check_lock_until = 0 OR check_lock_until < ?)",
			providerTradeNo,
			LiandongPaymentStatusPaid,
			false,
			now,
		).
		Updates(map[string]any{
			"check_lock_until": now + 30,
			"updated_at":       now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		var count int64
		if err := DB.Model(&LiandongOrder{}).
			Where("provider_trade_no = ?", providerTradeNo).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, ErrLiandongOrderNotFound
		}
		return nil, ErrLiandongOrderBusy
	}
	var order LiandongOrder
	if err := DB.Where("provider_trade_no = ?", providerTradeNo).First(&order).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

func ClaimLiandongClosableOrder(localTradeNo string) (*LiandongOrder, error) {
	now := common.GetTimestamp()
	result := DB.Model(&LiandongOrder{}).
		Where(
			"local_trade_no = ? AND payment_status IN ? AND provider_trade_no IS NOT NULL AND (check_lock_until = 0 OR check_lock_until < ?)",
			localTradeNo,
			[]string{
				LiandongPaymentStatusPending,
				LiandongPaymentStatusCreateFailed,
				LiandongPaymentStatusCreateUnknown,
				LiandongPaymentStatusExpired,
				LiandongPaymentStatusReviewRequired,
			},
			now,
		).
		Updates(map[string]any{
			"check_lock_until": now + 30,
			"updated_at":       now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, liandongClaimError(localTradeNo)
	}
	return GetLiandongOrder(localTradeNo)
}

func ClaimLiandongPaidOrder(localTradeNo string) (*LiandongOrder, error) {
	now := common.GetTimestamp()
	result := DB.Model(&LiandongOrder{}).
		Where(
			"local_trade_no = ? AND payment_status = ? AND fulfillment_status IN ? AND next_check_at <= ? AND (check_lock_until = 0 OR check_lock_until < ?)",
			localTradeNo,
			LiandongPaymentStatusPaid,
			[]string{LiandongFulfillmentStatusWaiting, LiandongFulfillmentStatusFailed},
			now,
			now,
		).
		Updates(map[string]any{
			"check_lock_until": now + 30,
			"updated_at":       now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, liandongClaimError(localTradeNo)
	}
	return GetLiandongOrder(localTradeNo)
}

func ClaimLiandongStaleCreatingOrder(localTradeNo string) (*LiandongOrder, error) {
	now := common.GetTimestamp()
	result := DB.Model(&LiandongOrder{}).
		Where(
			"local_trade_no = ? AND payment_status = ? AND created_at <= ? AND (check_lock_until = 0 OR check_lock_until < ?)",
			localTradeNo,
			LiandongPaymentStatusCreating,
			now-300,
			now,
		).
		Updates(map[string]any{
			"check_lock_until": now + 30,
			"updated_at":       now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, liandongClaimError(localTradeNo)
	}
	return GetLiandongOrder(localTradeNo)
}

func liandongClaimError(localTradeNo string) error {
	var count int64
	if err := DB.Model(&LiandongOrder{}).Where("local_trade_no = ?", localTradeNo).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrLiandongOrderNotFound
	}
	return ErrLiandongOrderBusy
}

func ReleaseLiandongOrderCheck(localTradeNo string, checkLockUntil int64) error {
	if checkLockUntil <= 0 {
		return ErrLiandongOrderBusy
	}
	result := DB.Model(&LiandongOrder{}).
		Where("local_trade_no = ? AND check_lock_until = ?", localTradeNo, checkLockUntil).
		Updates(map[string]any{
			"check_lock_until": 0,
			"updated_at":       common.GetTimestamp(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLiandongOrderBusy
	}
	return nil
}

func CompleteLiandongOrderCheck(
	localTradeNo string,
	checkLockUntil int64,
	paymentStatus string,
	providerSummary string,
) error {
	if checkLockUntil <= 0 {
		return ErrLiandongOrderBusy
	}
	now := common.GetTimestamp()
	updates := map[string]any{
		"last_check_at":           now,
		"next_check_at":           0,
		"check_count":             gorm.Expr("check_count + 1"),
		"consecutive_error_count": 0,
		"check_lock_until":        0,
		"last_error":              "",
		"provider_summary":        providerSummary,
		"updated_at":              now,
	}
	if paymentStatus != "" {
		updates["payment_status"] = paymentStatus
	}
	if paymentStatus == LiandongPaymentStatusPaid {
		updates["paid_at"] = now
		updates["next_check_at"] = now
	}
	result := DB.Model(&LiandongOrder{}).
		Where(
			"local_trade_no = ? AND payment_status IN ? AND check_lock_until = ?",
			localTradeNo,
			[]string{LiandongPaymentStatusPending, LiandongPaymentStatusCreateUnknown},
			checkLockUntil,
		).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLiandongOrderBusy
	}
	return nil
}

func FailLiandongOrderCheck(
	localTradeNo string,
	checkLockUntil int64,
	consecutiveErrors int,
	lastError string,
) error {
	if checkLockUntil <= 0 {
		return ErrLiandongOrderBusy
	}
	now := common.GetTimestamp()
	result := DB.Model(&LiandongOrder{}).
		Where(
			"local_trade_no = ? AND payment_status IN ? AND check_lock_until = ?",
			localTradeNo,
			[]string{LiandongPaymentStatusPending, LiandongPaymentStatusCreateUnknown},
			checkLockUntil,
		).
		Updates(map[string]any{
			"last_check_at":           now,
			"next_check_at":           0,
			"check_count":             gorm.Expr("check_count + 1"),
			"consecutive_error_count": consecutiveErrors,
			"check_lock_until":        0,
			"last_error":              lastError,
			"updated_at":              now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLiandongOrderBusy
	}
	return nil
}

func MarkLiandongStaleCreateReviewRequired(
	localTradeNo string,
	checkLockUntil int64,
	lastError string,
) error {
	if checkLockUntil <= 0 {
		return ErrLiandongOrderBusy
	}
	now := common.GetTimestamp()
	result := DB.Model(&LiandongOrder{}).
		Where(
			"local_trade_no = ? AND payment_status = ? AND check_lock_until = ? AND created_at <= ?",
			localTradeNo,
			LiandongPaymentStatusCreating,
			checkLockUntil,
			now-300,
		).
		Updates(map[string]any{
			"payment_status":          LiandongPaymentStatusCreateUnknown,
			"fulfillment_status":      LiandongFulfillmentStatusReviewRequired,
			"last_check_at":           now,
			"next_check_at":           0,
			"check_count":             gorm.Expr("check_count + 1"),
			"check_lock_until":        0,
			"last_error":              lastError,
			"consecutive_error_count": 0,
			"updated_at":              now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLiandongOrderBusy
	}
	return nil
}

func MarkLiandongOrderReviewRequired(
	localTradeNo string,
	checkLockUntil int64,
	providerSummary string,
	lastError string,
) error {
	if checkLockUntil <= 0 {
		return ErrLiandongOrderBusy
	}
	now := common.GetTimestamp()
	result := DB.Model(&LiandongOrder{}).
		Where(
			"local_trade_no = ? AND payment_status IN ? AND check_lock_until = ?",
			localTradeNo,
			[]string{LiandongPaymentStatusPending, LiandongPaymentStatusCreateUnknown},
			checkLockUntil,
		).
		Updates(map[string]any{
			"payment_status":          LiandongPaymentStatusReviewRequired,
			"fulfillment_status":      LiandongFulfillmentStatusReviewRequired,
			"last_check_at":           now,
			"next_check_at":           0,
			"check_count":             gorm.Expr("check_count + 1"),
			"check_lock_until":        0,
			"provider_summary":        providerSummary,
			"last_error":              lastError,
			"consecutive_error_count": 0,
			"updated_at":              now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLiandongOrderBusy
	}
	return nil
}

func FindPendingLiandongOrders(limit int) ([]LiandongOrder, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	now := common.GetTimestamp()
	var orders []LiandongOrder
	err := DB.Model(&LiandongOrder{}).
		Where(
			"payment_status IN ? AND provider_trade_no IS NOT NULL AND (check_lock_until = 0 OR check_lock_until < ?)",
			[]string{LiandongPaymentStatusPending, LiandongPaymentStatusCreateUnknown},
			now,
		).
		Order("last_check_at asc, id asc").
		Limit(limit).
		Find(&orders).Error
	return orders, err
}

func FindStaleCreatingLiandongOrders(limit int) ([]LiandongOrder, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	now := common.GetTimestamp()
	var orders []LiandongOrder
	err := DB.Model(&LiandongOrder{}).
		Where(
			"payment_status = ? AND created_at <= ? AND (check_lock_until = 0 OR check_lock_until < ?)",
			LiandongPaymentStatusCreating,
			now-300,
			now,
		).
		Order("created_at asc, id asc").
		Limit(limit).
		Find(&orders).Error
	return orders, err
}

func FindDuePaidLiandongOrders(limit int) ([]LiandongOrder, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	now := common.GetTimestamp()
	var orders []LiandongOrder
	err := DB.Model(&LiandongOrder{}).
		Where(
			"payment_status = ? AND fulfillment_status IN ? AND next_check_at <= ? AND (check_lock_until = 0 OR check_lock_until < ?)",
			LiandongPaymentStatusPaid,
			[]string{LiandongFulfillmentStatusWaiting, LiandongFulfillmentStatusFailed},
			now,
			now,
		).
		Order("next_check_at asc, id asc").
		Limit(limit).
		Find(&orders).Error
	return orders, err
}

func HasLiandongWork(
	reconcileEnabled bool,
	fulfillEnabled bool,
	timeoutVerificationEnabled bool,
) bool {
	now := common.GetTimestamp()
	staleCreatingBefore := now - 300
	unsettledStatuses := []string{
		LiandongPaymentStatusCreating,
		LiandongPaymentStatusPending,
		LiandongPaymentStatusCreateUnknown,
	}
	conditions := make([]string, 0, 4)
	args := make([]any, 0, 8)
	if timeoutVerificationEnabled {
		conditions = append(
			conditions,
			"(payment_status IN ? AND expires_at > 0 AND expires_at <= ? AND provider_trade_no IS NULL)",
			"(payment_status IN ? AND expires_at > 0 AND expires_at <= ? AND provider_trade_no IS NOT NULL)",
		)
		args = append(args, unsettledStatuses, now, unsettledStatuses, now)
	}
	if reconcileEnabled {
		conditions = append(
			conditions,
			"(payment_status <> ? AND provider_trade_no IS NOT NULL AND late_payment = ?)",
			"(payment_status = ? AND created_at <= ?)",
		)
		args = append(
			args,
			LiandongPaymentStatusPaid,
			false,
			LiandongPaymentStatusCreating,
			staleCreatingBefore,
		)
	}
	if fulfillEnabled {
		conditions = append(
			conditions,
			"(payment_status = ? AND fulfillment_status IN ?)",
		)
		args = append(
			args,
			LiandongPaymentStatusPaid,
			[]string{LiandongFulfillmentStatusWaiting, LiandongFulfillmentStatusFailed},
		)
	}
	if len(conditions) == 0 {
		return false
	}
	query := DB.Model(&LiandongOrder{}).Where(strings.Join(conditions, " OR "), args...)
	var count int64
	return query.Limit(1).Count(&count).Error == nil && count > 0
}

func HasLiandongPendingProviderOrders() bool {
	var count int64
	err := DB.Model(&LiandongOrder{}).
		Where("payment_status IN ? AND provider_trade_no IS NOT NULL", []string{
			LiandongPaymentStatusPending,
			LiandongPaymentStatusCreateUnknown,
		}).
		Limit(1).
		Count(&count).Error
	return err == nil && count > 0
}

func MarkLiandongCreateResult(localTradeNo string, providerTradeNo *string, paymentStatus string, lastError string) error {
	updates := map[string]any{
		"payment_status": paymentStatus,
		"last_error":     lastError,
		"next_check_at":  0,
		"updated_at":     common.GetTimestamp(),
	}
	if providerTradeNo != nil {
		updates["provider_trade_no"] = providerTradeNo
	}
	result := DB.Model(&LiandongOrder{}).
		Where("local_trade_no = ? AND payment_status = ?", localTradeNo, LiandongPaymentStatusCreating).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("liandong create result was not persisted")
	}
	return nil
}

func MarkLiandongCreateFailure(localTradeNo string, paymentStatus string, lastError string) error {
	if paymentStatus != LiandongPaymentStatusCreateFailed &&
		paymentStatus != LiandongPaymentStatusCreateUnknown {
		return errors.New("invalid liandong create failure status")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var order LiandongOrder
		if err := lockForUpdate(tx).
			Where("local_trade_no = ? AND payment_status = ?", localTradeNo, LiandongPaymentStatusCreating).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLiandongOrderNotFound
			}
			return err
		}

		fulfillmentStatus := LiandongFulfillmentStatusFailed
		if paymentStatus == LiandongPaymentStatusCreateUnknown {
			fulfillmentStatus = LiandongFulfillmentStatusReviewRequired
		}
		now := common.GetTimestamp()
		orderUpdate := tx.Model(&LiandongOrder{}).
			Where("id = ? AND payment_status = ?", order.ID, LiandongPaymentStatusCreating).
			Updates(map[string]any{
				"payment_status":     paymentStatus,
				"fulfillment_status": fulfillmentStatus,
				"last_error":         lastError,
				"next_check_at":      0,
				"check_lock_until":   0,
				"updated_at":         now,
			})
		if orderUpdate.Error != nil {
			return orderUpdate.Error
		}
		if orderUpdate.RowsAffected != 1 {
			return ErrLiandongOrderBusy
		}
		if paymentStatus != LiandongPaymentStatusCreateFailed {
			return nil
		}
		if err := releaseLiandongInventoryTx(tx, &order, now); err != nil {
			return err
		}
		return failLiandongApplicationTx(tx, &order, now)
	})
}

func MarkLiandongCreatePersistenceFailure(localTradeNo string, providerTradeNo string, lastError string) error {
	statuses := []string{LiandongPaymentStatusCreating, LiandongPaymentStatusPending}
	updates := map[string]any{
		"provider_trade_no":  providerTradeNo,
		"payment_status":     LiandongPaymentStatusCreateUnknown,
		"fulfillment_status": LiandongFulfillmentStatusReviewRequired,
		"last_error":         lastError,
		"next_check_at":      0,
		"check_lock_until":   0,
		"updated_at":         common.GetTimestamp(),
	}
	result := DB.Model(&LiandongOrder{}).
		Where("local_trade_no = ? AND payment_status IN ?",
			localTradeNo,
			statuses,
		).
		Updates(updates)
	if result.Error == nil && result.RowsAffected == 1 {
		return nil
	}

	persistError := result.Error
	if persistError == nil {
		persistError = errors.New("liandong create persistence failure was not recorded")
	}
	fallbackError := fmt.Sprintf(
		"%s; provider trade number %s could not be persisted safely",
		lastError,
		providerTradeNo,
	)
	fallback := DB.Model(&LiandongOrder{}).
		Where("local_trade_no = ? AND payment_status IN ?", localTradeNo, statuses).
		Updates(map[string]any{
			"payment_status":     LiandongPaymentStatusCreateUnknown,
			"fulfillment_status": LiandongFulfillmentStatusReviewRequired,
			"last_error":         fallbackError,
			"next_check_at":      0,
			"check_lock_until":   0,
			"updated_at":         common.GetTimestamp(),
		})
	if fallback.Error != nil {
		return errors.Join(persistError, fallback.Error)
	}
	if fallback.RowsAffected != 1 {
		return errors.Join(
			persistError,
			errors.New("liandong create persistence fallback was not recorded"),
		)
	}
	return nil
}

func RequeueLiandongOrder(localTradeNo string) error {
	return RequeueLiandongOrderWithTimeout(
		localTradeNo,
		setting.DefaultLiandongPaymentTimeoutMinutes,
	)
}

func RequeueLiandongOrderWithTimeout(localTradeNo string, timeoutMinutes int) error {
	if timeoutMinutes < setting.MinLiandongPaymentTimeoutMinutes ||
		timeoutMinutes > setting.MaxLiandongPaymentTimeoutMinutes {
		return errors.New("invalid liandong payment timeout")
	}
	var orderOwner struct {
		UserID int
	}
	if err := DB.Model(&LiandongOrder{}).
		Select("user_id").
		Where("local_trade_no = ?", localTradeNo).
		Take(&orderOwner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrLiandongOrderNotFound
		}
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		now := common.GetTimestamp()
		var user User
		if err := lockForUpdate(tx).
			Select("id").
			Where("id = ?", orderOwner.UserID).
			First(&user).Error; err != nil {
			return err
		}

		var order LiandongOrder
		if err := lockForUpdate(tx).
			Where("local_trade_no = ?", localTradeNo).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLiandongOrderNotFound
			}
			return err
		}
		if order.UserID != orderOwner.UserID {
			return ErrLiandongOrderBusy
		}
		if order.CheckLockUntil >= now {
			return ErrLiandongOrderBusy
		}

		updates := map[string]any{
			"check_lock_until":        0,
			"last_error":              "",
			"provider_summary":        "",
			"consecutive_error_count": 0,
			"updated_at":              now,
		}
		if order.PaymentStatus == LiandongPaymentStatusPaid {
			if order.FulfillmentStatus != LiandongFulfillmentStatusWaiting &&
				order.FulfillmentStatus != LiandongFulfillmentStatusFailed {
				return ErrLiandongOrderNotFound
			}
			updates["next_check_at"] = now
		} else {
			if order.ProviderTradeNo == nil ||
				order.FulfillmentStatus == LiandongFulfillmentStatusFulfilled {
				return ErrLiandongOrderNotFound
			}
			if order.LatePayment &&
				order.PaidAt > 0 &&
				order.FulfillmentStatus == LiandongFulfillmentStatusReviewRequired {
				return ErrLiandongOrderReviewRequired
			}
			requeueable := false
			for _, status := range []string{
				LiandongPaymentStatusPending,
				LiandongPaymentStatusCreateUnknown,
				LiandongPaymentStatusExpired,
				LiandongPaymentStatusReviewRequired,
				LiandongPaymentStatusClosed,
			} {
				if order.PaymentStatus == status {
					requeueable = true
					break
				}
			}
			if !requeueable {
				return ErrLiandongOrderNotFound
			}
			if err := reserveLiandongInventoryTx(tx, &order); err != nil {
				return err
			}
			if err := restoreLiandongApplicationTx(tx, &order); err != nil {
				return err
			}
			updates["payment_status"] = LiandongPaymentStatusPending
			updates["fulfillment_status"] = LiandongFulfillmentStatusWaiting
			updates["next_check_at"] = 0
			updates["expires_at"] = now + int64(timeoutMinutes*60)
			updates["closed_reason"] = ""
			updates["late_payment"] = false
			updates["paid_at"] = 0
		}

		result := tx.Model(&LiandongOrder{}).
			Where("id = ? AND payment_status = ?", order.ID, order.PaymentStatus).
			Where("(check_lock_until = 0 OR check_lock_until < ?)", now).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLiandongOrderBusy
		}
		return nil
	})
}

func CloseLiandongOrder(localTradeNo string) error {
	return closeLiandongOrder(localTradeNo, 0, "closed by root operator")
}

func failLiandongApplicationTx(tx *gorm.DB, order *LiandongOrder, now int64) error {
	if tx == nil || order == nil {
		return errors.New("invalid liandong application")
	}
	switch order.BusinessType {
	case LiandongBusinessTypeQuota:
		var topUp TopUp
		if err := lockForUpdate(tx).
			Where("trade_no = ? AND payment_provider = ?", order.LocalTradeNo, PaymentProviderLiandong).
			First(&topUp).Error; err != nil {
			return err
		}
		if topUp.UserId != order.UserID {
			return ErrPaymentMethodMismatch
		}
		if topUp.Status == common.TopUpStatusFailed {
			return nil
		}
		if topUp.Status != common.TopUpStatusPending {
			return ErrTopUpStatusInvalid
		}
		result := tx.Model(&TopUp{}).
			Where("id = ? AND status = ?", topUp.Id, common.TopUpStatusPending).
			Updates(map[string]any{
				"status":        common.TopUpStatusFailed,
				"complete_time": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLiandongOrderBusy
		}
		return nil
	case LiandongBusinessTypeSubscription:
		var subscriptionOrder SubscriptionOrder
		if err := lockForUpdate(tx).
			Where("trade_no = ? AND payment_provider = ?", order.LocalTradeNo, PaymentProviderLiandong).
			First(&subscriptionOrder).Error; err != nil {
			return err
		}
		if subscriptionOrder.UserId != order.UserID {
			return ErrPaymentMethodMismatch
		}
		if subscriptionOrder.Status == common.TopUpStatusFailed {
			return nil
		}
		if subscriptionOrder.Status != common.TopUpStatusPending {
			return ErrSubscriptionOrderStatusInvalid
		}
		result := tx.Model(&SubscriptionOrder{}).
			Where("id = ? AND status = ?", subscriptionOrder.Id, common.TopUpStatusPending).
			Updates(map[string]any{
				"status":        common.TopUpStatusFailed,
				"complete_time": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLiandongOrderBusy
		}
		return nil
	default:
		return errors.New("invalid liandong business type")
	}
}

func restoreLiandongApplicationTx(tx *gorm.DB, order *LiandongOrder) error {
	if tx == nil || order == nil {
		return errors.New("invalid liandong application")
	}
	switch order.BusinessType {
	case LiandongBusinessTypeQuota:
		var topUp TopUp
		if err := lockForUpdate(tx).
			Where("trade_no = ? AND payment_provider = ?", order.LocalTradeNo, PaymentProviderLiandong).
			First(&topUp).Error; err != nil {
			return err
		}
		if topUp.UserId != order.UserID {
			return ErrPaymentMethodMismatch
		}
		if topUp.Status == common.TopUpStatusPending {
			return nil
		}
		if topUp.Status != common.TopUpStatusFailed {
			return ErrTopUpStatusInvalid
		}
		result := tx.Model(&TopUp{}).
			Where("id = ? AND status = ?", topUp.Id, common.TopUpStatusFailed).
			Updates(map[string]any{
				"status":        common.TopUpStatusPending,
				"complete_time": 0,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLiandongOrderBusy
		}
		return nil
	case LiandongBusinessTypeSubscription:
		var subscriptionOrder SubscriptionOrder
		if err := lockForUpdate(tx).
			Where("trade_no = ? AND payment_provider = ?", order.LocalTradeNo, PaymentProviderLiandong).
			First(&subscriptionOrder).Error; err != nil {
			return err
		}
		if subscriptionOrder.UserId != order.UserID {
			return ErrPaymentMethodMismatch
		}
		if subscriptionOrder.Status == common.TopUpStatusPending {
			return nil
		}
		if subscriptionOrder.Status != common.TopUpStatusFailed {
			return ErrSubscriptionOrderStatusInvalid
		}
		result := tx.Model(&SubscriptionOrder{}).
			Where("id = ? AND status = ?", subscriptionOrder.Id, common.TopUpStatusFailed).
			Updates(map[string]any{
				"status":        common.TopUpStatusPending,
				"complete_time": 0,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLiandongOrderBusy
		}
		return nil
	default:
		return errors.New("invalid liandong business type")
	}
}

func MarkLiandongFulfillmentFailure(localTradeNo string, consecutiveErrors int, lastError string, nextCheckAt int64) error {
	result := DB.Model(&LiandongOrder{}).
		Where("local_trade_no = ? AND payment_status = ?", localTradeNo, LiandongPaymentStatusPaid).
		Where("fulfillment_status <> ?", LiandongFulfillmentStatusFulfilled).
		Updates(map[string]any{
			"fulfillment_status":      LiandongFulfillmentStatusFailed,
			"consecutive_error_count": consecutiveErrors,
			"next_check_at":           nextCheckAt,
			"check_lock_until":        0,
			"last_error":              lastError,
			"updated_at":              common.GetTimestamp(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLiandongOrderBusy
	}
	return nil
}

func FulfillLiandongOrder(localTradeNo string) (*LiandongFulfillmentResult, error) {
	result := &LiandongFulfillmentResult{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order LiandongOrder
		if err := lockForUpdate(tx).Where("local_trade_no = ?", localTradeNo).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLiandongOrderNotFound
			}
			return err
		}
		result.UserID = order.UserID
		result.ProductName = order.ProductNameSnapshot
		if order.FulfillmentStatus == LiandongFulfillmentStatusFulfilled {
			result.AlreadyFulfilled = true
			return nil
		}
		if order.PaymentStatus != LiandongPaymentStatusPaid {
			return ErrLiandongOrderNotPaid
		}
		if order.FulfillmentStatus == LiandongFulfillmentStatusReviewRequired {
			return ErrLiandongOrderReviewRequired
		}

		var user User
		if err := lockForUpdate(tx).Where("id = ?", order.UserID).First(&user).Error; err != nil {
			return err
		}

		switch order.BusinessType {
		case LiandongBusinessTypeQuota:
			var snapshot LiandongQuotaSnapshot
			if err := common.UnmarshalJsonStr(order.FulfillmentSnapshot, &snapshot); err != nil {
				return err
			}
			if snapshot.QuotaAmount <= 0 || snapshot.QuotaAmount > math.MaxInt32 {
				return errors.New("invalid quota snapshot")
			}
			currentQuota := int64(user.Quota)
			if currentQuota < 0 || currentQuota > math.MaxInt32 ||
				snapshot.QuotaAmount > math.MaxInt32-currentQuota {
				return errors.New("quota fulfillment exceeds storage limit")
			}
			var topUp TopUp
			if err := lockForUpdate(tx).Where("trade_no = ?", order.LocalTradeNo).First(&topUp).Error; err != nil {
				return err
			}
			if topUp.UserId != order.UserID || topUp.PaymentProvider != PaymentProviderLiandong {
				return ErrPaymentMethodMismatch
			}
			if topUp.Status != common.TopUpStatusPending && topUp.Status != common.TopUpStatusSuccess {
				return ErrTopUpStatusInvalid
			}
			if topUp.Status == common.TopUpStatusPending {
				if err := tx.Model(&User{}).Where("id = ?", order.UserID).
					Update("quota", currentQuota+snapshot.QuotaAmount).Error; err != nil {
					return err
				}
				topUp.Status = common.TopUpStatusSuccess
				topUp.CompleteTime = common.GetTimestamp()
				if err := tx.Save(&topUp).Error; err != nil {
					return err
				}
				result.QuotaAdded = snapshot.QuotaAmount
			}
			result.Money = topUp.Money
		case LiandongBusinessTypeSubscription:
			var snapshot LiandongSubscriptionSnapshot
			if err := common.UnmarshalJsonStr(order.FulfillmentSnapshot, &snapshot); err != nil {
				return err
			}
			if snapshot.Plan.Id <= 0 || snapshot.Plan.Id != order.TargetID {
				return errors.New("invalid subscription snapshot")
			}
			var subscriptionOrder SubscriptionOrder
			if err := lockForUpdate(tx).Where("trade_no = ?", order.LocalTradeNo).First(&subscriptionOrder).Error; err != nil {
				return err
			}
			if subscriptionOrder.UserId != order.UserID || subscriptionOrder.PaymentProvider != PaymentProviderLiandong {
				return ErrPaymentMethodMismatch
			}
			if subscriptionOrder.Status != common.TopUpStatusPending &&
				subscriptionOrder.Status != common.TopUpStatusSuccess {
				return ErrSubscriptionOrderStatusInvalid
			}
			if subscriptionOrder.Status == common.TopUpStatusPending {
				if _, err := createUserSubscriptionFromPlanTx(
					tx,
					order.UserID,
					&snapshot.Plan,
					PaymentMethodLiandong,
					true,
				); err != nil {
					return err
				}
				subscriptionOrder.Status = common.TopUpStatusSuccess
				subscriptionOrder.CompleteTime = common.GetTimestamp()
				if err := upsertSubscriptionTopUpTx(tx, &subscriptionOrder); err != nil {
					return err
				}
				if err := tx.Save(&subscriptionOrder).Error; err != nil {
					return err
				}
			}
			result.UpgradeGroup = strings.TrimSpace(snapshot.Plan.UpgradeGroup)
			result.Money = subscriptionOrder.Money
		default:
			return errors.New("invalid business type")
		}

		now := common.GetTimestamp()
		return tx.Model(&LiandongOrder{}).Where("id = ?", order.ID).Updates(map[string]any{
			"fulfillment_status":      LiandongFulfillmentStatusFulfilled,
			"fulfilled_at":            now,
			"last_error":              "",
			"next_check_at":           0,
			"consecutive_error_count": 0,
			"check_lock_until":        0,
			"updated_at":              now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	if result.AlreadyFulfilled {
		return result, nil
	}

	if result.QuotaAdded > 0 {
		if err := cacheIncrUserQuota(result.UserID, result.QuotaAdded); err != nil {
			common.SysError("failed to update quota cache after liandong fulfillment: " + err.Error())
		}
		RecordTopupLog(
			result.UserID,
			fmt.Sprintf(
				"链动卡网充值成功，充值额度: %v，支付金额: %.2f",
				logger.FormatQuota(int(result.QuotaAdded)),
				result.Money,
			),
			"",
			PaymentMethodLiandong,
			PaymentProviderLiandong,
		)
	}
	if result.UpgradeGroup != "" {
		_ = UpdateUserGroupCache(result.UserID, result.UpgradeGroup)
	}
	if result.UserID > 0 && result.QuotaAdded == 0 {
		RecordLog(
			result.UserID,
			LogTypeTopup,
			fmt.Sprintf("链动卡网订阅购买成功，商品: %s，支付金额: %.2f", result.ProductName, result.Money),
		)
	}
	return result, nil
}
