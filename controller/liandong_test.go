package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupLiandongControllerTestDB(t *testing.T) {
	t.Helper()
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.Option{},
		&model.TopUp{},
		&model.SubscriptionPlan{},
		&model.SubscriptionOrder{},
		&model.UserSubscription{},
		&model.LiandongProduct{},
		&model.LiandongOrder{},
		&model.LiandongProductInventoryCode{},
		&model.LiandongProductThumbnail{},
		&model.LiandongUserOperationLease{},
	))
}

func TestGetLiandongOrderScopesByAuthenticatedUser(t *testing.T) {
	setupLiandongControllerTestDB(t)
	userA := &model.User{
		Username: "liandong-user-a",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "LDA001",
	}
	userB := &model.User{
		Username: "liandong-user-b",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "LDB001",
	}
	require.NoError(t, model.DB.Create(userA).Error)
	require.NoError(t, model.DB.Create(userB).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Quota Product",
		GoodsKey:            "controller-goods-key",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	createResult, err := model.CreateLiandongOrder(
		userB.Id,
		product.ID,
		"123456789012",
		"test-merchant-id",
	)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	context.Params = gin.Params{{Key: "local_trade_no", Value: createResult.Order.LocalTradeNo}}
	context.Set("id", userA.Id)

	GetLiandongOrder(context)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), createResult.Order.LocalTradeNo)
}

func TestGetLiandongOrderOmitsSensitivePaymentConfiguration(t *testing.T) {
	setupLiandongControllerTestDB(t)
	user := &model.User{
		Username: "liandong-response-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "LDR001",
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Response Product",
		GoodsKey:            "secret-goods-key",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":                   "true",
		"LiandongIframeEnabled":             "true",
		"LiandongClientPollIntervalSeconds": "7",
		"LiandongJUUID":                     "secret-settings-juuid",
		"LiandongMerchantToken":             "secret-merchant-token",
	}))
	createResult, err := model.CreateLiandongOrder(
		user.Id,
		product.ID,
		"123456789012",
		"secret-order-juuid",
	)
	require.NoError(t, err)
	providerTradeNo := "TRADE-RESPONSE-001"
	require.NoError(t, model.MarkLiandongCreateResult(
		createResult.Order.LocalTradeNo,
		&providerTradeNo,
		model.LiandongPaymentStatusPending,
		"",
	))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	context.Params = gin.Params{{Key: "local_trade_no", Value: createResult.Order.LocalTradeNo}}
	context.Set("id", user.Id)

	GetLiandongOrder(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, createResult.Order.LocalTradeNo)
	assert.Contains(t, body, `"client_poll_interval_seconds":7`)
	assert.NotContains(t, body, "secret-goods-key")
	assert.NotContains(t, body, "123456789012")
	assert.NotContains(t, body, "secret-order-juuid")
	assert.NotContains(t, body, "secret-settings-juuid")
	assert.NotContains(t, body, "secret-merchant-token")
	assert.NotContains(t, body, `"contact"`)
	assert.NotContains(t, body, `"goods_key"`)
	assert.NotContains(t, body, `"juuid"`)
	assert.NotContains(t, body, "merchant_token")
}

func TestGetLiandongSettingsNeverReturnsMerchantToken(t *testing.T) {
	setupLiandongControllerTestDB(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongMerchantToken": "super-secret-token",
		"LiandongJUUID":         "merchant-id",
		"LiandongBaseURL":       "https://gateway.example.com/card",
		"LiandongProxyEnabled":  "true",
		"LiandongProxyURL":      "socks5h://127.0.0.1:1080",
		"LiandongProxyUsername": "secret-proxy-user",
		"LiandongProxyPassword": "secret-proxy-password",
	}))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	GetLiandongSettings(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "super-secret-token")
	assert.NotContains(t, recorder.Body.String(), "secret-proxy-user")
	assert.NotContains(t, recorder.Body.String(), "secret-proxy-password")
	assert.Contains(t, recorder.Body.String(), `"merchant_token_configured":true`)
	assert.Contains(t, recorder.Body.String(), `"base_url":"https://gateway.example.com/card"`)
	assert.Contains(t, recorder.Body.String(), `"proxy_enabled":true`)
	assert.Contains(t, recorder.Body.String(), `"proxy_url":"socks5h://127.0.0.1:1080"`)
	assert.Contains(t, recorder.Body.String(), `"proxy_username_configured":true`)
	assert.Contains(t, recorder.Body.String(), `"proxy_password_configured":true`)
}

func TestGetTopUpInfoReportsLiandongMasterSwitch(t *testing.T) {
	setupLiandongControllerTestDB(t)
	tests := []struct {
		name     string
		option   string
		expected bool
	}{
		{name: "enabled", option: "true", expected: true},
		{name: "disabled", option: "false", expected: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, model.UpdateOptionsBulk(map[string]string{
				"LiandongEnabled": test.option,
			}))

			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			GetTopUpInfo(context)

			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool `json:"success"`
				Data    struct {
					EnableLiandongTopup bool `json:"enable_liandong_topup"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.True(t, response.Success)
			assert.Equal(t, test.expected, response.Data.EnableLiandongTopup)
		})
	}
}

func TestRootListLiandongOrdersSanitizesHistoricalDiagnostics(t *testing.T) {
	setupLiandongControllerTestDB(t)
	user := &model.User{
		Username: "liandong-diagnostic-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "LDD001",
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Diagnostic Product",
		GoodsKey:            "snapshot-goods-key",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongJUUID":         "current-juuid",
		"LiandongUsername":      "current-username",
		"LiandongPassword":      "current-password",
		"LiandongMerchantToken": "current-token",
	}))
	createResult, err := model.CreateLiandongOrder(
		user.Id,
		product.ID,
		"123456789012",
		"snapshot-juuid",
	)
	require.NoError(t, err)
	historicalDiagnostic := strings.Join([]string{
		"token=historical-token",
		`password:"historical-password"`,
		"username=historical-username",
		"JUUID=snapshot-juuid",
		"contact=123456789012",
		"goods_key=snapshot-goods-key",
		"opaque=current-token",
		"configured=current-password",
		"merchant=current-username",
		"current_j=current-juuid",
	}, " ")
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("id = ?", createResult.Order.ID).
		Update("last_error", historicalDiagnostic).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/?page=1&page_size=10", nil)

	RootListLiandongOrders(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, "[redacted]")
	for _, secret := range []string{
		"historical-token",
		"historical-password",
		"historical-username",
		"snapshot-juuid",
		"123456789012",
		"snapshot-goods-key",
		"current-token",
		"current-password",
		"current-username",
		"current-juuid",
	} {
		assert.NotContains(t, body, secret)
	}
}

func TestUpdateLiandongSettingsAllowsEmergencyDisableWithIncompleteCredentials(t *testing.T) {
	setupLiandongControllerTestDB(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "true",
		"LiandongCreateEnabled":    "true",
		"LiandongReconcileEnabled": "true",
		"LiandongJUUID":            "",
		"LiandongMerchantToken":    "",
	}))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/option/liandong",
		strings.NewReader(`{"enabled":false}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")

	UpdateLiandongSettings(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	require.NoError(t, err)
	assert.False(t, settingsSnapshot.Enabled)
}

func TestUpdateLiandongSettingsDisablesProxyWithoutConnectivityCheck(t *testing.T) {
	setupLiandongControllerTestDB(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongProxyEnabled":  "true",
		"LiandongProxyURL":      "socks5h://127.0.0.1:10808",
		"LiandongProxyUsername": "configured-user",
		"LiandongProxyPassword": "configured-password",
	}))

	previousValidator := liandongProxyValidator
	liandongProxyValidator = func(context.Context, setting.LiandongPaymentSettings) error {
		return assert.AnError
	}
	t.Cleanup(func() {
		liandongProxyValidator = previousValidator
	})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/option/liandong",
		strings.NewReader(`{"proxy_enabled":false}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")

	UpdateLiandongSettings(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	require.NoError(t, err)
	assert.False(t, settingsSnapshot.ProxyEnabled)
}

func TestUpdateLiandongSettingsEnablesChildControlsByDefault(t *testing.T) {
	setupLiandongControllerTestDB(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongJUUID":         "merchant-id",
		"LiandongMerchantToken": "secret-token",
	}))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/option/liandong",
		strings.NewReader(`{"enabled":true}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")

	UpdateLiandongSettings(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	require.NoError(t, err)
	assert.True(t, settingsSnapshot.Enabled)
	assert.True(t, settingsSnapshot.CreateEnabled)
	assert.True(t, settingsSnapshot.ReconcileEnabled)
	assert.True(t, settingsSnapshot.FulfillEnabled)
	assert.True(t, settingsSnapshot.IframeEnabled)
}

func TestUpdateLiandongSettingsStoresIndependentPollingIntervals(t *testing.T) {
	setupLiandongControllerTestDB(t)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/option/liandong",
		strings.NewReader(`{"poll_interval_seconds":1,"client_poll_interval_seconds":7}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")

	UpdateLiandongSettings(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	require.NoError(t, err)
	assert.Equal(t, 1, settingsSnapshot.PollIntervalSeconds)
	assert.Equal(t, 7, settingsSnapshot.ClientPollIntervalSeconds)
	assert.NotContains(t, recorder.Body.String(), "merchant_token")
}

func TestUpdateLiandongSettingsStoresBaseURLAndSOCKS5Proxy(t *testing.T) {
	setupLiandongControllerTestDB(t)
	previousValidator := liandongProxyValidator
	liandongProxyValidator = func(context.Context, setting.LiandongPaymentSettings) error {
		return nil
	}
	t.Cleanup(func() {
		liandongProxyValidator = previousValidator
	})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/option/liandong",
		strings.NewReader(`{
			"base_url":"https://gateway.example.com/card/",
			"proxy_enabled":true,
			"proxy_url":"socks5h://127.0.0.1:1080/",
			"proxy_username":"proxy-user",
			"proxy_password":"proxy-password"
		}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")

	UpdateLiandongSettings(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	require.NoError(t, err)
	assert.Equal(t, "https://gateway.example.com/card", settingsSnapshot.BaseURL)
	assert.True(t, settingsSnapshot.ProxyEnabled)
	assert.Equal(t, "socks5h://127.0.0.1:1080", settingsSnapshot.ProxyURL)
	assert.Equal(t, "proxy-user", settingsSnapshot.ProxyUsername)
	assert.Equal(t, "proxy-password", settingsSnapshot.ProxyPassword)
	assert.NotContains(t, recorder.Body.String(), "proxy-user")
	assert.NotContains(t, recorder.Body.String(), "proxy-password")
}

func TestUpdateLiandongSettingsRequiresNewManualTokenWhenLeavingCredentialsMode(t *testing.T) {
	setupLiandongControllerTestDB(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "true",
		"LiandongCreateEnabled":    "false",
		"LiandongReconcileEnabled": "true",
		"LiandongAuthMode":         setting.LiandongAuthModeCredentials,
		"LiandongUsername":         "configured-user",
		"LiandongPassword":         "configured-password",
		"LiandongMerchantToken":    "derived-token",
	}))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/option/liandong",
		strings.NewReader(`{"auth_mode":"manual_token"}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")

	UpdateLiandongSettings(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Merchant token is required")
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	require.NoError(t, err)
	assert.Equal(t, setting.LiandongAuthModeCredentials, settingsSnapshot.AuthMode)
	assert.Equal(t, "derived-token", settingsSnapshot.MerchantToken)
	assert.Equal(t, "configured-user", settingsSnapshot.Username)
	assert.Equal(t, "configured-password", settingsSnapshot.Password)
}

func TestUpdateLiandongSettingsAcceptsExplicitManualTokenWhenLeavingCredentialsMode(t *testing.T) {
	setupLiandongControllerTestDB(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "true",
		"LiandongCreateEnabled":    "false",
		"LiandongReconcileEnabled": "true",
		"LiandongAuthMode":         setting.LiandongAuthModeCredentials,
		"LiandongUsername":         "configured-user",
		"LiandongPassword":         "configured-password",
		"LiandongMerchantToken":    "derived-token",
	}))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/option/liandong",
		strings.NewReader(`{"auth_mode":"manual_token","merchant_token":"new-manual-token"}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")

	UpdateLiandongSettings(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	require.NoError(t, err)
	assert.Equal(t, setting.LiandongAuthModeManualToken, settingsSnapshot.AuthMode)
	assert.Equal(t, "new-manual-token", settingsSnapshot.MerchantToken)
	assert.Empty(t, settingsSnapshot.Username)
	assert.Empty(t, settingsSnapshot.Password)
	assert.NotContains(t, recorder.Body.String(), "new-manual-token")
}

func TestLiandongPollHandlerRunsOnlyWhileLiandongWorkExists(t *testing.T) {
	setupLiandongControllerTestDB(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "true",
		"LiandongReconcileEnabled": "true",
		"LiandongFulfillEnabled":   "true",
	}))

	assert.False(t, (liandongPollHandler{}).Enabled())

	user := &model.User{
		Username: "liandong-handler-work-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "LDHWRK",
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Handler work product",
		GoodsKey:            "handler-work-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)

	pendingResult, err := model.CreateLiandongOrderWithTimeout(
		user.Id,
		product.ID,
		"123456789012",
		"test-merchant-id",
		1,
	)
	require.NoError(t, err)
	providerTradeNo := "LDHANDLERPENDING001"
	require.NoError(t, model.MarkLiandongCreateResult(
		pendingResult.Order.LocalTradeNo,
		&providerTradeNo,
		model.LiandongPaymentStatusPending,
		"",
	))
	assert.True(t, (liandongPollHandler{}).Enabled())

	require.NoError(t, model.DB.Where("id = ?", pendingResult.Order.ID).Delete(&model.LiandongOrder{}).Error)
	expiredResult, err := model.CreateLiandongOrderWithTimeout(
		user.Id,
		product.ID,
		"123456789013",
		"test-merchant-id",
		1,
	)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("id = ?", expiredResult.Order.ID).
		Update("expires_at", common.GetTimestamp()-1).Error)
	assert.True(t, (liandongPollHandler{}).Enabled())

	require.NoError(t, model.DB.Where("id = ?", expiredResult.Order.ID).Delete(&model.LiandongOrder{}).Error)
	paidResult, err := model.CreateLiandongOrderWithTimeout(
		user.Id,
		product.ID,
		"123456789014",
		"test-merchant-id",
		1,
	)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("id = ?", paidResult.Order.ID).
		Updates(map[string]any{
			"payment_status":     model.LiandongPaymentStatusPaid,
			"fulfillment_status": model.LiandongFulfillmentStatusWaiting,
		}).Error)
	assert.True(t, (liandongPollHandler{}).Enabled())
}

func TestLiandongPollHandlerKeepsClosedProviderOrderEligibleForLatePaymentDetection(t *testing.T) {
	setupLiandongControllerTestDB(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "true",
		"LiandongReconcileEnabled": "true",
		"LiandongFulfillEnabled":   "false",
	}))
	providerTradeNo := "LDHANDLERLATE001"
	order := &model.LiandongOrder{
		LocalTradeNo:      "LDHANDLERLATELOCAL001",
		BusinessType:      model.LiandongBusinessTypeQuota,
		ProviderTradeNo:   &providerTradeNo,
		PaymentStatus:     model.LiandongPaymentStatusClosed,
		FulfillmentStatus: model.LiandongFulfillmentStatusFailed,
		ClosedReason:      "payment timeout",
	}
	require.NoError(t, model.DB.Create(order).Error)

	assert.True(t, (liandongPollHandler{}).Enabled())

	require.NoError(t, model.DB.Model(order).Updates(map[string]any{
		"late_payment":       true,
		"fulfillment_status": model.LiandongFulfillmentStatusReviewRequired,
		"paid_at":            common.GetTimestamp(),
	}).Error)
	assert.False(t, (liandongPollHandler{}).Enabled())
}

func TestLiandongPollHandlerRunsFulfillmentOnlyWhenPaidWorkExists(t *testing.T) {
	setupLiandongControllerTestDB(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "true",
		"LiandongReconcileEnabled": "false",
		"LiandongFulfillEnabled":   "true",
	}))

	assert.False(t, (liandongPollHandler{}).Enabled())

	user := &model.User{
		Username: "liandong-fulfillment-handler-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "LDHFUL",
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Fulfillment handler product",
		GoodsKey:            "fulfillment-handler-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	createResult, err := model.CreateLiandongOrderWithTimeout(
		user.Id,
		product.ID,
		"123456789012",
		"test-merchant-id",
		1,
	)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("id = ?", createResult.Order.ID).
		Updates(map[string]any{
			"payment_status":     model.LiandongPaymentStatusPaid,
			"fulfillment_status": model.LiandongFulfillmentStatusWaiting,
		}).Error)

	assert.True(t, (liandongPollHandler{}).Enabled())
}

func TestLiandongPollHandlerStopsWhenMasterSwitchDisabled(t *testing.T) {
	setupLiandongControllerTestDB(t)
	user := &model.User{
		Username: "liandong-disabled-handler-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "LDH000",
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Disabled handler product",
		GoodsKey:            "disabled-handler-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	createResult, err := model.CreateLiandongOrderWithTimeout(
		user.Id,
		product.ID,
		"123456789012",
		"test-merchant-id",
		1,
	)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("id = ?", createResult.Order.ID).
		Update("expires_at", common.GetTimestamp()-1).Error)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "false",
		"LiandongReconcileEnabled": "true",
		"LiandongFulfillEnabled":   "true",
	}))

	assert.False(t, (liandongPollHandler{}).Enabled())
}

func TestLiandongPollHandlerStopsForExpiredProviderOrderWhenReconciliationDisabled(t *testing.T) {
	setupLiandongControllerTestDB(t)
	user := &model.User{
		Username: "liandong-expired-handler-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "LDH001",
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Expired handler product",
		GoodsKey:            "expired-handler-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	createResult, err := model.CreateLiandongOrderWithTimeout(
		user.Id,
		product.ID,
		"123456789012",
		"test-merchant-id",
		1,
	)
	require.NoError(t, err)
	providerTradeNo := "LDHANDLEREXPIRED001"
	require.NoError(t, model.MarkLiandongCreateResult(
		createResult.Order.LocalTradeNo,
		&providerTradeNo,
		model.LiandongPaymentStatusPending,
		"",
	))
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("id = ?", createResult.Order.ID).
		Update("expires_at", common.GetTimestamp()-1).Error)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "true",
		"LiandongReconcileEnabled": "false",
		"LiandongFulfillEnabled":   "false",
	}))

	assert.False(t, (liandongPollHandler{}).Enabled())
}

func TestLiandongRootRoutesRejectAdminSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("liandong-auth-test"))))
	engine.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "admin")
		session.Set("role", common.RoleAdminUser)
		session.Set("id", 42)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	handlerCalled := false
	engine.GET("/root", middleware.RootAuth(), func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusNoContent)
	})

	loginRecorder := httptest.NewRecorder()
	engine.ServeHTTP(loginRecorder, httptest.NewRequest(http.MethodGet, "/login", nil))
	require.Equal(t, http.StatusNoContent, loginRecorder.Code)
	cookieHeader := loginRecorder.Header().Get("Set-Cookie")
	require.NotEmpty(t, cookieHeader)

	request := httptest.NewRequest(http.MethodGet, "/root", nil)
	request.Header.Set("Cookie", strings.Split(cookieHeader, ";")[0])
	request.Header.Set("New-Api-User", "42")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	assert.False(t, handlerCalled)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
}

func TestLiandongRootRouteAllowsRootSession(t *testing.T) {
	setupLiandongControllerTestDB(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("liandong-root-auth-test"))))
	engine.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "root")
		session.Set("role", common.RoleRootUser)
		session.Set("id", 1)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	engine.GET("/api/option/liandong", middleware.RootAuth(), GetLiandongSettings)

	loginRecorder := httptest.NewRecorder()
	engine.ServeHTTP(loginRecorder, httptest.NewRequest(http.MethodGet, "/login", nil))
	require.Equal(t, http.StatusNoContent, loginRecorder.Code)
	cookieHeader := loginRecorder.Header().Get("Set-Cookie")
	require.NotEmpty(t, cookieHeader)

	request := httptest.NewRequest(http.MethodGet, "/api/option/liandong", nil)
	request.Header.Set("Cookie", strings.Split(cookieHeader, ";")[0])
	request.Header.Set("New-Api-User", "1")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	assert.Contains(t, recorder.Body.String(), `"merchant_token_configured":false`)
}
