package controller

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	_ "golang.org/x/image/webp"
	"gorm.io/gorm"
)

const (
	liandongThumbnailMaxBytes     = 512 * 1024
	liandongThumbnailMaxDimension = 2048
)

var liandongProxyValidator = service.ValidateLiandongProxy

type liandongCreateOrderRequest struct {
	ProductID int `json:"product_id"`
}

type liandongSubscriptionSpec struct {
	Title                   string `json:"title"`
	DurationUnit            string `json:"duration_unit"`
	DurationValue           int    `json:"duration_value"`
	CustomSeconds           int64  `json:"custom_seconds"`
	TotalAmount             int64  `json:"total_amount"`
	QuotaResetPeriod        string `json:"quota_reset_period"`
	QuotaResetCustomSeconds int64  `json:"quota_reset_custom_seconds"`
	UpgradeGroup            string `json:"upgrade_group"`
}

type liandongPublicProduct struct {
	ID                  int                       `json:"id"`
	BusinessType        string                    `json:"business_type"`
	Name                string                    `json:"name"`
	QuotaAmount         int64                     `json:"quota_amount"`
	PlanID              int                       `json:"plan_id"`
	ExpectedAmountMinor int64                     `json:"expected_amount_minor"`
	Currency            string                    `json:"currency"`
	ThumbnailURL        string                    `json:"thumbnail_url,omitempty"`
	ThumbnailVersion    int64                     `json:"thumbnail_version,omitempty"`
	InventoryLevel      string                    `json:"inventory_level"`
	Subscription        *liandongSubscriptionSpec `json:"subscription,omitempty"`
}

type liandongRootProductView struct {
	ID                  int                       `json:"id"`
	BusinessType        string                    `json:"business_type"`
	GoodsType           string                    `json:"goods_type"`
	Name                string                    `json:"name"`
	GoodsKey            string                    `json:"goods_key"`
	QuotaAmount         int64                     `json:"quota_amount"`
	PlanID              int                       `json:"plan_id"`
	ExpectedAmountMinor int64                     `json:"expected_amount_minor"`
	Currency            string                    `json:"currency"`
	InventoryMode       string                    `json:"inventory_mode"`
	InventoryCapacity   int                       `json:"inventory_capacity"`
	InventoryAvailable  int64                     `json:"inventory_available"`
	InventoryReserved   int64                     `json:"inventory_reserved"`
	InventoryConsumed   int64                     `json:"inventory_consumed"`
	InventoryDisabled   int64                     `json:"inventory_disabled"`
	InventoryLevel      string                    `json:"inventory_level"`
	ThumbnailURL        string                    `json:"thumbnail_url,omitempty"`
	ThumbnailVersion    int64                     `json:"thumbnail_version,omitempty"`
	Subscription        *liandongSubscriptionSpec `json:"subscription,omitempty"`
	Enabled             bool                      `json:"enabled"`
	SortOrder           int                       `json:"sort_order"`
	CreatedBy           int                       `json:"created_by"`
	UpdatedBy           int                       `json:"updated_by"`
	CreatedAt           int64                     `json:"created_at"`
	UpdatedAt           int64                     `json:"updated_at"`
}

type liandongRootOrderView struct {
	LocalTradeNo        string  `json:"local_trade_no"`
	ProviderTradeNo     *string `json:"provider_trade_no,omitempty"`
	UserID              int     `json:"user_id"`
	ProductID           int     `json:"product_id"`
	ProductName         string  `json:"product_name"`
	BusinessType        string  `json:"business_type"`
	TargetID            int     `json:"target_id"`
	InventoryCodeID     int     `json:"inventory_code_id,omitempty"`
	ExpectedAmountMinor int64   `json:"expected_amount_minor"`
	Currency            string  `json:"currency"`
	PaymentStatus       string  `json:"payment_status"`
	FulfillmentStatus   string  `json:"fulfillment_status"`
	LastCheckAt         int64   `json:"last_check_at"`
	NextCheckAt         int64   `json:"next_check_at"`
	CheckCount          int     `json:"check_count"`
	ConsecutiveErrors   int     `json:"consecutive_error_count"`
	LastError           string  `json:"last_error,omitempty"`
	ExpiresAt           int64   `json:"expires_at"`
	ClosedReason        string  `json:"closed_reason,omitempty"`
	LatePayment         bool    `json:"late_payment"`
	PaidAt              int64   `json:"paid_at"`
	FulfilledAt         int64   `json:"fulfilled_at"`
	CreatedAt           int64   `json:"created_at"`
	UpdatedAt           int64   `json:"updated_at"`
}

func ListLiandongProducts(c *gin.Context) {
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !operation_setting.IsPaymentComplianceConfirmed() ||
		!settingsSnapshot.Enabled || !settingsSnapshot.CreateEnabled {
		common.ApiSuccess(c, []liandongPublicProduct{})
		return
	}
	products, err := model.ListEnabledLiandongProducts()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	productIDs := make([]int, 0, len(products))
	for _, product := range products {
		productIDs = append(productIDs, product.ID)
	}
	summaries, err := model.GetLiandongInventorySummaries(productIDs)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	views := make([]liandongPublicProduct, 0, len(products))
	for _, product := range products {
		summary := summaries[product.ID]
		view := liandongPublicProduct{
			ID:                  product.ID,
			BusinessType:        product.BusinessType,
			Name:                product.Name,
			QuotaAmount:         product.QuotaAmount,
			PlanID:              product.PlanID,
			ExpectedAmountMinor: product.ExpectedAmountMinor,
			Currency:            product.Currency,
			ThumbnailURL:        liandongThumbnailURL(&product),
			ThumbnailVersion:    product.ThumbnailVersion,
			InventoryLevel:      liandongInventoryLevel(&product, summary),
		}
		if product.BusinessType == model.LiandongBusinessTypeSubscription {
			view.Subscription = getLiandongSubscriptionSpec(product.PlanID)
		}
		views = append(views, view)
	}
	common.ApiSuccess(c, views)
}

func GetLiandongProductThumbnail(c *gin.Context) {
	productID, err := strconv.Atoi(c.Param("id"))
	if err != nil || productID <= 0 {
		common.ApiErrorMsg(c, "Invalid product ID")
		return
	}
	thumbnail, err := model.GetLiandongProductThumbnail(productID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.Status(http.StatusNotFound)
			return
		}
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=86400, immutable")
	c.Header("ETag", fmt.Sprintf(`"%d"`, thumbnail.Version))
	c.Data(http.StatusOK, thumbnail.ContentType, thumbnail.Data)
}

func CreateLiandongOrder(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}
	var req liandongCreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ProductID <= 0 {
		common.ApiErrorMsg(c, "Invalid request")
		return
	}
	view, err := service.CreateLiandongPayment(c.Request.Context(), c.GetInt("id"), req.ProductID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func GetLiandongOrder(c *gin.Context) {
	view, err := service.RefreshLiandongPaymentForUser(
		c.Request.Context(),
		c.GetInt("id"),
		c.Param("local_trade_no"),
	)
	if err != nil {
		if errors.Is(err, model.ErrLiandongOrderNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Payment order not found"})
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func CloseLiandongOrderForUser(c *gin.Context) {
	view, err := service.CloseLiandongPaymentForUser(
		c.Request.Context(),
		c.GetInt("id"),
		c.Param("local_trade_no"),
	)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrLiandongOrderNotFound):
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Payment order not found"})
		case errors.Is(err, model.ErrLiandongOrderBusy):
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Payment order cannot be closed"})
		default:
			common.ApiError(c, err)
		}
		return
	}
	common.ApiSuccess(c, view)
}

type liandongSettingsUpdateRequest struct {
	Enabled                   *bool   `json:"enabled"`
	CreateEnabled             *bool   `json:"create_enabled"`
	ReconcileEnabled          *bool   `json:"reconcile_enabled"`
	FulfillEnabled            *bool   `json:"fulfill_enabled"`
	IframeEnabled             *bool   `json:"iframe_enabled"`
	BaseURL                   *string `json:"base_url"`
	ProxyEnabled              *bool   `json:"proxy_enabled"`
	ProxyURL                  *string `json:"proxy_url"`
	ProxyUsername             *string `json:"proxy_username"`
	ProxyPassword             *string `json:"proxy_password"`
	ProxyTimeoutSeconds       *int    `json:"proxy_timeout_seconds"`
	PollIntervalSeconds       *int    `json:"poll_interval_seconds"`
	ClientPollIntervalSeconds *int    `json:"client_poll_interval_seconds"`
	ReconcileBatchSize        *int    `json:"reconcile_batch_size"`
	PaymentTimeoutMinutes     *int    `json:"payment_timeout_minutes"`
	JUUID                     *string `json:"juuid"`
	AuthMode                  *string `json:"auth_mode"`
	Username                  *string `json:"username"`
	Password                  *string `json:"password"`
	MerchantToken             *string `json:"merchant_token"`
	ClearUsername             bool    `json:"clear_username"`
	ClearPassword             bool    `json:"clear_password"`
	ClearToken                bool    `json:"clear_token"`
	ClearProxyUsername        bool    `json:"clear_proxy_username"`
	ClearProxyPassword        bool    `json:"clear_proxy_password"`
}

func GetLiandongSettings(c *gin.Context) {
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	proxyURL := settingsSnapshot.ProxyURL
	if proxyURL != "" &&
		strings.TrimSpace(settingsSnapshot.ProxyUsername) != "" &&
		settingsSnapshot.ProxyPassword != "" {
		if parsedProxyURL, parseErr := url.Parse(proxyURL); parseErr == nil {
			parsedProxyURL.User = url.UserPassword(
				settingsSnapshot.ProxyUsername,
				settingsSnapshot.ProxyPassword,
			)
			proxyURL = parsedProxyURL.String()
		}
	}
	common.ApiSuccess(c, gin.H{
		"enabled":                      settingsSnapshot.Enabled,
		"create_enabled":               settingsSnapshot.CreateEnabled,
		"reconcile_enabled":            settingsSnapshot.ReconcileEnabled,
		"fulfill_enabled":              settingsSnapshot.FulfillEnabled,
		"iframe_enabled":               settingsSnapshot.IframeEnabled,
		"base_url":                     settingsSnapshot.BaseURL,
		"proxy_enabled":                settingsSnapshot.ProxyEnabled,
		"proxy_url":                    proxyURL,
		"proxy_username_configured":    strings.TrimSpace(settingsSnapshot.ProxyUsername) != "",
		"proxy_password_configured":    settingsSnapshot.ProxyPassword != "",
		"proxy_timeout_seconds":        settingsSnapshot.ProxyTimeoutSeconds,
		"poll_interval_seconds":        settingsSnapshot.PollIntervalSeconds,
		"client_poll_interval_seconds": settingsSnapshot.ClientPollIntervalSeconds,
		"reconcile_batch_size":         settingsSnapshot.ReconcileBatchSize,
		"payment_timeout_minutes":      settingsSnapshot.PaymentTimeoutMinutes,
		"juuid":                        settingsSnapshot.JUUID,
		"auth_mode":                    settingsSnapshot.AuthMode,
		"username_configured":          strings.TrimSpace(settingsSnapshot.Username) != "",
		"password_configured":          settingsSnapshot.Password != "",
		"merchant_token_configured":    strings.TrimSpace(settingsSnapshot.MerchantToken) != "",
	})
}

func UpdateLiandongSettings(c *gin.Context) {
	var req liandongSettingsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "Invalid request")
		return
	}
	current, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	updated := current
	values := map[string]string{}
	enablingGateway := req.Enabled != nil && *req.Enabled && !current.Enabled
	if enablingGateway {
		defaultEnabled := true
		if req.CreateEnabled == nil {
			req.CreateEnabled = &defaultEnabled
		}
		if req.ReconcileEnabled == nil {
			req.ReconcileEnabled = &defaultEnabled
		}
		if req.FulfillEnabled == nil {
			req.FulfillEnabled = &defaultEnabled
		}
		if req.IframeEnabled == nil {
			req.IframeEnabled = &defaultEnabled
		}
	}
	setBool := func(key string, input *bool, target *bool) {
		if input == nil {
			return
		}
		*target = *input
		values[key] = strconv.FormatBool(*input)
	}
	setBool("LiandongEnabled", req.Enabled, &updated.Enabled)
	setBool("LiandongCreateEnabled", req.CreateEnabled, &updated.CreateEnabled)
	setBool("LiandongReconcileEnabled", req.ReconcileEnabled, &updated.ReconcileEnabled)
	setBool("LiandongFulfillEnabled", req.FulfillEnabled, &updated.FulfillEnabled)
	setBool("LiandongIframeEnabled", req.IframeEnabled, &updated.IframeEnabled)
	setBool("LiandongProxyEnabled", req.ProxyEnabled, &updated.ProxyEnabled)

	proxyCredentialsChanged := false
	if req.BaseURL != nil {
		normalized, err := setting.NormalizeLiandongBaseURL(*req.BaseURL)
		if err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		updated.BaseURL = normalized
		values["LiandongBaseURL"] = normalized
	}
	if req.ProxyURL != nil {
		proxyConfig, err := setting.ParseLiandongProxy(*req.ProxyURL)
		if err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		if proxyConfig.Username != "" {
			if req.ProxyUsername != nil ||
				req.ProxyPassword != nil ||
				req.ClearProxyUsername ||
				req.ClearProxyPassword {
				common.ApiErrorMsg(c, "Proxy credentials must be provided either in the URL or in the separate fields")
				return
			}
			updated.ProxyUsername = proxyConfig.Username
			updated.ProxyPassword = proxyConfig.Password
			values["LiandongProxyUsername"] = proxyConfig.Username
			values["LiandongProxyPassword"] = proxyConfig.Password
			proxyCredentialsChanged = true
		} else if req.ProxyUsername == nil &&
			req.ProxyPassword == nil &&
			!req.ClearProxyUsername &&
			!req.ClearProxyPassword {
			updated.ProxyUsername = ""
			updated.ProxyPassword = ""
			values["LiandongProxyUsername"] = ""
			values["LiandongProxyPassword"] = ""
			proxyCredentialsChanged = true
		}
		updated.ProxyURL = proxyConfig.URL
		values["LiandongProxyURL"] = proxyConfig.URL
	}

	if req.ClearProxyUsername {
		updated.ProxyUsername = ""
		values["LiandongProxyUsername"] = ""
		proxyCredentialsChanged = true
	} else if req.ProxyUsername != nil {
		username := strings.TrimSpace(*req.ProxyUsername)
		if username == "" || len(username) > 128 {
			common.ApiErrorMsg(c, "Invalid proxy username")
			return
		}
		updated.ProxyUsername = username
		values["LiandongProxyUsername"] = username
		proxyCredentialsChanged = true
	}
	if req.ClearProxyPassword {
		updated.ProxyPassword = ""
		values["LiandongProxyPassword"] = ""
		proxyCredentialsChanged = true
	} else if req.ProxyPassword != nil {
		password := *req.ProxyPassword
		if password == "" || len(password) > 256 {
			common.ApiErrorMsg(c, "Invalid proxy password")
			return
		}
		updated.ProxyPassword = password
		values["LiandongProxyPassword"] = password
		proxyCredentialsChanged = true
	}

	if req.ProxyTimeoutSeconds != nil {
		if *req.ProxyTimeoutSeconds < setting.MinLiandongProxyTimeoutSeconds ||
			*req.ProxyTimeoutSeconds > setting.MaxLiandongProxyTimeoutSeconds {
			common.ApiErrorMsg(c, "Proxy timeout must be between 5 and 300 seconds")
			return
		}
		updated.ProxyTimeoutSeconds = *req.ProxyTimeoutSeconds
		values["LiandongProxyTimeoutSeconds"] = strconv.Itoa(*req.ProxyTimeoutSeconds)
	}

	if req.PollIntervalSeconds != nil {
		if *req.PollIntervalSeconds < setting.MinLiandongPollIntervalSeconds ||
			*req.PollIntervalSeconds > setting.MaxLiandongPollIntervalSeconds {
			common.ApiErrorMsg(c, "Verification interval must be between 1 and 3600 seconds")
			return
		}
		updated.PollIntervalSeconds = *req.PollIntervalSeconds
		values["LiandongPollIntervalSeconds"] = strconv.Itoa(*req.PollIntervalSeconds)
	}
	if req.ClientPollIntervalSeconds != nil {
		if *req.ClientPollIntervalSeconds < setting.MinLiandongClientPollIntervalSeconds ||
			*req.ClientPollIntervalSeconds > setting.MaxLiandongClientPollIntervalSeconds {
			common.ApiErrorMsg(c, "Client polling interval must be between 1 and 60 seconds")
			return
		}
		updated.ClientPollIntervalSeconds = *req.ClientPollIntervalSeconds
		values["LiandongClientPollIntervalSeconds"] = strconv.Itoa(*req.ClientPollIntervalSeconds)
	}
	if req.ReconcileBatchSize != nil {
		if *req.ReconcileBatchSize < setting.MinLiandongReconcileBatchSize ||
			*req.ReconcileBatchSize > setting.MaxLiandongReconcileBatchSize {
			common.ApiErrorMsg(c, "Verification batch size must be between 1 and 500")
			return
		}
		updated.ReconcileBatchSize = *req.ReconcileBatchSize
		values["LiandongReconcileBatchSize"] = strconv.Itoa(*req.ReconcileBatchSize)
	}
	if req.PaymentTimeoutMinutes != nil {
		if *req.PaymentTimeoutMinutes < setting.MinLiandongPaymentTimeoutMinutes ||
			*req.PaymentTimeoutMinutes > setting.MaxLiandongPaymentTimeoutMinutes {
			common.ApiErrorMsg(c, "Payment timeout must be between 1 and 1440 minutes")
			return
		}
		updated.PaymentTimeoutMinutes = *req.PaymentTimeoutMinutes
		values["LiandongPaymentTimeoutMinutes"] = strconv.Itoa(*req.PaymentTimeoutMinutes)
	}
	if req.JUUID != nil {
		updated.JUUID = strings.TrimSpace(*req.JUUID)
		if len(updated.JUUID) > 128 {
			common.ApiErrorMsg(c, "Invalid JUUID")
			return
		}
		values["LiandongJUUID"] = updated.JUUID
	}
	if req.AuthMode != nil {
		updated.AuthMode = strings.TrimSpace(*req.AuthMode)
		if updated.AuthMode != setting.LiandongAuthModeManualToken &&
			updated.AuthMode != setting.LiandongAuthModeCredentials {
			common.ApiErrorMsg(c, "Invalid authentication mode")
			return
		}
		values["LiandongAuthMode"] = updated.AuthMode
	}

	credentialsChanged := false
	if req.ClearUsername {
		updated.Username = ""
		values["LiandongUsername"] = ""
		credentialsChanged = true
	} else if req.Username != nil {
		username := strings.TrimSpace(*req.Username)
		if username == "" || len(username) > 128 {
			common.ApiErrorMsg(c, "Invalid Liandong username")
			return
		}
		updated.Username = username
		values["LiandongUsername"] = username
		credentialsChanged = true
	}
	if req.ClearPassword {
		updated.Password = ""
		values["LiandongPassword"] = ""
		credentialsChanged = true
	} else if req.Password != nil {
		password := *req.Password
		if password == "" || len(password) > 256 {
			common.ApiErrorMsg(c, "Invalid Liandong password")
			return
		}
		updated.Password = password
		values["LiandongPassword"] = password
		credentialsChanged = true
	}

	tokenChanged := false
	if req.ClearToken {
		updated.MerchantToken = ""
		values["LiandongMerchantToken"] = ""
		tokenChanged = true
	} else if req.MerchantToken != nil {
		token := strings.TrimSpace(*req.MerchantToken)
		if token == "" || len(token) > 512 {
			common.ApiErrorMsg(c, "Invalid merchant token")
			return
		}
		updated.MerchantToken = token
		values["LiandongMerchantToken"] = token
		tokenChanged = true
	}

	if updated.AuthMode == setting.LiandongAuthModeCredentials {
		if req.MerchantToken != nil {
			common.ApiErrorMsg(c, "Merchant token cannot be entered in credentials mode")
			return
		}
		if credentialsChanged || current.AuthMode != setting.LiandongAuthModeCredentials {
			updated.MerchantToken = ""
			values["LiandongMerchantToken"] = ""
			tokenChanged = true
		}
	} else {
		if current.AuthMode == setting.LiandongAuthModeCredentials && req.MerchantToken == nil {
			updated.MerchantToken = ""
			values["LiandongMerchantToken"] = ""
			tokenChanged = true
		}
		if req.Username != nil || req.Password != nil {
			common.ApiErrorMsg(c, "Username and password require credentials mode")
			return
		}
		updated.Username = ""
		updated.Password = ""
		values["LiandongUsername"] = ""
		values["LiandongPassword"] = ""
	}

	if updated.Enabled && updated.CreateEnabled && updated.JUUID == "" {
		common.ApiErrorMsg(c, "JUUID is required before order creation can be enabled")
		return
	}
	if updated.Enabled && updated.ReconcileEnabled {
		if updated.AuthMode == setting.LiandongAuthModeCredentials {
			if updated.Username == "" || updated.Password == "" {
				common.ApiErrorMsg(c, "Username and password are required for credentials mode")
				return
			}
		} else if updated.MerchantToken == "" {
			common.ApiErrorMsg(c, "Merchant token is required for manual token mode")
			return
		}
	}
	if updated.ProxyEnabled {
		if updated.ProxyURL == "" {
			common.ApiErrorMsg(c, "Proxy URL is required when the proxy is enabled")
			return
		}
		hasProxyUsername := strings.TrimSpace(updated.ProxyUsername) != ""
		hasProxyPassword := updated.ProxyPassword != ""
		if hasProxyUsername != hasProxyPassword {
			common.ApiErrorMsg(c, "Proxy username and password must be configured together")
			return
		}
	}
	emergencyDisable := req.Enabled != nil && !*req.Enabled
	disablingProxy := req.ProxyEnabled != nil && !*req.ProxyEnabled
	proxyConfigurationChanged := enablingGateway ||
		req.BaseURL != nil ||
		req.ProxyEnabled != nil ||
		req.ProxyURL != nil ||
		req.ProxyTimeoutSeconds != nil ||
		proxyCredentialsChanged
	if !emergencyDisable && !disablingProxy && updated.ProxyURL != "" && proxyConfigurationChanged {
		if err := liandongProxyValidator(c.Request.Context(), updated); err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
	}
	if len(values) > 0 {
		if err := model.UpdateOptionsBulk(values); err != nil {
			common.ApiError(c, err)
			return
		}
	}

	updatedKeys := make([]string, 0, len(values))
	for key := range values {
		if key == "LiandongMerchantToken" ||
			key == "LiandongUsername" ||
			key == "LiandongPassword" ||
			key == "LiandongProxyUsername" ||
			key == "LiandongProxyPassword" {
			continue
		}
		updatedKeys = append(updatedKeys, key)
	}
	sort.Strings(updatedKeys)
	recordManageAudit(c, "liandong.settings.update", map[string]interface{}{
		"updated_keys":              updatedKeys,
		"credentials_changed":       credentialsChanged,
		"token_changed":             tokenChanged,
		"proxy_credentials_changed": proxyCredentialsChanged,
	})
	service.WakeSystemTaskRunner()
	common.ApiSuccess(c, nil)
}

func RootListLiandongProducts(c *gin.Context) {
	products, err := model.ListAllLiandongProducts()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	productIDs := make([]int, 0, len(products))
	for _, product := range products {
		productIDs = append(productIDs, product.ID)
	}
	summaries, err := model.GetLiandongInventorySummaries(productIDs)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	views := make([]liandongRootProductView, 0, len(products))
	for _, product := range products {
		views = append(views, makeLiandongRootProductView(product, summaries[product.ID]))
	}
	common.ApiSuccess(c, views)
}

type liandongProductRequest struct {
	BusinessType        string `json:"business_type"`
	GoodsType           string `json:"goods_type"`
	Name                string `json:"name"`
	GoodsKey            string `json:"goods_key"`
	QuotaAmount         int64  `json:"quota_amount"`
	PlanID              int    `json:"plan_id"`
	ExpectedAmountMinor int64  `json:"expected_amount_minor"`
	Currency            string `json:"currency"`
	InventoryMode       string `json:"inventory_mode"`
	InventoryCapacity   int    `json:"inventory_capacity"`
	Enabled             bool   `json:"enabled"`
	SortOrder           int    `json:"sort_order"`
}

func RootCreateLiandongProduct(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}
	var req liandongProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "Invalid request")
		return
	}
	product := model.LiandongProduct{
		BusinessType:        req.BusinessType,
		GoodsType:           req.GoodsType,
		Name:                req.Name,
		GoodsKey:            req.GoodsKey,
		QuotaAmount:         req.QuotaAmount,
		PlanID:              req.PlanID,
		ExpectedAmountMinor: req.ExpectedAmountMinor,
		Currency:            req.Currency,
		InventoryMode:       req.InventoryMode,
		InventoryCapacity:   req.InventoryCapacity,
		Enabled:             req.Enabled,
		SortOrder:           req.SortOrder,
		CreatedBy:           c.GetInt("id"),
		UpdatedBy:           c.GetInt("id"),
	}
	if err := validateLiandongProductTarget(&product); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Create(&product).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "liandong.product.create", map[string]interface{}{"product_id": product.ID})
	common.ApiSuccess(c, makeLiandongRootProductView(product, model.LiandongInventorySummary{}))
}

func RootUpdateLiandongProduct(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "Invalid product ID")
		return
	}
	var req liandongProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "Invalid request")
		return
	}
	product := model.LiandongProduct{
		ID:                  id,
		BusinessType:        req.BusinessType,
		GoodsType:           req.GoodsType,
		Name:                req.Name,
		GoodsKey:            req.GoodsKey,
		QuotaAmount:         req.QuotaAmount,
		PlanID:              req.PlanID,
		ExpectedAmountMinor: req.ExpectedAmountMinor,
		Currency:            req.Currency,
		InventoryMode:       req.InventoryMode,
		InventoryCapacity:   req.InventoryCapacity,
		Enabled:             req.Enabled,
		SortOrder:           req.SortOrder,
		UpdatedBy:           c.GetInt("id"),
	}
	if err := validateLiandongProductTarget(&product); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateLiandongProduct(&product); err != nil {
		common.ApiError(c, err)
		return
	}
	summaries, err := model.GetLiandongInventorySummaries([]int{id})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "liandong.product.update", map[string]interface{}{"product_id": product.ID})
	common.ApiSuccess(c, makeLiandongRootProductView(product, summaries[id]))
}

func validateLiandongProductTarget(product *model.LiandongProduct) error {
	if err := model.ValidateLiandongProduct(product); err != nil {
		return err
	}
	if product.BusinessType != model.LiandongBusinessTypeSubscription {
		return nil
	}
	plan, err := model.GetSubscriptionPlanById(product.PlanID)
	if err != nil {
		return errors.New("subscription plan not found")
	}
	if !plan.Enabled {
		return errors.New("subscription plan is disabled")
	}
	return nil
}

type liandongInventoryChangeRequest struct {
	Count int    `json:"count"`
	Name  string `json:"name"`
}

func RootAddLiandongInventory(c *gin.Context) {
	productID, err := strconv.Atoi(c.Param("id"))
	if err != nil || productID <= 0 {
		common.ApiErrorMsg(c, "Invalid product ID")
		return
	}
	var req liandongInventoryChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil ||
		req.Count <= 0 ||
		req.Count > model.LiandongInventoryBatchLimit {
		common.ApiErrorMsg(c, "Inventory count must be between 1 and 1000")
		return
	}
	codes, err := model.AddLiandongInventoryCodes(productID, req.Count, req.Name, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "liandong.product.inventory.add", map[string]interface{}{
		"product_id": productID,
		"count":      len(codes),
	})
	common.ApiSuccess(c, gin.H{"created": len(codes)})
}

func RootDisableLiandongInventory(c *gin.Context) {
	productID, err := strconv.Atoi(c.Param("id"))
	if err != nil || productID <= 0 {
		common.ApiErrorMsg(c, "Invalid product ID")
		return
	}
	var req liandongInventoryChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil ||
		req.Count <= 0 ||
		req.Count > model.LiandongInventoryBatchLimit {
		common.ApiErrorMsg(c, "Inventory count must be between 1 and 1000")
		return
	}
	if err := model.DisableLiandongAvailableInventoryCodes(productID, req.Count); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "liandong.product.inventory.disable", map[string]interface{}{
		"product_id": productID,
		"count":      req.Count,
	})
	common.ApiSuccess(c, nil)
}

func RootUploadLiandongThumbnail(c *gin.Context) {
	productID, err := strconv.Atoi(c.Param("id"))
	if err != nil || productID <= 0 {
		common.ApiErrorMsg(c, "Invalid product ID")
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		common.ApiErrorMsg(c, "Thumbnail file is required")
		return
	}
	if fileHeader.Size <= 0 || fileHeader.Size > liandongThumbnailMaxBytes {
		common.ApiErrorMsg(c, "Thumbnail must not exceed 512 KB")
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, liandongThumbnailMaxBytes+1))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if len(data) == 0 || len(data) > liandongThumbnailMaxBytes {
		common.ApiErrorMsg(c, "Thumbnail must not exceed 512 KB")
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		common.ApiErrorMsg(c, "Invalid thumbnail image")
		return
	}
	contentType := ""
	switch format {
	case "jpeg":
		contentType = "image/jpeg"
	case "png":
		contentType = "image/png"
	case "webp":
		contentType = "image/webp"
	default:
		common.ApiErrorMsg(c, "Thumbnail must be JPEG, PNG, or WebP")
		return
	}
	if detected := http.DetectContentType(data); detected != contentType {
		common.ApiErrorMsg(c, "Thumbnail MIME type does not match its content")
		return
	}
	if config.Width <= 0 || config.Height <= 0 ||
		config.Width > liandongThumbnailMaxDimension ||
		config.Height > liandongThumbnailMaxDimension {
		common.ApiErrorMsg(c, "Thumbnail dimensions must be between 1 and 2048 pixels")
		return
	}
	if err := model.SaveLiandongProductThumbnail(
		productID,
		contentType,
		data,
		config.Width,
		config.Height,
	); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "liandong.product.thumbnail.update", map[string]interface{}{
		"product_id": productID,
		"width":      config.Width,
		"height":     config.Height,
		"size":       len(data),
	})
	common.ApiSuccess(c, nil)
}

func RootDeleteLiandongThumbnail(c *gin.Context) {
	productID, err := strconv.Atoi(c.Param("id"))
	if err != nil || productID <= 0 {
		common.ApiErrorMsg(c, "Invalid product ID")
		return
	}
	if err := model.DeleteLiandongProductThumbnail(productID); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "liandong.product.thumbnail.delete", map[string]interface{}{
		"product_id": productID,
	})
	common.ApiSuccess(c, nil)
}

func RootListLiandongProviderGoods(c *gin.Context) {
	goods, err := service.ListLiandongProviderGoods(
		c.Request.Context(),
		c.Query("goods_type"),
		c.Query("name"),
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, goods)
}

func RootListLiandongOrders(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	orders, total, err := model.ListLiandongOrders(pageInfo, c.Query("keyword"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	views := make([]liandongRootOrderView, 0, len(orders))
	for _, order := range orders {
		views = append(views, liandongRootOrderView{
			LocalTradeNo:        order.LocalTradeNo,
			ProviderTradeNo:     order.ProviderTradeNo,
			UserID:              order.UserID,
			ProductID:           order.ProductID,
			ProductName:         order.ProductNameSnapshot,
			BusinessType:        order.BusinessType,
			TargetID:            order.TargetID,
			InventoryCodeID:     order.InventoryCodeID,
			ExpectedAmountMinor: order.ExpectedAmountMinor,
			Currency:            order.CurrencySnapshot,
			PaymentStatus:       order.PaymentStatus,
			FulfillmentStatus:   order.FulfillmentStatus,
			LastCheckAt:         order.LastCheckAt,
			NextCheckAt:         order.NextCheckAt,
			CheckCount:          order.CheckCount,
			ConsecutiveErrors:   order.ConsecutiveErrorCount,
			LastError:           service.SanitizeLiandongOrderDiagnostic(&order, settingsSnapshot),
			ExpiresAt:           order.ExpiresAt,
			ClosedReason:        order.ClosedReason,
			LatePayment:         order.LatePayment,
			PaidAt:              order.PaidAt,
			FulfilledAt:         order.FulfilledAt,
			CreatedAt:           order.CreatedAt,
			UpdatedAt:           order.UpdatedAt,
		})
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(views)
	common.ApiSuccess(c, pageInfo)
}

func RootRequeueLiandongOrder(c *gin.Context) {
	if err := service.RequeueLiandongOrderContext(
		c.Request.Context(),
		c.Param("local_trade_no"),
	); err != nil {
		switch {
		case errors.Is(err, model.ErrLiandongOrderBusy):
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Payment order is being processed"})
		case errors.Is(err, model.ErrLiandongOrderNotFound):
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Payment order cannot be requeued"})
		default:
			common.ApiError(c, err)
		}
		return
	}
	recordManageAudit(c, "liandong.order.requeue", map[string]interface{}{
		"local_trade_no": c.Param("local_trade_no"),
	})
	service.WakeSystemTaskRunner()
	common.ApiSuccess(c, nil)
}

func RootCloseLiandongOrder(c *gin.Context) {
	view, err := service.CloseLiandongPaymentForRoot(
		c.Request.Context(),
		c.Param("local_trade_no"),
	)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrLiandongOrderBusy):
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Payment order cannot be closed"})
		case errors.Is(err, model.ErrLiandongOrderReviewRequired):
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Payment was confirmed or the order requires manual review"})
		case errors.Is(err, model.ErrLiandongOrderNotFound):
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Payment order not found"})
		default:
			common.ApiError(c, err)
		}
		return
	}
	recordManageAudit(c, "liandong.order.close", map[string]interface{}{
		"local_trade_no": c.Param("local_trade_no"),
	})
	common.ApiSuccess(c, view)
}

func RootManualFulfillLiandongOrder(c *gin.Context) {
	view, err := service.ManualFulfillLiandongLatePayment(c.Param("local_trade_no"))
	if err != nil {
		switch {
		case errors.Is(err, model.ErrLiandongInventoryUnavailable):
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Product inventory is unavailable"})
		case errors.Is(err, model.ErrLiandongOrderBusy):
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Payment order is being processed"})
		case errors.Is(err, model.ErrLiandongOrderReviewRequired):
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "Payment order is not an eligible confirmed late payment"})
		case errors.Is(err, model.ErrLiandongOrderNotFound):
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Payment order not found"})
		default:
			common.ApiError(c, err)
		}
		return
	}
	recordManageAudit(c, "liandong.order.manual_fulfill", map[string]interface{}{
		"local_trade_no": c.Param("local_trade_no"),
	})
	common.ApiSuccess(c, view)
}

func RootRetryLiandongFulfillment(c *gin.Context) {
	view, err := service.RetryLiandongFulfillment(c.Param("local_trade_no"))
	if err != nil {
		if errors.Is(err, model.ErrLiandongOrderNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Payment order not found"})
			return
		}
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "liandong.order.retry_fulfillment", map[string]interface{}{
		"local_trade_no": c.Param("local_trade_no"),
	})
	common.ApiSuccess(c, view)
}

func makeLiandongRootProductView(
	product model.LiandongProduct,
	summary model.LiandongInventorySummary,
) liandongRootProductView {
	view := liandongRootProductView{
		ID:                  product.ID,
		BusinessType:        product.BusinessType,
		GoodsType:           product.GoodsType,
		Name:                product.Name,
		GoodsKey:            product.GoodsKey,
		QuotaAmount:         product.QuotaAmount,
		PlanID:              product.PlanID,
		ExpectedAmountMinor: product.ExpectedAmountMinor,
		Currency:            product.Currency,
		InventoryMode:       product.InventoryMode,
		InventoryCapacity:   product.InventoryCapacity,
		InventoryAvailable:  summary.Available,
		InventoryReserved:   summary.Reserved,
		InventoryConsumed:   summary.Consumed,
		InventoryDisabled:   summary.Disabled,
		InventoryLevel:      liandongInventoryLevel(&product, summary),
		ThumbnailURL:        liandongThumbnailURL(&product),
		ThumbnailVersion:    product.ThumbnailVersion,
		Enabled:             product.Enabled,
		SortOrder:           product.SortOrder,
		CreatedBy:           product.CreatedBy,
		UpdatedBy:           product.UpdatedBy,
		CreatedAt:           product.CreatedAt,
		UpdatedAt:           product.UpdatedAt,
	}
	if product.BusinessType == model.LiandongBusinessTypeSubscription {
		view.Subscription = getLiandongSubscriptionSpec(product.PlanID)
	}
	return view
}

func getLiandongSubscriptionSpec(planID int) *liandongSubscriptionSpec {
	plan, err := model.GetSubscriptionPlanById(planID)
	if err != nil {
		return nil
	}
	return &liandongSubscriptionSpec{
		Title:                   plan.Title,
		DurationUnit:            plan.DurationUnit,
		DurationValue:           plan.DurationValue,
		CustomSeconds:           plan.CustomSeconds,
		TotalAmount:             plan.TotalAmount,
		QuotaResetPeriod:        plan.QuotaResetPeriod,
		QuotaResetCustomSeconds: plan.QuotaResetCustomSeconds,
		UpgradeGroup:            plan.UpgradeGroup,
	}
}

func liandongInventoryLevel(
	product *model.LiandongProduct,
	summary model.LiandongInventorySummary,
) string {
	if product == nil || product.InventoryMode != model.LiandongInventoryModeRedemptionCode {
		return "unlimited"
	}
	if product.InventoryCapacity <= 0 || summary.Available <= 0 {
		return "out_of_stock"
	}
	ratio := float64(summary.Available) / float64(product.InventoryCapacity)
	switch {
	case ratio > 0.6:
		return "sufficient"
	case ratio > 0.2:
		return "normal"
	default:
		return "low"
	}
}

func liandongThumbnailURL(product *model.LiandongProduct) string {
	if product == nil || product.ID <= 0 || product.ThumbnailVersion <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"/api/payment/liandong/products/%d/thumbnail?v=%d",
		product.ID,
		product.ThumbnailVersion,
	)
}
