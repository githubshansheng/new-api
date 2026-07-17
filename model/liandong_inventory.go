package model

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const LiandongInventoryBatchLimit = 1000

type LiandongProductThumbnail struct {
	ProductID   int    `json:"product_id" gorm:"primaryKey;autoIncrement:false"`
	ContentType string `json:"content_type" gorm:"type:varchar(32);not null"`
	Data        []byte `json:"-"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Size        int    `json:"size"`
	Version     int64  `json:"version" gorm:"type:bigint;not null"`
	CreatedAt   int64  `json:"created_at" gorm:"type:bigint"`
	UpdatedAt   int64  `json:"updated_at" gorm:"type:bigint"`
}

func (t *LiandongProductThumbnail) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if t.CreatedAt == 0 {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.Version == 0 {
		t.Version = now
	}
	return nil
}

func (t *LiandongProductThumbnail) BeforeUpdate(_ *gorm.DB) error {
	t.UpdatedAt = common.GetTimestamp()
	return nil
}

type LiandongProductInventoryCode struct {
	ID              int    `json:"id" gorm:"primaryKey"`
	ProductID       int    `json:"product_id" gorm:"not null;index:idx_liandong_inventory_product_status,priority:1"`
	Code            string `json:"code,omitempty" gorm:"type:char(32);not null;uniqueIndex"`
	Name            string `json:"name" gorm:"type:varchar(128);not null"`
	Status          string `json:"status" gorm:"type:varchar(32);not null;index:idx_liandong_inventory_product_status,priority:2"`
	ReservedOrderID int    `json:"reserved_order_id,omitempty" gorm:"index"`
	ReservedTradeNo string `json:"reserved_trade_no,omitempty" gorm:"type:varchar(128);index"`
	ReservedUserID  int    `json:"reserved_user_id,omitempty" gorm:"index"`
	ReservedAt      int64  `json:"reserved_at,omitempty" gorm:"type:bigint"`
	ConsumedAt      int64  `json:"consumed_at,omitempty" gorm:"type:bigint"`
	DisabledAt      int64  `json:"disabled_at,omitempty" gorm:"type:bigint"`
	CreatedBy       int    `json:"created_by"`
	CreatedAt       int64  `json:"created_at" gorm:"type:bigint"`
	UpdatedAt       int64  `json:"updated_at" gorm:"type:bigint"`
}

func (c *LiandongProductInventoryCode) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if c.CreatedAt == 0 {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	return nil
}

func (c *LiandongProductInventoryCode) BeforeUpdate(_ *gorm.DB) error {
	c.UpdatedAt = common.GetTimestamp()
	return nil
}

type LiandongInventorySummary struct {
	Available int64 `json:"available"`
	Reserved  int64 `json:"reserved"`
	Consumed  int64 `json:"consumed"`
	Disabled  int64 `json:"disabled"`
}

type LiandongPaidTransition struct {
	Order     *LiandongOrder
	NewlyPaid bool
	Late      bool
}

func reserveLiandongInventoryTx(tx *gorm.DB, order *LiandongOrder) error {
	if tx == nil || order == nil || order.ID <= 0 || order.ProductID <= 0 {
		return errors.New("invalid liandong inventory reservation")
	}

	var product LiandongProduct
	if err := lockForUpdate(tx).Where("id = ?", order.ProductID).First(&product).Error; err != nil {
		return err
	}
	inventoryMode := strings.TrimSpace(product.InventoryMode)
	if inventoryMode == "" {
		inventoryMode = LiandongInventoryModeUnlimited
	}
	if inventoryMode == LiandongInventoryModeUnlimited {
		order.InventoryCodeID = 0
		return tx.Model(&LiandongOrder{}).
			Where("id = ?", order.ID).
			Update("inventory_code_id", 0).Error
	}
	if inventoryMode != LiandongInventoryModeRedemptionCode {
		return errors.New("invalid liandong inventory mode")
	}

	var inventoryCode LiandongProductInventoryCode
	hasInventoryCode := false
	if order.InventoryCodeID > 0 {
		err := lockForUpdate(tx).
			Where("id = ? AND product_id = ?", order.InventoryCodeID, order.ProductID).
			First(&inventoryCode).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			switch {
			case inventoryCode.Status == LiandongInventoryStatusReserved &&
				inventoryCode.ReservedOrderID == order.ID:
				return nil
			case inventoryCode.Status == LiandongInventoryStatusConsumed &&
				inventoryCode.ReservedOrderID == order.ID:
				return nil
			case inventoryCode.Status == LiandongInventoryStatusAvailable:
				hasInventoryCode = true
			}
		}
	}
	if !hasInventoryCode {
		if err := lockForUpdate(tx).
			Where("product_id = ? AND status = ?", order.ProductID, LiandongInventoryStatusAvailable).
			Order("id asc").
			First(&inventoryCode).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLiandongInventoryUnavailable
			}
			return err
		}
	}

	now := common.GetTimestamp()
	result := tx.Model(&LiandongProductInventoryCode{}).
		Where(
			"id = ? AND product_id = ? AND status = ?",
			inventoryCode.ID,
			order.ProductID,
			LiandongInventoryStatusAvailable,
		).
		Updates(map[string]any{
			"status":            LiandongInventoryStatusReserved,
			"reserved_order_id": order.ID,
			"reserved_trade_no": order.LocalTradeNo,
			"reserved_user_id":  order.UserID,
			"reserved_at":       now,
			"updated_at":        now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLiandongInventoryUnavailable
	}
	if err := tx.Model(&LiandongOrder{}).
		Where("id = ?", order.ID).
		Update("inventory_code_id", inventoryCode.ID).Error; err != nil {
		return err
	}
	order.InventoryCodeID = inventoryCode.ID
	return nil
}

func UpdateLiandongProduct(product *LiandongProduct) error {
	if product == nil || product.ID <= 0 {
		return errors.New("invalid liandong product")
	}
	if err := ValidateLiandongProduct(product); err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var current LiandongProduct
		if err := lockForUpdate(tx).Where("id = ?", product.ID).First(&current).Error; err != nil {
			return err
		}

		var availableCount int64
		if err := tx.Model(&LiandongProductInventoryCode{}).
			Where("product_id = ? AND status = ?", product.ID, LiandongInventoryStatusAvailable).
			Count(&availableCount).Error; err != nil {
			return err
		}
		var reservedCount int64
		if err := tx.Model(&LiandongProductInventoryCode{}).
			Where("product_id = ? AND status = ?", product.ID, LiandongInventoryStatusReserved).
			Count(&reservedCount).Error; err != nil {
			return err
		}

		if product.InventoryMode == LiandongInventoryModeRedemptionCode {
			if availableCount+reservedCount > int64(product.InventoryCapacity) {
				return ErrLiandongInventoryCapacity
			}
		} else if reservedCount > 0 {
			return ErrLiandongOrderBusy
		} else if availableCount > 0 {
			now := common.GetTimestamp()
			if err := tx.Model(&LiandongProductInventoryCode{}).
				Where("product_id = ? AND status = ?", product.ID, LiandongInventoryStatusAvailable).
				Updates(map[string]any{
					"status":      LiandongInventoryStatusDisabled,
					"disabled_at": now,
					"updated_at":  now,
				}).Error; err != nil {
				return err
			}
		}

		product.CreatedAt = current.CreatedAt
		product.CreatedBy = current.CreatedBy
		product.ThumbnailVersion = current.ThumbnailVersion
		return tx.Save(product).Error
	})
}

func FindActiveLiandongOrderForUser(userID int) (*LiandongOrder, error) {
	var order LiandongOrder
	err := DB.Where("user_id = ? AND payment_status IN ?", userID, []string{
		LiandongPaymentStatusCreating,
		LiandongPaymentStatusPending,
		LiandongPaymentStatusCreateUnknown,
	}).
		Order("id desc").
		First(&order).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &order, nil
}

func FindExpiredLiandongOrders(limit int) ([]LiandongOrder, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	now := common.GetTimestamp()
	var orders []LiandongOrder
	err := DB.Where("expires_at > 0 AND expires_at <= ?", now).
		Where("payment_status IN ?", []string{
			LiandongPaymentStatusCreating,
			LiandongPaymentStatusPending,
			LiandongPaymentStatusCreateUnknown,
		}).
		Where("(check_lock_until = 0 OR check_lock_until < ?)", now).
		Order("expires_at asc, id asc").
		Limit(limit).
		Find(&orders).Error
	return orders, err
}

func FindExpiredLiandongOrdersWithoutProvider(limit int) ([]LiandongOrder, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	now := common.GetTimestamp()
	var orders []LiandongOrder
	err := DB.Where("expires_at > 0 AND expires_at <= ? AND provider_trade_no IS NULL", now).
		Where("payment_status IN ?", []string{
			LiandongPaymentStatusCreating,
			LiandongPaymentStatusPending,
			LiandongPaymentStatusCreateUnknown,
		}).
		Where("(check_lock_until = 0 OR check_lock_until < ?)", now).
		Order("expires_at asc, id asc").
		Limit(limit).
		Find(&orders).Error
	return orders, err
}

func ApplyLiandongPaidTradeNo(providerTradeNo string, providerSummary string) (*LiandongPaidTransition, error) {
	providerTradeNo = strings.TrimSpace(providerTradeNo)
	if providerTradeNo == "" {
		return nil, errors.New("provider trade number is required")
	}
	var current LiandongOrder
	if err := DB.Where("provider_trade_no = ?", providerTradeNo).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrLiandongOrderNotFound
		}
		return nil, err
	}
	if current.PaymentStatus == LiandongPaymentStatusPaid {
		return &LiandongPaidTransition{Order: &current}, nil
	}
	claimed, err := ClaimLiandongOrderByProviderTradeNo(providerTradeNo)
	if err != nil {
		if errors.Is(err, ErrLiandongOrderBusy) {
			if reloadErr := DB.Where("provider_trade_no = ?", providerTradeNo).
				First(&current).Error; reloadErr == nil &&
				current.PaymentStatus == LiandongPaymentStatusPaid {
				return &LiandongPaidTransition{Order: &current}, nil
			}
		}
		return nil, err
	}
	return ApplyClaimedLiandongPaidTradeNo(
		providerTradeNo,
		claimed.CheckLockUntil,
		providerSummary,
	)
}

func ApplyClaimedLiandongPaidTradeNo(
	providerTradeNo string,
	checkLockUntil int64,
	providerSummary string,
) (*LiandongPaidTransition, error) {
	providerTradeNo = strings.TrimSpace(providerTradeNo)
	if providerTradeNo == "" {
		return nil, errors.New("provider trade number is required")
	}
	if checkLockUntil <= 0 {
		return nil, ErrLiandongOrderBusy
	}
	transition := &LiandongPaidTransition{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order LiandongOrder
		if err := lockForUpdate(tx).
			Where("provider_trade_no = ?", providerTradeNo).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLiandongOrderNotFound
			}
			return err
		}
		transition.Order = &order
		now := common.GetTimestamp()
		if order.PaymentStatus != LiandongPaymentStatusPaid &&
			order.CheckLockUntil != checkLockUntil {
			return ErrLiandongOrderBusy
		}
		var newerOrderCount int64
		if order.PaymentStatus != LiandongPaymentStatusPaid {
			if err := tx.Model(&LiandongOrder{}).
				Where("user_id = ? AND id > ?", order.UserID, order.ID).
				Count(&newerOrderCount).Error; err != nil {
				return err
			}
		}
		if order.PaymentStatus != LiandongPaymentStatusPaid && newerOrderCount > 0 {
			result := tx.Model(&LiandongOrder{}).
				Where(
					"id = ? AND payment_status = ? AND check_lock_until = ?",
					order.ID,
					order.PaymentStatus,
					checkLockUntil,
				).
				Updates(map[string]any{
					"late_payment":            true,
					"fulfillment_status":      LiandongFulfillmentStatusReviewRequired,
					"paid_at":                 now,
					"last_check_at":           now,
					"next_check_at":           0,
					"check_count":             gorm.Expr("check_count + 1"),
					"consecutive_error_count": 0,
					"check_lock_until":        0,
					"provider_summary":        providerSummary,
					"last_error":              "payment completed after the inventory reservation was replaced",
					"updated_at":              now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrLiandongOrderBusy
			}
			order.LatePayment = true
			order.FulfillmentStatus = LiandongFulfillmentStatusReviewRequired
			order.PaidAt = now
			order.NextCheckAt = 0
			order.CheckLockUntil = 0
			transition.Late = true
			return nil
		}
		switch order.PaymentStatus {
		case LiandongPaymentStatusPaid:
			return nil
		case LiandongPaymentStatusPending, LiandongPaymentStatusCreateUnknown:
			if err := consumeLiandongInventoryTx(tx, &order, now); err != nil {
				return err
			}
			result := tx.Model(&LiandongOrder{}).
				Where(
					"id = ? AND payment_status = ? AND check_lock_until = ?",
					order.ID,
					order.PaymentStatus,
					checkLockUntil,
				).
				Updates(map[string]any{
					"payment_status":          LiandongPaymentStatusPaid,
					"paid_at":                 now,
					"last_check_at":           now,
					"next_check_at":           now,
					"check_count":             gorm.Expr("check_count + 1"),
					"consecutive_error_count": 0,
					"check_lock_until":        0,
					"provider_summary":        providerSummary,
					"last_error":              "",
					"updated_at":              now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrLiandongOrderBusy
			}
			order.PaymentStatus = LiandongPaymentStatusPaid
			order.PaidAt = now
			order.NextCheckAt = now
			order.CheckLockUntil = 0
			transition.NewlyPaid = true
			return nil
		default:
			result := tx.Model(&LiandongOrder{}).
				Where(
					"id = ? AND payment_status = ? AND check_lock_until = ?",
					order.ID,
					order.PaymentStatus,
					checkLockUntil,
				).
				Updates(map[string]any{
					"late_payment":       true,
					"fulfillment_status": LiandongFulfillmentStatusReviewRequired,
					"paid_at":            now,
					"last_check_at":      now,
					"check_count":        gorm.Expr("check_count + 1"),
					"check_lock_until":   0,
					"provider_summary":   providerSummary,
					"last_error":         "payment completed after the local order was closed",
					"updated_at":         now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrLiandongOrderBusy
			}
			order.LatePayment = true
			order.FulfillmentStatus = LiandongFulfillmentStatusReviewRequired
			order.PaidAt = now
			order.CheckLockUntil = 0
			transition.Late = true
			return nil
		}
	})
	if err != nil {
		return nil, err
	}
	return transition, nil
}

func CloseLiandongOrderForUser(userID int, localTradeNo string, reason string) error {
	if userID <= 0 {
		return ErrLiandongOrderNotFound
	}
	return closeLiandongOrder(localTradeNo, userID, reason)
}

func CloseClaimedLiandongOrder(
	localTradeNo string,
	userID int,
	checkLockUntil int64,
	providerSummary string,
	reason string,
) error {
	if checkLockUntil <= 0 {
		return ErrLiandongOrderBusy
	}
	return closeClaimedLiandongOrder(localTradeNo, userID, checkLockUntil, providerSummary, reason)
}

func closeLiandongOrder(localTradeNo string, userID int, reason string) error {
	return closeClaimedLiandongOrder(localTradeNo, userID, 0, "", reason)
}

func closeClaimedLiandongOrder(
	localTradeNo string,
	userID int,
	checkLockUntil int64,
	providerSummary string,
	reason string,
) error {
	now := common.GetTimestamp()
	return DB.Transaction(func(tx *gorm.DB) error {
		var order LiandongOrder
		query := lockForUpdate(tx).Where("local_trade_no = ?", localTradeNo)
		if userID > 0 {
			query = query.Where("user_id = ?", userID)
		}
		if err := query.First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLiandongOrderNotFound
			}
			return err
		}
		if checkLockUntil > 0 {
			if order.CheckLockUntil != checkLockUntil {
				return ErrLiandongOrderBusy
			}
		} else if order.CheckLockUntil >= now {
			return ErrLiandongOrderBusy
		}
		switch order.PaymentStatus {
		case LiandongPaymentStatusCreating,
			LiandongPaymentStatusPending,
			LiandongPaymentStatusCreateFailed,
			LiandongPaymentStatusCreateUnknown,
			LiandongPaymentStatusExpired,
			LiandongPaymentStatusReviewRequired:
		default:
			return ErrLiandongOrderNotFound
		}
		if order.FulfillmentStatus == LiandongFulfillmentStatusFulfilled {
			return ErrLiandongOrderBusy
		}
		if err := releaseLiandongInventoryTx(tx, &order, now); err != nil {
			return err
		}
		if err := failLiandongApplicationTx(tx, &order, now); err != nil {
			return err
		}
		updateQuery := tx.Model(&LiandongOrder{}).
			Where("id = ? AND payment_status = ?", order.ID, order.PaymentStatus)
		if checkLockUntil > 0 {
			updateQuery = updateQuery.Where("check_lock_until = ?", checkLockUntil)
		}
		updates := map[string]any{
			"payment_status":          LiandongPaymentStatusClosed,
			"fulfillment_status":      LiandongFulfillmentStatusFailed,
			"closed_reason":           strings.TrimSpace(reason),
			"next_check_at":           0,
			"check_lock_until":        0,
			"consecutive_error_count": 0,
			"last_error":              strings.TrimSpace(reason),
			"updated_at":              now,
		}
		if checkLockUntil > 0 {
			updates["last_check_at"] = now
			updates["check_count"] = gorm.Expr("check_count + 1")
			updates["provider_summary"] = providerSummary
		}
		result := updateQuery.Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLiandongOrderBusy
		}
		return nil
	})
}

func releaseLiandongInventoryTx(tx *gorm.DB, order *LiandongOrder, now int64) error {
	if order == nil || order.InventoryCodeID <= 0 {
		return nil
	}
	result := tx.Model(&LiandongProductInventoryCode{}).
		Where(
			"id = ? AND status = ? AND reserved_order_id = ?",
			order.InventoryCodeID,
			LiandongInventoryStatusReserved,
			order.ID,
		).
		Updates(map[string]any{
			"status":            LiandongInventoryStatusAvailable,
			"reserved_order_id": 0,
			"reserved_trade_no": "",
			"reserved_user_id":  0,
			"reserved_at":       0,
			"updated_at":        now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var code LiandongProductInventoryCode
	if err := tx.Where("id = ?", order.InventoryCodeID).First(&code).Error; err != nil {
		return err
	}
	if code.Status == LiandongInventoryStatusAvailable ||
		code.Status == LiandongInventoryStatusDisabled {
		return nil
	}
	return ErrLiandongOrderBusy
}

func consumeLiandongInventoryTx(tx *gorm.DB, order *LiandongOrder, now int64) error {
	if order == nil || order.InventoryCodeID <= 0 {
		return nil
	}
	result := tx.Model(&LiandongProductInventoryCode{}).
		Where(
			"id = ? AND status = ? AND reserved_order_id = ?",
			order.InventoryCodeID,
			LiandongInventoryStatusReserved,
			order.ID,
		).
		Updates(map[string]any{
			"status":      LiandongInventoryStatusConsumed,
			"consumed_at": now,
			"updated_at":  now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var code LiandongProductInventoryCode
	if err := tx.Where("id = ?", order.InventoryCodeID).First(&code).Error; err != nil {
		return err
	}
	if code.Status == LiandongInventoryStatusConsumed &&
		code.ReservedOrderID == order.ID {
		return nil
	}
	return ErrLiandongOrderReviewRequired
}

func PrepareLiandongLatePaymentFulfillment(localTradeNo string) (*LiandongOrder, error) {
	var prepared LiandongOrder
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order LiandongOrder
		if err := lockForUpdate(tx).
			Where("local_trade_no = ?", localTradeNo).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLiandongOrderNotFound
			}
			return err
		}
		if !order.LatePayment ||
			order.PaidAt <= 0 ||
			order.FulfillmentStatus != LiandongFulfillmentStatusReviewRequired ||
			order.FulfilledAt > 0 {
			return ErrLiandongOrderReviewRequired
		}
		if order.CheckLockUntil >= common.GetTimestamp() {
			return ErrLiandongOrderBusy
		}
		if err := reserveLiandongInventoryTx(tx, &order); err != nil {
			return err
		}
		if err := restoreLiandongApplicationTx(tx, &order); err != nil {
			return err
		}
		now := common.GetTimestamp()
		if err := consumeLiandongInventoryTx(tx, &order, now); err != nil {
			return err
		}
		result := tx.Model(&LiandongOrder{}).
			Where(
				"id = ? AND late_payment = ? AND paid_at > 0 AND fulfillment_status = ?",
				order.ID,
				true,
				LiandongFulfillmentStatusReviewRequired,
			).
			Updates(map[string]any{
				"payment_status":          LiandongPaymentStatusPaid,
				"fulfillment_status":      LiandongFulfillmentStatusWaiting,
				"next_check_at":           now,
				"check_lock_until":        0,
				"consecutive_error_count": 0,
				"last_error":              "",
				"updated_at":              now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLiandongOrderBusy
		}
		order.PaymentStatus = LiandongPaymentStatusPaid
		order.FulfillmentStatus = LiandongFulfillmentStatusWaiting
		order.NextCheckAt = now
		order.CheckLockUntil = 0
		order.LastError = ""
		prepared = order
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &prepared, nil
}

func GetLiandongInventorySummaries(productIDs []int) (map[int]LiandongInventorySummary, error) {
	summaries := make(map[int]LiandongInventorySummary, len(productIDs))
	if len(productIDs) == 0 {
		return summaries, nil
	}
	type inventoryCount struct {
		ProductID int
		Status    string
		Count     int64
	}
	var counts []inventoryCount
	if err := DB.Model(&LiandongProductInventoryCode{}).
		Select("product_id, status, COUNT(*) AS count").
		Where("product_id IN ?", productIDs).
		Group("product_id, status").
		Find(&counts).Error; err != nil {
		return nil, err
	}
	for _, count := range counts {
		summary := summaries[count.ProductID]
		switch count.Status {
		case LiandongInventoryStatusAvailable:
			summary.Available = count.Count
		case LiandongInventoryStatusReserved:
			summary.Reserved = count.Count
		case LiandongInventoryStatusConsumed:
			summary.Consumed = count.Count
		case LiandongInventoryStatusDisabled:
			summary.Disabled = count.Count
		}
		summaries[count.ProductID] = summary
	}
	return summaries, nil
}

func AddLiandongInventoryCodes(productID int, count int, name string, createdBy int) ([]string, error) {
	if productID <= 0 || count <= 0 || count > LiandongInventoryBatchLimit {
		return nil, errors.New("inventory code count must be between 1 and 1000")
	}
	keys := make([]string, 0, count)
	err := DB.Transaction(func(tx *gorm.DB) error {
		var product LiandongProduct
		if err := lockForUpdate(tx).Where("id = ?", productID).First(&product).Error; err != nil {
			return err
		}
		if product.InventoryMode != LiandongInventoryModeRedemptionCode {
			return errors.New("product does not use redemption code inventory")
		}
		var activeCount int64
		if err := tx.Model(&LiandongProductInventoryCode{}).
			Where("product_id = ? AND status IN ?", productID, []string{
				LiandongInventoryStatusAvailable,
				LiandongInventoryStatusReserved,
			}).
			Count(&activeCount).Error; err != nil {
			return err
		}
		if product.InventoryCapacity <= 0 || activeCount+int64(count) > int64(product.InventoryCapacity) {
			return ErrLiandongInventoryCapacity
		}
		codeName := strings.TrimSpace(name)
		if codeName == "" {
			codeName = product.Name
		}
		codes := make([]LiandongProductInventoryCode, 0, count)
		for index := 0; index < count; index++ {
			key := common.GetUUID()
			keys = append(keys, key)
			codes = append(codes, LiandongProductInventoryCode{
				ProductID: productID,
				Code:      key,
				Name:      codeName,
				Status:    LiandongInventoryStatusAvailable,
				CreatedBy: createdBy,
			})
		}
		return tx.Create(&codes).Error
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func DisableLiandongAvailableInventoryCodes(productID int, count int) error {
	if productID <= 0 || count <= 0 || count > LiandongInventoryBatchLimit {
		return errors.New("inventory reduction count must be between 1 and 1000")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var product LiandongProduct
		if err := lockForUpdate(tx).Select("id").Where("id = ?", productID).First(&product).Error; err != nil {
			return err
		}
		var codes []LiandongProductInventoryCode
		if err := lockForUpdate(tx).
			Where("product_id = ? AND status = ?", productID, LiandongInventoryStatusAvailable).
			Order("id desc").
			Limit(count).
			Find(&codes).Error; err != nil {
			return err
		}
		if len(codes) != count {
			return ErrLiandongInventoryUnavailable
		}
		ids := make([]int, 0, len(codes))
		for _, code := range codes {
			ids = append(ids, code.ID)
		}
		now := common.GetTimestamp()
		result := tx.Model(&LiandongProductInventoryCode{}).
			Where("id IN ? AND status = ?", ids, LiandongInventoryStatusAvailable).
			Updates(map[string]any{
				"status":      LiandongInventoryStatusDisabled,
				"disabled_at": now,
				"updated_at":  now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(count) {
			return ErrLiandongOrderBusy
		}
		return nil
	})
}

func SaveLiandongProductThumbnail(productID int, contentType string, data []byte, width int, height int) error {
	if productID <= 0 || len(data) == 0 {
		return errors.New("invalid product thumbnail")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var product LiandongProduct
		if err := lockForUpdate(tx).Where("id = ?", productID).First(&product).Error; err != nil {
			return err
		}
		now := common.GetTimestamp()
		version := time.Now().UnixNano()
		thumbnail := LiandongProductThumbnail{
			ProductID:   productID,
			ContentType: contentType,
			Data:        data,
			Width:       width,
			Height:      height,
			Size:        len(data),
			Version:     version,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Where("product_id = ?", productID).Delete(&LiandongProductThumbnail{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&thumbnail).Error; err != nil {
			return err
		}
		return tx.Model(&LiandongProduct{}).
			Where("id = ?", productID).
			Updates(map[string]any{
				"thumbnail_version": version,
				"updated_at":        now,
			}).Error
	})
}

func GetLiandongProductThumbnail(productID int) (*LiandongProductThumbnail, error) {
	var thumbnail LiandongProductThumbnail
	if err := DB.Where("product_id = ?", productID).First(&thumbnail).Error; err != nil {
		return nil, err
	}
	return &thumbnail, nil
}

func DeleteLiandongProductThumbnail(productID int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("product_id = ?", productID).Delete(&LiandongProductThumbnail{}).Error; err != nil {
			return err
		}
		return tx.Model(&LiandongProduct{}).
			Where("id = ?", productID).
			Updates(map[string]any{
				"thumbnail_version": 0,
				"updated_at":        common.GetTimestamp(),
			}).Error
	})
}
