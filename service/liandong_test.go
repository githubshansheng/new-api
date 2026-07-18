package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLiandongCreateTradeNo(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		want      string
		wantError bool
	}{
		{
			name: "absolute payment URL",
			body: `{"payUrl":"https://pay.ldxp.cn/shopApi/Pay/payment?trade_no=TRADE123"}`,
			want: "TRADE123",
		},
		{
			name: "relative payment URL",
			body: `{"data":{"pay_url":"/shopApi/Pay/payment?trade_no=TRADE456"}}`,
			want: "TRADE456",
		},
		{
			name: "nested trade number",
			body: `{"code":1,"msg":"success","data":{"trade_no":"TRADE789"}}`,
			want: "TRADE789",
		},
		{
			name:      "provider business rejection",
			body:      `{"code":0,"msg":"商品不存在","data":null}`,
			wantError: true,
		},
		{
			name:      "foreign host",
			body:      `{"payUrl":"https://example.com/shopApi/Pay/payment?trade_no=TRADE123"}`,
			wantError: true,
		},
		{
			name:      "scheme relative foreign host",
			body:      `{"payUrl":"//example.com/shopApi/Pay/payment?trade_no=TRADE123"}`,
			wantError: true,
		},
		{
			name:      "unexpected path",
			body:      `{"payUrl":"https://pay.ldxp.cn/other?trade_no=TRADE123"}`,
			wantError: true,
		},
		{
			name:      "ambiguous trade number",
			body:      `{"payUrl":"/shopApi/Pay/payment?trade_no=TRADE123&trade_no=TRADE456"}`,
			wantError: true,
		},
		{
			name:      "invalid trade number",
			body:      `{"payUrl":"/shopApi/Pay/payment?trade_no=bad%20trade"}`,
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseLiandongCreateTradeNo([]byte(test.body))
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestParseLiandongCreateTradeNoAcceptsConfiguredBaseURL(t *testing.T) {
	tradeNo, err := parseLiandongCreateTradeNoForBaseURL(
		[]byte(
			`{"payUrl":"https://gateway.example.com/card/shopApi/Pay/payment?trade_no=TRADE123"}`,
		),
		"https://gateway.example.com/card",
	)

	require.NoError(t, err)
	assert.Equal(t, "TRADE123", tradeNo)
	assert.Equal(
		t,
		"https://gateway.example.com/card/shopApi/Pay/payment?trade_no=TRADE123",
		liandongPaymentURL("https://gateway.example.com/card", "TRADE123"),
	)
}

func TestParseLiandongLoginToken(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		want      string
		wantError string
	}{
		{
			name: "accepts root token with ambiguous zero code",
			body: `{"code":0,"merchant_token":"fresh-token"}`,
			want: "fresh-token",
		},
		{
			name: "accepts nested token with string zero code",
			body: `{"code":"0","data":{"merchant-token":"nested-token"}}`,
			want: "nested-token",
		},
		{
			name:      "rejects unauthorized body even when token is present",
			body:      `{"code":"401","token":"must-not-be-used"}`,
			wantError: "login was rejected",
		},
		{
			name:      "rejects zero code without token",
			body:      `{"code":0,"message":"invalid credentials"}`,
			wantError: "login was rejected",
		},
		{
			name:      "rejects missing token",
			body:      `{"code":1,"message":"success"}`,
			wantError: "no valid merchant token",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token, err := parseLiandongLoginToken([]byte(test.body))
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				assert.Empty(t, token)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, token)
		})
	}
}

func TestLiandongProviderBusinessRejectionIsDefinitive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"商品不存在","data":null}`))
	}))
	defer server.Close()

	client := &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	}
	_, err := client.createOrder(
		context.Background(),
		"missing-goods-key",
		"123456789012",
		"merchant-id",
	)

	require.Error(t, err)
	var createErr *liandongCreateError
	require.ErrorAs(t, err, &createErr)
	assert.True(t, createErr.definitive)
	assert.Contains(t, createErr.Error(), "商品不存在")
}

func TestParseLiandongOrderVerification(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		paid           bool
		reviewRequired bool
		wantError      bool
	}{
		{
			name: "paid",
			body: `{"list":[{"trade_no":"TRADE123","status":1}]}`,
			paid: true,
		},
		{
			name: "other numeric status remains pending",
			body: `{"list":[{"trade_no":"TRADE123","status":2}]}`,
		},
		{
			name: "numeric string remains pending",
			body: `{"data":{"records":[{"tradeNo":"TRADE123","status":"9"}]}}`,
		},
		{
			name: "no record remains pending",
			body: `{"list":[]}`,
		},
		{
			name:      "provider rejects merchant token",
			body:      `{"code":0,"msg":"token invalid","data":null}`,
			wantError: true,
		},
		{
			name:      "provider rejects merchant token with string code",
			body:      `{"code":"0","message":"token invalid","data":[]}`,
			wantError: true,
		},
		{
			name:           "multiple records require review",
			body:           `{"list":[{"trade_no":"TRADE123","status":1},{"trade_no":"TRADE123","status":1}]}`,
			reviewRequired: true,
		},
		{
			name:           "wrong trade number requires review",
			body:           `{"list":[{"trade_no":"OTHER123","status":1}]}`,
			reviewRequired: true,
		},
		{
			name:           "invalid status requires review",
			body:           `{"list":[{"trade_no":"TRADE123","status":"paid"}]}`,
			reviewRequired: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verification, err := parseLiandongOrderVerification([]byte(test.body), "TRADE123")
			if test.wantError {
				require.Error(t, err)
				assert.Nil(t, verification)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.paid, verification.Paid)
			assert.Equal(t, test.reviewRequired, verification.ReviewRequired)
		})
	}
}

func TestParseLiandongOrderRecordsAcceptsEmptyNestedList(t *testing.T) {
	records, err := parseLiandongOrderRecords(
		[]byte(`{"data":{"records":[],"total":0}}`),
	)

	require.NoError(t, err)
	assert.Empty(t, records)
}

func TestParseLiandongGoodsAcceptsUTF8BOM(t *testing.T) {
	body := append(
		[]byte{0xef, 0xbb, 0xbf},
		[]byte(`{"data":{"records":[{"goods_key":"goods-1","name":"Product 1","goods_type":"card"}]}}`)...,
	)

	goods, err := parseLiandongGoods(body)

	require.NoError(t, err)
	require.Len(t, goods, 1)
	assert.Equal(t, "goods-1", goods[0].GoodsKey)
	assert.Equal(t, "Product 1", goods[0].Name)
	assert.Equal(t, "card", goods[0].GoodsType)
}

func TestParseLiandongGoodsClassifiesHTMLWithoutLeakingBody(t *testing.T) {
	_, err := parseLiandongGoods(
		[]byte(`<html><body>merchant-token=secret-token</body></html>`),
	)

	require.ErrorContains(t, err, "received HTML instead of JSON")
	assert.NotContains(t, err.Error(), "secret-token")
	assert.NotContains(t, err.Error(), "<html>")
}

func TestLiandongProviderResponseDiagnosticRedactsConfiguredSecrets(t *testing.T) {
	diagnostic := liandongProviderResponseDiagnostic(
		[]byte(
			`{"message":"merchant-token=secret-token username=root-user password=root-password juuid=merchant-id goods_key=goods-1 contact=123456789012"}`,
		),
		"secret-token",
		"root-user",
		"root-password",
		"merchant-id",
		"goods-1",
		"123456789012",
	)

	assert.Contains(t, diagnostic, `"message":`)
	assert.NotContains(t, diagnostic, "secret-token")
	assert.NotContains(t, diagnostic, "root-user")
	assert.NotContains(t, diagnostic, "root-password")
	assert.NotContains(t, diagnostic, "merchant-id")
	assert.NotContains(t, diagnostic, "goods-1")
	assert.NotContains(t, diagnostic, "123456789012")
}

func TestNewLiandongClientUsesDedicatedSOCKS5Proxy(t *testing.T) {
	client := newLiandongClientWithSettings(setting.LiandongPaymentSettings{
		BaseURL:       "https://gateway.example.com/card",
		ProxyEnabled:  true,
		ProxyURL:      "socks5h://127.0.0.1:1080",
		ProxyUsername: "proxy-user",
		ProxyPassword: "proxy-password",
	})

	require.NoError(t, client.configErr)
	assert.Equal(t, "https://gateway.example.com/card", client.baseURL)
	transport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.Proxy)
	request := httptest.NewRequest(http.MethodGet, "https://gateway.example.com", nil)
	proxyURL, err := transport.Proxy(request)
	require.NoError(t, err)
	require.NotNil(t, proxyURL)
	assert.Equal(t, "socks5h", proxyURL.Scheme)
	assert.Equal(t, "127.0.0.1:1080", proxyURL.Host)
	assert.Equal(t, "proxy-user", proxyURL.User.Username())
	password, hasPassword := proxyURL.User.Password()
	assert.True(t, hasPassword)
	assert.Equal(t, "proxy-password", password)
}

func TestNewLiandongClientUsesDedicatedSOCKS5ProxyWithoutAuthentication(t *testing.T) {
	client := newLiandongClientWithSettings(setting.LiandongPaymentSettings{
		BaseURL:      "https://gateway.example.com/card",
		ProxyEnabled: true,
		ProxyURL:     "socks5h://127.0.0.1:10808",
	})

	require.NoError(t, client.configErr)
	transport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	request := httptest.NewRequest(http.MethodGet, "https://gateway.example.com", nil)
	proxyURL, err := transport.Proxy(request)
	require.NoError(t, err)
	require.NotNil(t, proxyURL)
	assert.Equal(t, "socks5h", proxyURL.Scheme)
	assert.Equal(t, "127.0.0.1:10808", proxyURL.Host)
	assert.Nil(t, proxyURL.User)
}

func TestLiandongMerchantTokenOnlySentToOrderList(t *testing.T) {
	type capturedRequest struct {
		path  string
		token string
	}
	requests := make(chan capturedRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- capturedRequest{
			path:  r.URL.Path,
			token: r.Header.Get("merchant-token"),
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case liandongCreatePath:
			_, _ = w.Write([]byte(`{"payUrl":"/shopApi/Pay/payment?trade_no=TRADE123"}`))
		case liandongOrderListPath:
			_, _ = w.Write([]byte(`{"list":[{"trade_no":"TRADE123","status":2}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	}
	tradeNo, err := client.createOrder(context.Background(), "goods-key", "123456789012", "merchant-id")
	require.NoError(t, err)
	require.Equal(t, "TRADE123", tradeNo)

	order := &model.LiandongOrder{ProviderTradeNo: &tradeNo}
	verification, err := client.queryOrderWithSettings(
		context.Background(),
		setting.LiandongPaymentSettings{
			AuthMode:      setting.LiandongAuthModeManualToken,
			MerchantToken: "secret-token",
		},
		order,
	)
	require.NoError(t, err)
	assert.False(t, verification.Paid)

	createRequest := <-requests
	queryRequest := <-requests
	assert.Equal(t, liandongCreatePath, createRequest.path)
	assert.Empty(t, createRequest.token)
	assert.Equal(t, liandongOrderListPath, queryRequest.path)
	assert.Equal(t, "secret-token", queryRequest.token)
}

func TestCreateLiandongPaymentRejectsUnavailableInventoryBeforeProviderRequest(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user := &model.User{
		Username: "liandong-out-of-stock-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		GoodsType:           "card",
		Name:                "Out of stock product",
		GoodsKey:            "out-of-stock-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		InventoryMode:       model.LiandongInventoryModeRedemptionCode,
		InventoryCapacity:   1,
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":       "true",
		"LiandongCreateEnabled": "true",
		"LiandongJUUID":         "test-merchant-id",
	}))

	var providerRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerRequests.Add(1)
		http.Error(w, "provider should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	view, err := createLiandongPayment(
		context.Background(),
		user.Id,
		product.ID,
		&liandongClient{httpClient: server.Client(), baseURL: server.URL},
	)

	require.ErrorIs(t, err, model.ErrLiandongInventoryUnavailable)
	assert.Nil(t, view)
	assert.Zero(t, providerRequests.Load())
	var orderCount int64
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("user_id = ?", user.Id).
		Count(&orderCount).Error)
	assert.Zero(t, orderCount)
}

func TestRunLiandongReconcileOnceReleasesExpiredReservedInventoryWithoutProviderTradeNo(t *testing.T) {
	resetLiandongServiceFixtures(t)
	configureLiandongServiceSettings(t)
	user := &model.User{
		Username: "liandong-timeout-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		GoodsType:           "card",
		Name:                "Timeout inventory product",
		GoodsKey:            "timeout-inventory-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		InventoryMode:       model.LiandongInventoryModeRedemptionCode,
		InventoryCapacity:   1,
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	_, err := model.AddLiandongInventoryCodes(product.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)
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

	var providerRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"list":[]}`))
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.NoError(t, err)
	assert.Equal(t, int32(1), providerRequests.Load())
	assert.Equal(t, 1, result["processed"])
	order, err := model.GetLiandongOrder(createResult.Order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusClosed, order.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusFailed, order.FulfillmentStatus)
	assert.Equal(t, "payment timeout", order.ClosedReason)

	summaries, err := model.GetLiandongInventorySummaries([]int{product.ID})
	require.NoError(t, err)
	assert.EqualValues(t, 1, summaries[product.ID].Available)
	assert.Zero(t, summaries[product.ID].Reserved)
	assert.Zero(t, summaries[product.ID].Consumed)

	var topUp model.TopUp
	require.NoError(t, model.DB.
		Where("trade_no = ?", createResult.Order.LocalTradeNo).
		First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusFailed, topUp.Status)
}

func TestRunLiandongReconcileOnceUsesSingleFirstPageBatchRequest(t *testing.T) {
	resetLiandongServiceFixtures(t)
	_, order := createLiandongServiceQuotaOrder(t, "123456789012")
	configureLiandongServiceSettings(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongReconcileBatchSize": "37",
	}))

	type requestPayload struct {
		Current  int     `json:"current"`
		PageSize int     `json:"pageSize"`
		Status   int     `json:"status"`
		TradeNo  *string `json:"trade_no"`
	}
	payloads := make(chan requestPayload, 2)
	decodeErrors := make(chan error, 2)
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		var payload requestPayload
		if err := common.DecodeJson(r.Body, &payload); err != nil {
			decodeErrors <- err
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		payloads <- payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"list":[{"trade_no":%q,"status":2}]}`,
			*order.ProviderTradeNo,
		)))
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.NoError(t, err)
	assert.Equal(t, int32(1), requestCount.Load())
	assert.Zero(t, result["paid"])
	require.Empty(t, decodeErrors)
	require.Len(t, payloads, 1)
	payload := <-payloads
	assert.Equal(t, 1, payload.Current)
	assert.Equal(t, 37, payload.PageSize)
	assert.Equal(t, 999, payload.Status)
	assert.Nil(t, payload.TradeNo)
}

func TestRunLiandongReconcileOnceDiscoversLatePaymentWithoutPendingOrders(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user, order := createLiandongServiceQuotaOrder(t, "123456789012")
	configureLiandongServiceSettings(t)
	require.NoError(t, model.CloseLiandongOrderForUser(
		user.Id,
		order.LocalTradeNo,
		"payment timeout",
	))

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"list":[{"trade_no":%q,"status":1}]}`,
			*order.ProviderTradeNo,
		)))
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.NoError(t, err)
	assert.Equal(t, int32(1), requestCount.Load())
	assert.Equal(t, 1, result["processed"])
	assert.Equal(t, 1, result["failed"])
	assert.Zero(t, result["paid"])
	assert.Zero(t, result["fulfilled"])

	reloaded, err := model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusClosed, reloaded.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusReviewRequired, reloaded.FulfillmentStatus)
	assert.True(t, reloaded.LatePayment)

	firstCheckCount := reloaded.CheckCount
	secondResult, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), requestCount.Load())
	assert.Zero(t, secondResult["processed"])
	assert.Zero(t, secondResult["paid"])
	assert.Zero(t, secondResult["fulfilled"])
	assert.Zero(t, secondResult["failed"])

	reloaded, err = model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, firstCheckCount, reloaded.CheckCount)
	assert.True(t, reloaded.LatePayment)
}

func TestRunLiandongReconcileOnceKeepsExpiredUncreatedInventoryWhenVerificationDisabled(t *testing.T) {
	tests := []struct {
		name          string
		masterEnabled bool
	}{
		{
			name:          "verification switch disabled",
			masterEnabled: true,
		},
		{
			name: "master switch disabled",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetLiandongServiceFixtures(t)
			user := &model.User{
				Username: fmt.Sprintf("liandong-uncreated-expiry-%s", common.GetUUID()),
				Password: "password",
				Status:   common.UserStatusEnabled,
				Role:     common.RoleCommonUser,
				Group:    "default",
				AffCode:  common.GetRandomString(16),
			}
			require.NoError(t, model.DB.Create(user).Error)
			product := &model.LiandongProduct{
				BusinessType:        model.LiandongBusinessTypeQuota,
				GoodsType:           "card",
				Name:                "Disabled timeout inventory product",
				GoodsKey:            fmt.Sprintf("disabled-timeout-%s", common.GetUUID()),
				QuotaAmount:         100,
				ExpectedAmountMinor: 100,
				Currency:            "CNY",
				InventoryMode:       model.LiandongInventoryModeRedemptionCode,
				InventoryCapacity:   1,
				Enabled:             true,
			}
			require.NoError(t, model.DB.Create(product).Error)
			_, err := model.AddLiandongInventoryCodes(product.ID, 1, "", common.RoleRootUser)
			require.NoError(t, err)
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
				"LiandongEnabled":          fmt.Sprintf("%t", test.masterEnabled),
				"LiandongReconcileEnabled": "false",
				"LiandongFulfillEnabled":   "false",
			}))

			result, err := runLiandongReconcileOnce(context.Background(), newLiandongClient())

			require.NoError(t, err)
			assert.Zero(t, result["processed"])
			reloaded, err := model.GetLiandongOrder(createResult.Order.LocalTradeNo)
			require.NoError(t, err)
			assert.Equal(t, model.LiandongPaymentStatusCreating, reloaded.PaymentStatus)
			summaries, err := model.GetLiandongInventorySummaries([]int{product.ID})
			require.NoError(t, err)
			assert.Zero(t, summaries[product.ID].Available)
			assert.EqualValues(t, 1, summaries[product.ID].Reserved)
		})
	}
}

func TestLiandongClientExactQueryIncludesProviderTradeNo(t *testing.T) {
	resetLiandongServiceFixtures(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongAuthMode":      "manual_token",
		"LiandongMerchantToken": "secret-token",
	}))
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	require.NoError(t, err)

	type requestPayload struct {
		Current  int     `json:"current"`
		PageSize int     `json:"pageSize"`
		Status   int     `json:"status"`
		TradeNo  *string `json:"trade_no"`
	}
	payloads := make(chan requestPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload requestPayload
		if err := common.DecodeJson(r.Body, &payload); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		payloads <- payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"list":[{"trade_no":"TRADE-EXACT-001","status":2}]}`))
	}))
	defer server.Close()
	tradeNo := "TRADE-EXACT-001"

	verification, err := (&liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	}).queryOrderWithSettings(
		context.Background(),
		settingsSnapshot,
		&model.LiandongOrder{ProviderTradeNo: &tradeNo},
	)

	require.NoError(t, err)
	require.NotNil(t, verification)
	assert.False(t, verification.Paid)
	require.Len(t, payloads, 1)
	payload := <-payloads
	assert.Equal(t, 1, payload.Current)
	assert.Equal(t, 1, payload.PageSize)
	assert.Equal(t, 999, payload.Status)
	require.NotNil(t, payload.TradeNo)
	assert.Equal(t, tradeNo, *payload.TradeNo)
}

func TestLiandongCredentialAuthRefreshesOnUnauthorizedAndRetriesOnce(t *testing.T) {
	tests := []struct {
		name             string
		unauthorizedHTTP bool
	}{
		{name: "HTTP 401", unauthorizedHTTP: true},
		{name: "response body code 401"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetLiandongServiceFixtures(t)
			require.NoError(t, model.UpdateOptionsBulk(map[string]string{
				"LiandongAuthMode":      "credentials",
				"LiandongUsername":      "provider-user",
				"LiandongPassword":      "provider-password",
				"LiandongMerchantToken": "stale-token",
			}))
			settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
			require.NoError(t, err)

			type loginPayload struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			loginPayloads := make(chan loginPayload, 1)
			var listRequests atomic.Int32
			var loginRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case liandongOrderListPath:
					listRequests.Add(1)
					if r.Header.Get("merchant-token") == "stale-token" {
						if test.unauthorizedHTTP {
							w.WriteHeader(http.StatusUnauthorized)
						}
						_, _ = w.Write([]byte(`{"code":"401","message":"expired"}`))
						return
					}
					_, _ = w.Write([]byte(`{"list":[{"trade_no":"TRADE-AUTH-001","status":2}]}`))
				case liandongLoginPath:
					loginRequests.Add(1)
					var payload loginPayload
					if err := common.DecodeJson(r.Body, &payload); err != nil {
						http.Error(w, "invalid login request", http.StatusBadRequest)
						return
					}
					loginPayloads <- payload
					_, _ = w.Write([]byte(`{"data":{"merchant_token":"fresh-token"}}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			tradeNo := "TRADE-AUTH-001"

			verification, err := (&liandongClient{
				httpClient: server.Client(),
				baseURL:    server.URL,
			}).queryOrderWithSettings(
				context.Background(),
				settingsSnapshot,
				&model.LiandongOrder{ProviderTradeNo: &tradeNo},
			)

			require.NoError(t, err)
			require.NotNil(t, verification)
			assert.False(t, verification.Paid)
			assert.Equal(t, int32(2), listRequests.Load())
			assert.Equal(t, int32(1), loginRequests.Load())
			require.Len(t, loginPayloads, 1)
			receivedLogin := <-loginPayloads
			assert.Equal(t, "provider-user", receivedLogin.Username)
			assert.Equal(t, "provider-password", receivedLogin.Password)
			refreshedSettings, err := model.GetLiandongPaymentSettingsFromDB()
			require.NoError(t, err)
			assert.Equal(t, "fresh-token", refreshedSettings.MerchantToken)
		})
	}
}

func TestLiandongManualTokenModeNeverCallsLogin(t *testing.T) {
	resetLiandongServiceFixtures(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongAuthMode":      "manual_token",
		"LiandongMerchantToken": "manual-token",
		"LiandongUsername":      "provider-user",
		"LiandongPassword":      "provider-password",
	}))
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	require.NoError(t, err)

	var listRequests atomic.Int32
	var loginRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case liandongOrderListPath:
			listRequests.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":401}`))
		case liandongLoginPath:
			loginRequests.Add(1)
			_, _ = w.Write([]byte(`{"token":"unexpected-token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	tradeNo := "TRADE-MANUAL-001"

	verification, err := (&liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	}).queryOrderWithSettings(
		context.Background(),
		settingsSnapshot,
		&model.LiandongOrder{ProviderTradeNo: &tradeNo},
	)

	require.Error(t, err)
	assert.Nil(t, verification)
	assert.Equal(t, int32(1), listRequests.Load())
	assert.Zero(t, loginRequests.Load())
}

func TestLiandongConcurrentUnauthorizedRequestsShareOneLogin(t *testing.T) {
	resetLiandongServiceFixtures(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongAuthMode":      "credentials",
		"LiandongUsername":      "provider-user",
		"LiandongPassword":      "provider-password",
		"LiandongMerchantToken": "stale-token",
	}))
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	require.NoError(t, err)

	const workerCount = 8
	var staleRequests atomic.Int32
	var freshRequests atomic.Int32
	var loginRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case liandongOrderListPath:
			var payload struct {
				TradeNo string `json:"trade_no"`
			}
			if err := common.DecodeJson(r.Body, &payload); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			switch r.Header.Get("merchant-token") {
			case "stale-token":
				staleRequests.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":401}`))
			case "fresh-token":
				freshRequests.Add(1)
				_, _ = w.Write([]byte(fmt.Sprintf(
					`{"list":[{"trade_no":%q,"status":2}]}`,
					payload.TradeNo,
				)))
			default:
				http.Error(w, "unexpected token", http.StatusForbidden)
			}
		case liandongLoginPath:
			loginRequests.Add(1)
			_, _ = w.Write([]byte(`{"merchant_token":"fresh-token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &liandongClient{httpClient: server.Client(), baseURL: server.URL}

	start := make(chan struct{})
	errorsByWorker := make(chan error, workerCount)
	var waitGroup sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			<-start
			tradeNo := fmt.Sprintf("TRADE-CONCURRENT-%03d", worker)
			_, queryErr := client.queryOrderWithSettings(
				context.Background(),
				settingsSnapshot,
				&model.LiandongOrder{ProviderTradeNo: &tradeNo},
			)
			errorsByWorker <- queryErr
		}(index)
	}
	close(start)
	waitGroup.Wait()
	close(errorsByWorker)

	for queryErr := range errorsByWorker {
		require.NoError(t, queryErr)
	}
	assert.Equal(t, int32(workerCount), staleRequests.Load())
	assert.Equal(t, int32(workerCount), freshRequests.Load())
	assert.Equal(t, int32(1), loginRequests.Load())
}

func TestSanitizeLiandongDiagnosticRedactsSecretsBeforeTruncation(t *testing.T) {
	diagnostic := sanitizeLiandongDiagnostic(
		"token=prefix-secret juuid=prefix\nencoded=merchant+token",
		"prefix",
		"prefix-secret",
		"merchant token",
	)

	assert.Equal(
		t,
		"token=[redacted] juuid=[redacted] encoded=[redacted]",
		diagnostic,
	)
}

func TestCreateLiandongPaymentWaitsForConcurrentCreationBeforeReplacing(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user := &model.User{
		Username: "liandong-concurrent-replacement-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	firstProduct := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		GoodsType:           "card",
		Name:                "Concurrent first product",
		GoodsKey:            "concurrent-first-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		InventoryMode:       model.LiandongInventoryModeRedemptionCode,
		InventoryCapacity:   1,
		Enabled:             true,
	}
	secondProduct := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		GoodsType:           "card",
		Name:                "Concurrent replacement product",
		GoodsKey:            "concurrent-replacement-goods",
		QuotaAmount:         200,
		ExpectedAmountMinor: 200,
		Currency:            "CNY",
		InventoryMode:       model.LiandongInventoryModeRedemptionCode,
		InventoryCapacity:   1,
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(firstProduct).Error)
	require.NoError(t, model.DB.Create(secondProduct).Error)
	_, err := model.AddLiandongInventoryCodes(firstProduct.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)
	_, err = model.AddLiandongInventoryCodes(secondProduct.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "true",
		"LiandongCreateEnabled":    "true",
		"LiandongReconcileEnabled": "true",
		"LiandongJUUID":            "test-merchant-id",
		"LiandongMerchantToken":    "secret-token",
	}))

	firstProviderStarted := make(chan struct{})
	releaseFirstProvider := make(chan struct{})
	defer func() {
		select {
		case <-releaseFirstProvider:
		default:
			close(releaseFirstProvider)
		}
	}()
	var createRequests atomic.Int32
	var exactRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case liandongCreatePath:
			count := createRequests.Add(1)
			if count == 1 {
				close(firstProviderStarted)
				<-releaseFirstProvider
			}
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"payUrl":"/shopApi/Pay/payment?trade_no=TRADE-CONCURRENT-%d"}`,
				count,
			)))
		case liandongOrderListPath:
			exactRequests.Add(1)
			_, _ = w.Write([]byte(
				`{"list":[{"trade_no":"TRADE-CONCURRENT-1","status":2}]}`,
			))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &liandongClient{httpClient: server.Client(), baseURL: server.URL}

	type liandongCreateCallResult struct {
		view *LiandongPaymentView
		err  error
	}
	firstResults := make(chan liandongCreateCallResult, 1)
	go func() {
		view, createErr := createLiandongPayment(
			context.Background(),
			user.Id,
			firstProduct.ID,
			client,
		)
		firstResults <- liandongCreateCallResult{view: view, err: createErr}
	}()
	select {
	case <-firstProviderStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first provider request did not start")
	}

	secondResults := make(chan liandongCreateCallResult, 1)
	go func() {
		view, createErr := createLiandongPayment(
			context.Background(),
			user.Id,
			secondProduct.ID,
			client,
		)
		secondResults <- liandongCreateCallResult{view: view, err: createErr}
	}()

	var earlySecondResult *liandongCreateCallResult
	select {
	case result := <-secondResults:
		earlySecondResult = &result
	case <-time.After(200 * time.Millisecond):
	}
	close(releaseFirstProvider)

	firstResult := <-firstResults
	require.NoError(t, firstResult.err)
	require.NotNil(t, firstResult.view)
	var secondResult liandongCreateCallResult
	if earlySecondResult != nil {
		secondResult = *earlySecondResult
	} else {
		secondResult = <-secondResults
	}
	require.NoError(t, secondResult.err)
	require.NotNil(t, secondResult.view)

	assert.EqualValues(t, 2, createRequests.Load())
	assert.Zero(t, exactRequests.Load())
	firstStored, err := model.GetLiandongOrder(firstResult.view.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, firstStored.PaymentStatus)
	assert.Empty(t, firstStored.ClosedReason)
	secondStored, err := model.GetLiandongOrder(secondResult.view.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, secondStored.PaymentStatus)
	assert.Equal(t, secondProduct.ID, secondStored.ProductID)
	var activeOrderCount int64
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("user_id = ? AND payment_status IN ?", user.Id, []string{
			model.LiandongPaymentStatusCreating,
			model.LiandongPaymentStatusPending,
			model.LiandongPaymentStatusCreateUnknown,
		}).
		Count(&activeOrderCount).Error)
	assert.EqualValues(t, 2, activeOrderCount)
	summaries, err := model.GetLiandongInventorySummaries(
		[]int{firstProduct.ID, secondProduct.ID},
	)
	require.NoError(t, err)
	assert.EqualValues(t, 1, summaries[firstProduct.ID].Available)
	assert.Zero(t, summaries[firstProduct.ID].Reserved)
	assert.Zero(t, summaries[secondProduct.ID].Available)
	assert.EqualValues(t, 1, summaries[secondProduct.ID].Reserved)
}

func TestLiandongCloseCannotInterruptProviderOrderCreation(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user := &model.User{
		Username: "liandong-close-during-create-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Close during creation product",
		GoodsKey:            "close-during-creation-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":       "true",
		"LiandongCreateEnabled": "true",
		"LiandongJUUID":         "test-merchant-id",
	}))

	providerStarted := make(chan struct{})
	releaseProvider := make(chan struct{})
	defer func() {
		select {
		case <-releaseProvider:
		default:
			close(releaseProvider)
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != liandongCreatePath {
			http.NotFound(w, r)
			return
		}
		close(providerStarted)
		<-releaseProvider
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"payUrl":"/shopApi/Pay/payment?trade_no=TRADE-CLOSE-RACE"}`,
		))
	}))
	defer server.Close()

	type liandongCreateCallResult struct {
		view *LiandongPaymentView
		err  error
	}
	createResults := make(chan liandongCreateCallResult, 1)
	go func() {
		view, createErr := createLiandongPayment(
			context.Background(),
			user.Id,
			product.ID,
			&liandongClient{httpClient: server.Client(), baseURL: server.URL},
		)
		createResults <- liandongCreateCallResult{view: view, err: createErr}
	}()
	select {
	case <-providerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("provider request did not start")
	}

	activeOrder, err := model.FindActiveLiandongOrderForUser(user.Id)
	require.NoError(t, err)
	require.NotNil(t, activeOrder)
	require.Equal(t, model.LiandongPaymentStatusCreating, activeOrder.PaymentStatus)
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = CloseLiandongPaymentForUser(
		cancelledContext,
		user.Id,
		activeOrder.LocalTradeNo,
	)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = CloseLiandongPaymentForRoot(cancelledContext, activeOrder.LocalTradeNo)
	assert.ErrorIs(t, err, context.Canceled)

	stillCreating, err := model.GetLiandongOrder(activeOrder.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusCreating, stillCreating.PaymentStatus)
	close(releaseProvider)
	callResult := <-createResults
	require.NoError(t, callResult.err)
	require.NotNil(t, callResult.view)
	stored, err := model.GetLiandongOrder(activeOrder.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, stored.PaymentStatus)
	assert.NotNil(t, stored.ProviderTradeNo)
}

func TestCreateLiandongPaymentCreatesNewProviderOrderEachTime(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user := &model.User{
		Username: "liandong-new-order-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "New order product",
		GoodsKey:            "new-order-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":             "true",
		"LiandongCreateEnabled":       "true",
		"LiandongReconcileEnabled":    "true",
		"LiandongPollIntervalSeconds": "15",
		"LiandongJUUID":               "test-merchant-id",
		"LiandongMerchantToken":       "secret-token",
	}))

	var createRequestCount atomic.Int32
	var queryRequestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case liandongCreatePath:
			count := createRequestCount.Add(1)
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"payUrl":"/shopApi/Pay/payment?trade_no=TRADE%03d"}`,
				count,
			)))
		case liandongOrderListPath:
			queryRequestCount.Add(1)
			assert.Equal(t, "secret-token", r.Header.Get("merchant-token"))
			_, _ = w.Write([]byte(`{"list":[{"trade_no":"TRADE001","status":2}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &liandongClient{httpClient: server.Client(), baseURL: server.URL}

	first, err := createLiandongPayment(context.Background(), user.Id, product.ID, client)
	require.NoError(t, err)
	second, err := createLiandongPayment(context.Background(), user.Id, product.ID, client)
	require.NoError(t, err)

	assert.EqualValues(t, 2, createRequestCount.Load())
	assert.Zero(t, queryRequestCount.Load())
	assert.NotEqual(t, first.LocalTradeNo, second.LocalTradeNo)
	assert.NotEqual(t, first.PaymentURL, second.PaymentURL)
	assert.Contains(t, first.PaymentURL, "TRADE001")
	assert.Contains(t, second.PaymentURL, "TRADE002")
	firstStored, err := model.GetLiandongOrder(first.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, firstStored.PaymentStatus)
	assert.Empty(t, firstStored.ClosedReason)
}

func TestCreateLiandongPaymentReplacesFiniteInventoryReservation(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user := &model.User{
		Username: "liandong-replace-inventory-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	firstProduct := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		GoodsType:           "card",
		Name:                "First finite product",
		GoodsKey:            "first-finite-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		InventoryMode:       model.LiandongInventoryModeRedemptionCode,
		InventoryCapacity:   1,
		Enabled:             true,
	}
	secondProduct := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		GoodsType:           "card",
		Name:                "Second finite product",
		GoodsKey:            "second-finite-goods",
		QuotaAmount:         200,
		ExpectedAmountMinor: 200,
		Currency:            "CNY",
		InventoryMode:       model.LiandongInventoryModeRedemptionCode,
		InventoryCapacity:   1,
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(firstProduct).Error)
	require.NoError(t, model.DB.Create(secondProduct).Error)
	_, err := model.AddLiandongInventoryCodes(firstProduct.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)
	_, err = model.AddLiandongInventoryCodes(secondProduct.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "true",
		"LiandongCreateEnabled":    "true",
		"LiandongReconcileEnabled": "true",
		"LiandongJUUID":            "test-merchant-id",
		"LiandongMerchantToken":    "secret-token",
	}))

	var createRequests atomic.Int32
	var exactRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case liandongCreatePath:
			count := createRequests.Add(1)
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"payUrl":"/shopApi/Pay/payment?trade_no=TRADE-REPLACE-%d"}`,
				count,
			)))
		case liandongOrderListPath:
			exactRequests.Add(1)
			_, _ = w.Write([]byte(
				`{"list":[{"trade_no":"TRADE-REPLACE-1","status":2}]}`,
			))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &liandongClient{httpClient: server.Client(), baseURL: server.URL}

	first, err := createLiandongPayment(
		context.Background(),
		user.Id,
		firstProduct.ID,
		client,
	)
	require.NoError(t, err)
	second, err := createLiandongPayment(
		context.Background(),
		user.Id,
		secondProduct.ID,
		client,
	)

	require.NoError(t, err)
	assert.EqualValues(t, 2, createRequests.Load())
	assert.Zero(t, exactRequests.Load())
	firstStored, err := model.GetLiandongOrder(first.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, firstStored.PaymentStatus)
	assert.Empty(t, firstStored.ClosedReason)
	secondStored, err := model.GetLiandongOrder(second.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, secondProduct.ID, secondStored.ProductID)
	assert.Equal(t, model.LiandongPaymentStatusPending, secondStored.PaymentStatus)
	var activeOrderCount int64
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("user_id = ? AND payment_status IN ?", user.Id, []string{
			model.LiandongPaymentStatusCreating,
			model.LiandongPaymentStatusPending,
			model.LiandongPaymentStatusCreateUnknown,
		}).
		Count(&activeOrderCount).Error)
	assert.EqualValues(t, 2, activeOrderCount)
	summaries, err := model.GetLiandongInventorySummaries(
		[]int{firstProduct.ID, secondProduct.ID},
	)
	require.NoError(t, err)
	assert.EqualValues(t, 1, summaries[firstProduct.ID].Available)
	assert.Zero(t, summaries[firstProduct.ID].Reserved)
	assert.Zero(t, summaries[secondProduct.ID].Available)
	assert.EqualValues(t, 1, summaries[secondProduct.ID].Reserved)
}

func TestCreateLiandongPaymentMarksReplacedOrderLateWhenPaymentIsFoundLater(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user := &model.User{
		Username: "liandong-paid-before-replace-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	firstProduct := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		GoodsType:           "card",
		Name:                "Paid finite product",
		GoodsKey:            "paid-finite-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		InventoryMode:       model.LiandongInventoryModeRedemptionCode,
		InventoryCapacity:   1,
		Enabled:             true,
	}
	secondProduct := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		GoodsType:           "card",
		Name:                "Replacement product",
		GoodsKey:            "replacement-goods",
		QuotaAmount:         200,
		ExpectedAmountMinor: 200,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(firstProduct).Error)
	require.NoError(t, model.DB.Create(secondProduct).Error)
	_, err := model.AddLiandongInventoryCodes(firstProduct.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "true",
		"LiandongCreateEnabled":    "true",
		"LiandongReconcileEnabled": "true",
		"LiandongFulfillEnabled":   "true",
		"LiandongJUUID":            "test-merchant-id",
		"LiandongMerchantToken":    "secret-token",
	}))

	var createRequests atomic.Int32
	var exactRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case liandongCreatePath:
			count := createRequests.Add(1)
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"payUrl":"/shopApi/Pay/payment?trade_no=TRADE-PAID-%03d"}`,
				count,
			)))
		case liandongOrderListPath:
			exactRequests.Add(1)
			_, _ = w.Write([]byte(
				`{"list":[{"trade_no":"TRADE-PAID-001","status":1}]}`,
			))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &liandongClient{httpClient: server.Client(), baseURL: server.URL}

	first, err := createLiandongPayment(
		context.Background(),
		user.Id,
		firstProduct.ID,
		client,
	)
	require.NoError(t, err)
	replacementResult, err := createLiandongPayment(
		context.Background(),
		user.Id,
		secondProduct.ID,
		client,
	)

	require.NoError(t, err)
	assert.NotEqual(t, first.LocalTradeNo, replacementResult.LocalTradeNo)
	assert.Equal(t, model.LiandongPaymentStatusPending, replacementResult.PaymentStatus)
	assert.EqualValues(t, 2, createRequests.Load())
	assert.Zero(t, exactRequests.Load())

	transition, err := model.ApplyLiandongPaidTradeNo(
		"TRADE-PAID-001",
		`{"trade_no":"TRADE-PAID-001","status":1}`,
	)
	require.NoError(t, err)
	require.NotNil(t, transition)
	assert.True(t, transition.Late)
	assert.False(t, transition.NewlyPaid)

	firstStored, err := model.GetLiandongOrder(first.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, firstStored.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusReviewRequired, firstStored.FulfillmentStatus)
	assert.Empty(t, firstStored.ClosedReason)
	assert.True(t, firstStored.LatePayment)
	var reloadedUser model.User
	require.NoError(t, model.DB.First(&reloadedUser, user.Id).Error)
	assert.Zero(t, reloadedUser.Quota)
	summaries, err := model.GetLiandongInventorySummaries([]int{firstProduct.ID})
	require.NoError(t, err)
	assert.EqualValues(t, 1, summaries[firstProduct.ID].Available)
	assert.Zero(t, summaries[firstProduct.ID].Reserved)
	assert.Zero(t, summaries[firstProduct.ID].Consumed)
	var replacementOrderCount int64
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("product_id = ?", secondProduct.ID).
		Count(&replacementOrderCount).Error)
	assert.EqualValues(t, 1, replacementOrderCount)
}

func TestCreateLiandongPaymentReplacesOrderWithoutReconciliationAuthentication(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user := &model.User{
		Username: "liandong-create-switch-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	oldProduct := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		GoodsType:           "card",
		Name:                "Old switch product",
		GoodsKey:            "old-switch-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		InventoryMode:       model.LiandongInventoryModeUnlimited,
		Enabled:             true,
	}
	newProduct := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		GoodsType:           "card",
		Name:                "New switch product",
		GoodsKey:            "new-switch-goods",
		QuotaAmount:         200,
		ExpectedAmountMinor: 200,
		Currency:            "CNY",
		InventoryMode:       model.LiandongInventoryModeRedemptionCode,
		InventoryCapacity:   1,
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(oldProduct).Error)
	require.NoError(t, model.DB.Create(newProduct).Error)
	_, err := model.AddLiandongInventoryCodes(newProduct.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)
	oldCreate, err := model.CreateLiandongOrderWithTimeout(
		user.Id,
		oldProduct.ID,
		"123456789012",
		"test-merchant-id",
		30,
	)
	require.NoError(t, err)
	oldProviderTradeNo := "TRADE-SWITCH-OLD"
	require.NoError(t, model.MarkLiandongCreateResult(
		oldCreate.Order.LocalTradeNo,
		&oldProviderTradeNo,
		model.LiandongPaymentStatusPending,
		"",
	))
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":          "true",
		"LiandongCreateEnabled":    "true",
		"LiandongReconcileEnabled": "false",
		"LiandongJUUID":            "test-merchant-id",
		"LiandongMerchantToken":    "",
	}))

	var exactRequests atomic.Int32
	var createRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case liandongOrderListPath:
			exactRequests.Add(1)
			http.Error(w, "verification endpoint should not be called", http.StatusInternalServerError)
		case liandongCreatePath:
			createRequests.Add(1)
			_, _ = w.Write([]byte(
				`{"payUrl":"/shopApi/Pay/payment?trade_no=TRADE-WITHOUT-AUTH"}`,
			))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	view, err := createLiandongPayment(
		context.Background(),
		user.Id,
		newProduct.ID,
		&liandongClient{httpClient: server.Client(), baseURL: server.URL},
	)

	require.NoError(t, err)
	require.NotNil(t, view)
	assert.Zero(t, exactRequests.Load())
	assert.EqualValues(t, 1, createRequests.Load())

	oldOrder, err := model.GetLiandongOrder(oldCreate.Order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, oldOrder.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusWaiting, oldOrder.FulfillmentStatus)
	assert.Empty(t, oldOrder.ClosedReason)

	var newOrder model.LiandongOrder
	require.NoError(t, model.DB.
		Where("user_id = ? AND product_id = ?", user.Id, newProduct.ID).
		First(&newOrder).Error)
	assert.Equal(t, model.LiandongPaymentStatusPending, newOrder.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusWaiting, newOrder.FulfillmentStatus)

	summaries, err := model.GetLiandongInventorySummaries([]int{newProduct.ID})
	require.NoError(t, err)
	assert.Zero(t, summaries[newProduct.ID].Available)
	assert.EqualValues(t, 1, summaries[newProduct.ID].Reserved)
	assert.Zero(t, summaries[newProduct.ID].Consumed)
}

func TestCreateLiandongPaymentHidesPaymentURLWhenGatewayDisabledDuringProviderRequest(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user := &model.User{
		Username: "liandong-emergency-disable-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Emergency disable product",
		GoodsKey:            "emergency-disable-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":       "true",
		"LiandongCreateEnabled": "true",
		"LiandongIframeEnabled": "true",
		"LiandongJUUID":         "test-merchant-id",
	}))

	updateErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		updateErrors <- model.UpdateOptionsBulk(map[string]string{
			"LiandongEnabled": "false",
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"payUrl":"/shopApi/Pay/payment?trade_no=TRADE-DISABLED"}`,
		))
	}))
	defer server.Close()

	view, err := createLiandongPayment(
		context.Background(),
		user.Id,
		product.ID,
		&liandongClient{httpClient: server.Client(), baseURL: server.URL},
	)

	require.NoError(t, <-updateErrors)
	require.NoError(t, err)
	require.NotNil(t, view)
	assert.Empty(t, view.PaymentURL)
	assert.False(t, view.IframeAllowed)

	storedOrder, err := model.GetLiandongOrder(view.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, storedOrder.PaymentStatus)
	require.NotNil(t, storedOrder.ProviderTradeNo)
	assert.Equal(t, "TRADE-DISABLED", *storedOrder.ProviderTradeNo)
}

func TestCreateLiandongPaymentReturnsLocalizedSafeError(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user := &model.User{
		Username: "liandong-provider-error-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Provider error product",
		GoodsKey:            "provider-error-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":       "true",
		"LiandongCreateEnabled": "true",
		"LiandongJUUID":         "test-merchant-id",
		"LiandongMerchantToken": "secret-token",
	}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"code":0,"msg":"商品不存在 goods=provider-error-goods\nJUUID=test-merchant-id token=secret-token"}`,
		))
	}))
	defer server.Close()

	view, err := createLiandongPayment(
		context.Background(),
		user.Id,
		product.ID,
		&liandongClient{httpClient: server.Client(), baseURL: server.URL},
	)

	require.Error(t, err)
	assert.Nil(t, view)
	assert.Equal(t, "Failed to create payment order", err.Error())
	assert.NotContains(t, err.Error(), "provider-error-goods")
	assert.NotContains(t, err.Error(), "test-merchant-id")
	assert.NotContains(t, err.Error(), "secret-token")
	assert.NotContains(t, err.Error(), "\n")

	var storedOrder model.LiandongOrder
	require.NoError(t, model.DB.
		Where("user_id = ?", user.Id).
		Order("id DESC").
		First(&storedOrder).Error)
	assert.Equal(t, model.LiandongPaymentStatusCreateFailed, storedOrder.PaymentStatus)
	assert.Contains(t, storedOrder.LastError, "[redacted]")
	assert.NotContains(t, storedOrder.LastError, "provider-error-goods")
	assert.NotContains(t, storedOrder.LastError, "test-merchant-id")
	assert.NotContains(t, storedOrder.LastError, "secret-token")
	assert.NotContains(t, storedOrder.LastError, "\n")
}

func resetLiandongServiceFixtures(t *testing.T) {
	t.Helper()
	reset := func() {
		model.DB.Exec("DELETE FROM liandong_product_inventory_codes")
		model.DB.Exec("DELETE FROM liandong_product_thumbnails")
		model.DB.Exec("DELETE FROM liandong_user_operation_leases")
		model.DB.Exec("DELETE FROM liandong_orders")
		model.DB.Exec("DELETE FROM liandong_products")
		model.DB.Exec("DELETE FROM top_ups")
		model.DB.Exec("DELETE FROM subscription_orders")
		model.DB.Exec("DELETE FROM subscription_plans")
		model.DB.Exec("DELETE FROM options")
		model.DB.Exec("DELETE FROM users")
	}
	reset()
	t.Cleanup(reset)
}

func createLiandongServiceQuotaOrder(t *testing.T, contact string) (*model.User, *model.LiandongOrder) {
	t.Helper()
	user := &model.User{
		Username: fmt.Sprintf("liandong-service-user-%s", common.GetUUID()),
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Service quota product",
		GoodsKey:            fmt.Sprintf("service-goods-%s", common.GetUUID()),
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	createResult, err := model.CreateLiandongOrder(
		user.Id,
		product.ID,
		contact,
		"test-merchant-id",
	)
	require.NoError(t, err)
	providerTradeNo := "LD" + common.GetRandomString(20)
	require.NoError(t, model.MarkLiandongCreateResult(
		createResult.Order.LocalTradeNo,
		&providerTradeNo,
		model.LiandongPaymentStatusPending,
		"",
	))
	order, err := model.GetLiandongOrder(createResult.Order.LocalTradeNo)
	require.NoError(t, err)
	return user, order
}

func configureLiandongServiceSettings(t *testing.T) {
	t.Helper()
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":             "true",
		"LiandongReconcileEnabled":    "true",
		"LiandongFulfillEnabled":      "true",
		"LiandongPollIntervalSeconds": "15",
		"LiandongMerchantToken":       "secret-token",
	}))
}

func TestManualFulfillLiandongLatePaymentIgnoresAutomaticFulfillmentSwitch(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user, order := createLiandongServiceQuotaOrder(t, "123456789012")
	configureLiandongServiceSettings(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongFulfillEnabled": "false",
	}))
	require.NoError(t, model.CloseLiandongOrder(order.LocalTradeNo))
	transition, err := model.ApplyLiandongPaidTradeNo(*order.ProviderTradeNo, `{"status":1}`)
	require.NoError(t, err)
	require.True(t, transition.Late)

	view, err := ManualFulfillLiandongLatePayment(order.LocalTradeNo)

	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPaid, view.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusFulfilled, view.FulfillmentStatus)
	var reloadedUser model.User
	require.NoError(t, model.DB.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100, reloadedUser.Quota)
}

func TestManualFulfillLiandongLatePaymentRejectsWhenReleasedInventoryWasConsumed(t *testing.T) {
	resetLiandongServiceFixtures(t)
	configureLiandongServiceSettings(t)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		GoodsType:           "card",
		Name:                "Finite late payment product",
		GoodsKey:            fmt.Sprintf("finite-late-%s", common.GetUUID()),
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		InventoryMode:       model.LiandongInventoryModeRedemptionCode,
		InventoryCapacity:   1,
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	_, err := model.AddLiandongInventoryCodes(product.ID, 1, "", common.RoleRootUser)
	require.NoError(t, err)

	firstUser := &model.User{
		Username: fmt.Sprintf("liandong-late-first-%s", common.GetUUID()),
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(firstUser).Error)
	firstResult, err := model.CreateLiandongOrder(
		firstUser.Id,
		product.ID,
		"123456789012",
		"test-merchant-id",
	)
	require.NoError(t, err)
	firstProviderTradeNo := "LD" + common.GetRandomString(20)
	require.NoError(t, model.MarkLiandongCreateResult(
		firstResult.Order.LocalTradeNo,
		&firstProviderTradeNo,
		model.LiandongPaymentStatusPending,
		"",
	))
	require.NoError(t, model.CloseLiandongOrder(firstResult.Order.LocalTradeNo))

	secondUser := &model.User{
		Username: fmt.Sprintf("liandong-late-second-%s", common.GetUUID()),
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(secondUser).Error)
	secondResult, err := model.CreateLiandongOrder(
		secondUser.Id,
		product.ID,
		"123456789013",
		"test-merchant-id",
	)
	require.NoError(t, err)
	secondProviderTradeNo := "LD" + common.GetRandomString(20)
	require.NoError(t, model.MarkLiandongCreateResult(
		secondResult.Order.LocalTradeNo,
		&secondProviderTradeNo,
		model.LiandongPaymentStatusPending,
		"",
	))
	secondTransition, err := model.ApplyLiandongPaidTradeNo(
		secondProviderTradeNo,
		`{"status":1}`,
	)
	require.NoError(t, err)
	require.True(t, secondTransition.NewlyPaid)

	lateTransition, err := model.ApplyLiandongPaidTradeNo(
		firstProviderTradeNo,
		`{"status":1}`,
	)
	require.NoError(t, err)
	require.True(t, lateTransition.Late)

	view, err := ManualFulfillLiandongLatePayment(firstResult.Order.LocalTradeNo)

	require.ErrorIs(t, err, model.ErrLiandongInventoryUnavailable)
	assert.Nil(t, view)
	reloaded, reloadErr := model.GetLiandongOrder(firstResult.Order.LocalTradeNo)
	require.NoError(t, reloadErr)
	assert.Equal(t, model.LiandongPaymentStatusClosed, reloaded.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusReviewRequired, reloaded.FulfillmentStatus)
	assert.True(t, reloaded.LatePayment)
	var reloadedFirstUser model.User
	require.NoError(t, model.DB.First(&reloadedFirstUser, firstUser.Id).Error)
	assert.Zero(t, reloadedFirstUser.Quota)
	summaries, summaryErr := model.GetLiandongInventorySummaries([]int{product.ID})
	require.NoError(t, summaryErr)
	assert.Zero(t, summaries[product.ID].Available)
	assert.Zero(t, summaries[product.ID].Reserved)
	assert.EqualValues(t, 1, summaries[product.ID].Consumed)
}

func TestManualFulfillLiandongLatePaymentRequiresMasterSwitch(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user, order := createLiandongServiceQuotaOrder(t, "123456789012")
	configureLiandongServiceSettings(t)
	require.NoError(t, model.CloseLiandongOrder(order.LocalTradeNo))
	transition, err := model.ApplyLiandongPaidTradeNo(*order.ProviderTradeNo, `{"status":1}`)
	require.NoError(t, err)
	require.True(t, transition.Late)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":        "false",
		"LiandongFulfillEnabled": "true",
	}))

	view, err := ManualFulfillLiandongLatePayment(order.LocalTradeNo)

	require.Error(t, err)
	assert.Nil(t, view)
	reloaded, reloadErr := model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, reloadErr)
	assert.Equal(t, model.LiandongFulfillmentStatusReviewRequired, reloaded.FulfillmentStatus)
	var reloadedUser model.User
	require.NoError(t, model.DB.First(&reloadedUser, user.Id).Error)
	assert.Zero(t, reloadedUser.Quota)
}

func TestRetryLiandongFulfillmentIgnoresAutomaticFulfillmentSwitch(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user, order := createLiandongServiceQuotaOrder(t, "123456789012")
	configureLiandongServiceSettings(t)
	transition, err := model.ApplyLiandongPaidTradeNo(*order.ProviderTradeNo, `{"status":1}`)
	require.NoError(t, err)
	require.True(t, transition.NewlyPaid)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongFulfillEnabled": "false",
	}))

	view, err := RetryLiandongFulfillment(order.LocalTradeNo)

	require.NoError(t, err)
	assert.Equal(t, model.LiandongFulfillmentStatusFulfilled, view.FulfillmentStatus)
	var reloadedUser model.User
	require.NoError(t, model.DB.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100, reloadedUser.Quota)
}

func TestRetryLiandongFulfillmentRequiresMasterSwitch(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user, order := createLiandongServiceQuotaOrder(t, "123456789012")
	configureLiandongServiceSettings(t)
	transition, err := model.ApplyLiandongPaidTradeNo(*order.ProviderTradeNo, `{"status":1}`)
	require.NoError(t, err)
	require.True(t, transition.NewlyPaid)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongEnabled":        "false",
		"LiandongFulfillEnabled": "true",
	}))

	view, err := RetryLiandongFulfillment(order.LocalTradeNo)

	require.Error(t, err)
	assert.Nil(t, view)
	reloaded, reloadErr := model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, reloadErr)
	assert.Equal(t, model.LiandongFulfillmentStatusWaiting, reloaded.FulfillmentStatus)
	var reloadedUser model.User
	require.NoError(t, model.DB.First(&reloadedUser, user.Id).Error)
	assert.Zero(t, reloadedUser.Quota)
}

func TestRunLiandongReconcileOnceExactExpiryCheckWinsAfterBatchShowsUnpaid(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user, order := createLiandongServiceQuotaOrder(t, "123456789012")
	configureLiandongServiceSettings(t)
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("id = ?", order.ID).
		Update("expires_at", common.GetTimestamp()-1).Error)

	type requestPayload struct {
		TradeNo string `json:"trade_no"`
	}
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		var payload requestPayload
		if err := common.DecodeJson(r.Body, &payload); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		status := 2
		if payload.TradeNo == *order.ProviderTradeNo {
			status = 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"list":[{"trade_no":%q,"status":%d}]}`,
			*order.ProviderTradeNo,
			status,
		)))
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.NoError(t, err)
	assert.EqualValues(t, 2, requestCount.Load())
	assert.Equal(t, 1, result["paid"])
	assert.Equal(t, 1, result["fulfilled"])
	reloaded, reloadErr := model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, reloadErr)
	assert.Equal(t, model.LiandongPaymentStatusPaid, reloaded.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusFulfilled, reloaded.FulfillmentStatus)
	assert.False(t, reloaded.LatePayment)
	assert.Empty(t, reloaded.ClosedReason)
	var reloadedUser model.User
	require.NoError(t, model.DB.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100, reloadedUser.Quota)
}

func TestRunLiandongReconcileOnceDoesNotVerifyExpiredProviderOrdersWhenReconciliationDisabled(t *testing.T) {
	tests := []struct {
		name                 string
		masterEnabled        bool
		providerStatus       int
		wantProviderRequests int32
		wantPaymentStatus    string
		wantFulfillment      string
		wantAvailable        int64
		wantReserved         int64
		wantConsumed         int64
		wantQuota            int
	}{
		{
			name:                 "enabled gateway keeps unpaid order reserved",
			masterEnabled:        true,
			providerStatus:       2,
			wantProviderRequests: 0,
			wantPaymentStatus:    model.LiandongPaymentStatusPending,
			wantFulfillment:      model.LiandongFulfillmentStatusWaiting,
			wantReserved:         1,
		},
		{
			name:                 "enabled gateway does not discover paid order",
			masterEnabled:        true,
			providerStatus:       1,
			wantProviderRequests: 0,
			wantPaymentStatus:    model.LiandongPaymentStatusPending,
			wantFulfillment:      model.LiandongFulfillmentStatusWaiting,
			wantReserved:         1,
		},
		{
			name:              "master switch also keeps provider order reserved",
			providerStatus:    2,
			wantPaymentStatus: model.LiandongPaymentStatusPending,
			wantFulfillment:   model.LiandongFulfillmentStatusWaiting,
			wantReserved:      1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetLiandongServiceFixtures(t)
			user := &model.User{
				Username: fmt.Sprintf("liandong-expiry-%s", common.GetUUID()),
				Password: "password",
				Status:   common.UserStatusEnabled,
				Role:     common.RoleCommonUser,
				Group:    "default",
				AffCode:  common.GetRandomString(16),
			}
			require.NoError(t, model.DB.Create(user).Error)
			product := &model.LiandongProduct{
				BusinessType:        model.LiandongBusinessTypeQuota,
				GoodsType:           "card",
				Name:                "Exact expiry inventory product",
				GoodsKey:            fmt.Sprintf("expiry-goods-%s", common.GetUUID()),
				QuotaAmount:         100,
				ExpectedAmountMinor: 100,
				Currency:            "CNY",
				InventoryMode:       model.LiandongInventoryModeRedemptionCode,
				InventoryCapacity:   1,
				Enabled:             true,
			}
			require.NoError(t, model.DB.Create(product).Error)
			_, err := model.AddLiandongInventoryCodes(product.ID, 1, "", common.RoleRootUser)
			require.NoError(t, err)
			createResult, err := model.CreateLiandongOrderWithTimeout(
				user.Id,
				product.ID,
				"123456789012",
				"test-merchant-id",
				1,
			)
			require.NoError(t, err)
			providerTradeNo := "LD" + common.GetRandomString(20)
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
				"LiandongEnabled":          fmt.Sprintf("%t", test.masterEnabled),
				"LiandongReconcileEnabled": "false",
				"LiandongFulfillEnabled":   "true",
				"LiandongAuthMode":         setting.LiandongAuthModeManualToken,
				"LiandongMerchantToken":    "secret-token",
			}))

			type requestPayload struct {
				TradeNo *string `json:"trade_no"`
			}
			var providerRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerRequests.Add(1)
				assert.Equal(t, "secret-token", r.Header.Get("merchant-token"))
				var payload requestPayload
				require.NoError(t, common.DecodeJson(r.Body, &payload))
				require.NotNil(t, payload.TradeNo)
				assert.Equal(t, providerTradeNo, *payload.TradeNo)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(fmt.Sprintf(
					`{"list":[{"trade_no":%q,"status":%d}]}`,
					providerTradeNo,
					test.providerStatus,
				)))
			}))
			defer server.Close()

			result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
				httpClient: server.Client(),
				baseURL:    server.URL,
			})

			require.NoError(t, err)
			assert.Equal(t, test.wantProviderRequests, providerRequests.Load())
			assert.Zero(t, result["processed"])
			reloaded, err := model.GetLiandongOrder(createResult.Order.LocalTradeNo)
			require.NoError(t, err)
			assert.Equal(t, test.wantPaymentStatus, reloaded.PaymentStatus)
			assert.Equal(t, test.wantFulfillment, reloaded.FulfillmentStatus)
			summaries, err := model.GetLiandongInventorySummaries([]int{product.ID})
			require.NoError(t, err)
			assert.Equal(t, test.wantAvailable, summaries[product.ID].Available)
			assert.Equal(t, test.wantReserved, summaries[product.ID].Reserved)
			assert.Equal(t, test.wantConsumed, summaries[product.ID].Consumed)
			var reloadedUser model.User
			require.NoError(t, model.DB.First(&reloadedUser, user.Id).Error)
			assert.Equal(t, test.wantQuota, reloadedUser.Quota)
		})
	}
}

func TestRunLiandongReconcileOnceSkipsOrderClaimedByExactCheck(t *testing.T) {
	resetLiandongServiceFixtures(t)
	_, order := createLiandongServiceQuotaOrder(t, "123456789012")
	configureLiandongServiceSettings(t)
	exactClaim, err := model.ClaimLiandongPendingOrder(order.LocalTradeNo)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"list":[{"trade_no":%q,"status":1}]}`,
			*order.ProviderTradeNo,
		)))
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.NoError(t, err)
	assert.Zero(t, result["processed"])
	assert.Zero(t, result["paid"])
	reloaded, reloadErr := model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, reloadErr)
	assert.Equal(t, model.LiandongPaymentStatusPending, reloaded.PaymentStatus)
	assert.Equal(t, exactClaim.CheckLockUntil, reloaded.CheckLockUntil)
}

func TestRequeueLiandongOrderUsesConfiguredPaymentTimeout(t *testing.T) {
	resetLiandongServiceFixtures(t)
	_, order := createLiandongServiceQuotaOrder(t, "123456789012")
	require.NoError(t, model.CloseLiandongOrder(order.LocalTradeNo))
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongPaymentTimeoutMinutes": "7",
	}))
	before := common.GetTimestamp()

	require.NoError(t, RequeueLiandongOrder(order.LocalTradeNo))

	reloaded, err := model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, reloaded.PaymentStatus)
	assert.Empty(t, reloaded.ClosedReason)
	assert.GreaterOrEqual(t, reloaded.ExpiresAt, before+7*60)
	assert.LessOrEqual(t, reloaded.ExpiresAt, common.GetTimestamp()+7*60)
}

func TestRunLiandongReconcileOnceStopsBatchAfterMasterSwitchDisabled(t *testing.T) {
	resetLiandongServiceFixtures(t)
	_, firstOrder := createLiandongServiceQuotaOrder(t, "123456789012")
	_, secondOrder := createLiandongServiceQuotaOrder(t, "223456789012")
	configureLiandongServiceSettings(t)

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if err := model.UpdateOptionsBulk(map[string]string{"LiandongEnabled": "false"}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"list":[{"trade_no":%q,"status":2}]}`,
			*firstOrder.ProviderTradeNo,
		)))
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.NoError(t, err)
	assert.Equal(t, int32(1), requestCount.Load())
	assert.Zero(t, result["processed"])
	assert.Zero(t, result["paid"])
	firstReloaded, err := model.GetLiandongOrder(firstOrder.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, firstReloaded.PaymentStatus)
	assert.Zero(t, firstReloaded.CheckLockUntil)
	secondReloaded, err := model.GetLiandongOrder(secondOrder.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, secondReloaded.PaymentStatus)
	assert.Zero(t, secondReloaded.CheckLockUntil)
}

func TestRunLiandongReconcileOnceHonorsFulfillmentSwitchChangedDuringQuery(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user, order := createLiandongServiceQuotaOrder(t, "123456789012")
	configureLiandongServiceSettings(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := model.UpdateOptionsBulk(map[string]string{"LiandongFulfillEnabled": "false"}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"list":[{"trade_no":%q,"status":1}]}`,
			*order.ProviderTradeNo,
		)))
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.NoError(t, err)
	assert.Equal(t, 1, result["processed"])
	assert.Equal(t, 1, result["paid"])
	assert.Zero(t, result["fulfilled"])
	reloaded, err := model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPaid, reloaded.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusWaiting, reloaded.FulfillmentStatus)
	var reloadedUser model.User
	require.NoError(t, model.DB.First(&reloadedUser, user.Id).Error)
	assert.Zero(t, reloadedUser.Quota)
}

func TestRunLiandongReconcileOnceFulfillsPaidOrderAfterActivationReenabled(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user, order := createLiandongServiceQuotaOrder(t, "123456789012")
	configureLiandongServiceSettings(t)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongFulfillEnabled": "false",
	}))

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"list":[{"trade_no":%q,"status":1}]}`,
			*order.ProviderTradeNo,
		)))
	}))
	defer server.Close()
	client := &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	firstResult, err := runLiandongReconcileOnce(context.Background(), client)
	require.NoError(t, err)
	assert.Equal(t, 1, firstResult["paid"])
	assert.Zero(t, firstResult["fulfilled"])

	reloaded, err := model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPaid, reloaded.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusWaiting, reloaded.FulfillmentStatus)
	var reloadedUser model.User
	require.NoError(t, model.DB.First(&reloadedUser, user.Id).Error)
	assert.Zero(t, reloadedUser.Quota)

	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongFulfillEnabled": "true",
	}))
	secondResult, err := runLiandongReconcileOnce(context.Background(), client)
	require.NoError(t, err)
	assert.Zero(t, secondResult["paid"])
	assert.Equal(t, 1, secondResult["fulfilled"])
	assert.EqualValues(t, 2, requestCount.Load())

	reloaded, err = model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPaid, reloaded.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusFulfilled, reloaded.FulfillmentStatus)
	require.NoError(t, model.DB.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100, reloadedUser.Quota)
}

func TestRunLiandongReconcileOnceStopsAfterSystemicProviderError(t *testing.T) {
	resetLiandongServiceFixtures(t)
	orders := make([]*model.LiandongOrder, 0, 8)
	for index := 0; index < 8; index++ {
		_, order := createLiandongServiceQuotaOrder(t, fmt.Sprintf("%012d", 300000000000+index))
		orders = append(orders, order)
	}
	configureLiandongServiceSettings(t)

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.Error(t, err)
	assert.Equal(t, int32(1), requestCount.Load())
	assert.Equal(t, 1, result["failed"])
	assert.Zero(t, result["processed"])
	for _, order := range orders {
		reloaded, reloadErr := model.GetLiandongOrder(order.LocalTradeNo)
		require.NoError(t, reloadErr)
		assert.Equal(t, model.LiandongPaymentStatusPending, reloaded.PaymentStatus)
		assert.Zero(t, reloaded.CheckLockUntil)
	}
}

func TestRunLiandongReconcileOnceStopsAfterProviderBusinessRejection(t *testing.T) {
	resetLiandongServiceFixtures(t)
	orders := make([]*model.LiandongOrder, 0, 8)
	for index := 0; index < 8; index++ {
		_, order := createLiandongServiceQuotaOrder(t, fmt.Sprintf("%012d", 400000000000+index))
		orders = append(orders, order)
	}
	configureLiandongServiceSettings(t)

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"token invalid","data":null}`))
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.Error(t, err)
	assert.Equal(t, int32(1), requestCount.Load())
	assert.Equal(t, 1, result["failed"])
	assert.Zero(t, result["processed"])
	for _, order := range orders {
		reloaded, reloadErr := model.GetLiandongOrder(order.LocalTradeNo)
		require.NoError(t, reloadErr)
		assert.Equal(t, model.LiandongPaymentStatusPending, reloaded.PaymentStatus)
		assert.Zero(t, reloaded.CheckLockUntil)
	}
}

func TestRunLiandongReconcileOnceStopsOnProviderRejectionWithoutExpiringOrder(t *testing.T) {
	resetLiandongServiceFixtures(t)
	_, order := createLiandongServiceQuotaOrder(t, "123456789012")
	legacyDeadlineAt := common.GetTimestamp() - 10
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("local_trade_no = ?", order.LocalTradeNo).
		Update("check_deadline_at", legacyDeadlineAt).Error)
	configureLiandongServiceSettings(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"code":0,"msg":"token invalid secret-token merchant=test-merchant-id\nretry","data":null}`,
		))
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.Error(t, err)
	assert.Zero(t, result["processed"])
	assert.Equal(t, 1, result["failed"])
	assert.Zero(t, result["paid"])

	reloaded, err := model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusPending, reloaded.PaymentStatus)
	assert.Zero(t, reloaded.NextCheckAt)
	assert.Equal(t, legacyDeadlineAt, reloaded.CheckDeadlineAt)
	assert.Zero(t, reloaded.ConsecutiveErrorCount)
	assert.Empty(t, reloaded.LastError)
	assert.Zero(t, reloaded.CheckLockUntil)
}

func TestRunLiandongReconcileOnceRedactsTokenUsedBeforeRotation(t *testing.T) {
	resetLiandongServiceFixtures(t)
	_, order := createLiandongServiceQuotaOrder(t, "123456789012")
	configureLiandongServiceSettings(t)

	oldToken := "rotated-old-" + strings.Repeat("sensitive", 40)
	const newToken = "rotated-new-secret-token"
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"LiandongMerchantToken": oldToken,
	}))
	requestTokens := make(chan string, 1)
	rotationErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestTokens <- r.Header.Get("merchant-token")
		rotationErr := model.UpdateOptionsBulk(map[string]string{
			"LiandongMerchantToken": newToken,
		})
		rotationErrors <- rotationErr
		if rotationErr != nil {
			http.Error(w, "failed to rotate merchant token", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"code":0,"msg":"token invalid: %s","data":null}`,
			oldToken,
		)))
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.Error(t, err)
	require.Equal(t, 1, len(requestTokens))
	assert.Equal(t, oldToken, <-requestTokens)
	require.Equal(t, 1, len(rotationErrors))
	require.NoError(t, <-rotationErrors)
	assert.Zero(t, result["processed"])
	assert.Equal(t, 1, result["failed"])

	reloaded, err := model.GetLiandongOrder(order.LocalTradeNo)
	require.NoError(t, err)
	assert.Empty(t, reloaded.LastError)
}

func TestRunLiandongReconcileOnceMovesStaleCreatingOrderToReview(t *testing.T) {
	resetLiandongServiceFixtures(t)
	user := &model.User{
		Username: "liandong-expired-creating-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  common.GetRandomString(16),
	}
	require.NoError(t, model.DB.Create(user).Error)
	product := &model.LiandongProduct{
		BusinessType:        model.LiandongBusinessTypeQuota,
		Name:                "Expired creating product",
		GoodsKey:            "expired-creating-goods",
		QuotaAmount:         100,
		ExpectedAmountMinor: 100,
		Currency:            "CNY",
		Enabled:             true,
	}
	require.NoError(t, model.DB.Create(product).Error)
	createResult, err := model.CreateLiandongOrder(
		user.Id,
		product.ID,
		"123456789012",
		"test-merchant-id",
	)
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.LiandongOrder{}).
		Where("local_trade_no = ?", createResult.Order.LocalTradeNo).
		Update("created_at", common.GetTimestamp()-301).Error)
	configureLiandongServiceSettings(t)

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"list":[]}`))
	}))
	defer server.Close()

	result, err := runLiandongReconcileOnce(context.Background(), &liandongClient{
		httpClient: server.Client(),
		baseURL:    server.URL,
	})

	require.NoError(t, err)
	assert.EqualValues(t, 1, requestCount.Load())
	assert.Equal(t, 1, result["processed"])
	assert.Equal(t, 1, result["failed"])
	assert.Zero(t, result["paid"])
	assert.Zero(t, result["fulfilled"])

	reloaded, err := model.GetLiandongOrder(createResult.Order.LocalTradeNo)
	require.NoError(t, err)
	assert.Equal(t, model.LiandongPaymentStatusCreateUnknown, reloaded.PaymentStatus)
	assert.Equal(t, model.LiandongFulfillmentStatusReviewRequired, reloaded.FulfillmentStatus)
	assert.Nil(t, reloaded.ProviderTradeNo)
	assert.Zero(t, reloaded.CheckLockUntil)

	var reloadedUser model.User
	require.NoError(t, model.DB.First(&reloadedUser, user.Id).Error)
	assert.Zero(t, reloadedUser.Quota)

	var topUp model.TopUp
	require.NoError(t, model.DB.Where("trade_no = ?", createResult.Order.LocalTradeNo).First(&topUp).Error)
	assert.Equal(t, common.TopUpStatusPending, topUp.Status)
}
