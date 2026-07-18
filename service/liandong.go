package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	xproxy "golang.org/x/net/proxy"
)

const (
	liandongBaseURL               = setting.DefaultLiandongBaseURL
	liandongCreatePath            = "/shopApi/Pay/order"
	liandongPaymentPath           = "/shopApi/Pay/payment"
	liandongOrderListPath         = "/merchantApi/order/list"
	liandongGoodsListPath         = "/merchantApi/Goods/list"
	liandongLoginPath             = "/merchantApi/user/login"
	liandongMaxBodyBytes          = 1 << 20
	liandongDialTimeout           = 10 * time.Second
	liandongTLSHandshakeTimeout   = 15 * time.Second
	liandongResponseHeaderTimeout = 20 * time.Second
	liandongRequestTimeout        = 30 * time.Second
	liandongMaxDiagnosticRunes    = 256
	liandongReconcileBatchSize    = 100
	liandongOperationLeaseTTL     = 90 * time.Second
	liandongOperationWait         = 15 * time.Second
	liandongOperationRetry        = 100 * time.Millisecond
)

var liandongTradeNoPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,128}$`)
var liandongSensitiveDiagnosticValuePattern = regexp.MustCompile(
	`(?i)(["']?(?:merchant[-_ ]?token|token|password|username|juuid|contact|goods[-_ ]?key)["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^,\s;&]+)`,
)
var liandongAuthRefreshMu sync.Mutex

type LiandongPaymentView struct {
	LocalTradeNo              string `json:"local_trade_no"`
	ProductName               string `json:"product_name"`
	BusinessType              string `json:"business_type"`
	PaymentStatus             string `json:"payment_status"`
	FulfillmentStatus         string `json:"fulfillment_status"`
	PaymentURL                string `json:"payment_url,omitempty"`
	IframeAllowed             bool   `json:"iframe_allowed"`
	CreatedAt                 int64  `json:"created_at"`
	PaidAt                    int64  `json:"paid_at,omitempty"`
	FulfilledAt               int64  `json:"fulfilled_at,omitempty"`
	ExpiresAt                 int64  `json:"expires_at,omitempty"`
	LatePayment               bool   `json:"late_payment,omitempty"`
	ClientPollIntervalSeconds int    `json:"client_poll_interval_seconds"`
}

type liandongClient struct {
	httpClient *http.Client
	baseURL    string
	configErr  error
}

type liandongCreateError struct {
	definitive bool
	err        error
}

type liandongQueryError struct {
	statusCode int
	systemic   bool
	err        error
}

func (e *liandongQueryError) Error() string {
	return e.err.Error()
}

func (e *liandongQueryError) Unwrap() error {
	return e.err
}

func (e *liandongCreateError) Error() string {
	return e.err.Error()
}

func (e *liandongCreateError) Unwrap() error {
	return e.err
}

type liandongProviderRejection struct {
	message string
}

func (e *liandongProviderRejection) Error() string {
	if e.message == "" {
		return "provider rejected order"
	}
	return "provider rejected order: " + e.message
}

type liandongVerification struct {
	Paid             bool
	ReviewRequired   bool
	SanitizedSummary string
}

type liandongCreateResponse struct {
	Code      json.RawMessage `json:"code"`
	Message   string          `json:"msg"`
	MessageV2 string          `json:"message"`
	PayURL    string          `json:"payUrl"`
	PayURLV2  string          `json:"pay_url"`
	TradeNo   string          `json:"trade_no"`
	TradeNoV2 string          `json:"tradeNo"`
	Data      json.RawMessage `json:"data"`
}

type liandongCreateResponseData struct {
	PayURL    string `json:"payUrl"`
	PayURLV2  string `json:"pay_url"`
	TradeNo   string `json:"trade_no"`
	TradeNoV2 string `json:"tradeNo"`
}

type liandongOrderRecord struct {
	TradeNo   string          `json:"trade_no"`
	TradeNoV2 string          `json:"tradeNo"`
	Status    json.RawMessage `json:"status"`
}

type liandongOrderListResponse struct {
	Code      json.RawMessage       `json:"code"`
	Message   string                `json:"msg"`
	MessageV2 string                `json:"message"`
	List      []liandongOrderRecord `json:"list"`
	Records   []liandongOrderRecord `json:"records"`
	Items     []liandongOrderRecord `json:"items"`
	Data      json.RawMessage       `json:"data"`
}

type liandongOrderListData struct {
	List    []liandongOrderRecord `json:"list"`
	Records []liandongOrderRecord `json:"records"`
	Items   []liandongOrderRecord `json:"items"`
}

type liandongLoginResponse struct {
	Code          json.RawMessage `json:"code"`
	Message       string          `json:"msg"`
	MessageV2     string          `json:"message"`
	Token         string          `json:"token"`
	MerchantToken string          `json:"merchant_token"`
	MerchantDash  string          `json:"merchant-token"`
	Data          json.RawMessage `json:"data"`
}

type liandongLoginResponseData struct {
	Token         string `json:"token"`
	MerchantToken string `json:"merchant_token"`
	MerchantDash  string `json:"merchant-token"`
}

type LiandongProviderGoods struct {
	GoodsKey  string `json:"goods_key"`
	Name      string `json:"name"`
	GoodsType string `json:"goods_type"`
}

type liandongGoodsRecord struct {
	GoodsKey  string `json:"goods_key"`
	GoodsKey2 string `json:"goodsKey"`
	Name      string `json:"name"`
	GoodsName string `json:"goods_name"`
	GoodsType string `json:"goods_type"`
}

type liandongGoodsListResponse struct {
	Code      json.RawMessage       `json:"code"`
	Message   string                `json:"msg"`
	MessageV2 string                `json:"message"`
	List      []liandongGoodsRecord `json:"list"`
	Records   []liandongGoodsRecord `json:"records"`
	Items     []liandongGoodsRecord `json:"items"`
	Data      json.RawMessage       `json:"data"`
}

type liandongGoodsListData struct {
	List    []liandongGoodsRecord `json:"list"`
	Records []liandongGoodsRecord `json:"records"`
	Items   []liandongGoodsRecord `json:"items"`
}

func newLiandongClient() *liandongClient {
	settingsSnapshot := setting.LiandongPaymentSettings{
		BaseURL: setting.DefaultLiandongBaseURL,
	}
	if loaded, err := model.GetLiandongPaymentSettingsFromDB(); err == nil {
		settingsSnapshot = loaded
	}
	return newLiandongClientWithSettings(settingsSnapshot)
}

func newLiandongClientWithSettings(
	settingsSnapshot setting.LiandongPaymentSettings,
) *liandongClient {
	baseURL, configErr := setting.NormalizeLiandongBaseURL(settingsSnapshot.BaseURL)
	proxyFunc := http.ProxyFromEnvironment
	var dialContext func(context.Context, string, string) (net.Conn, error)
	dialTimeout := liandongDialTimeout
	tlsHandshakeTimeout := liandongTLSHandshakeTimeout
	responseHeaderTimeout := liandongResponseHeaderTimeout
	requestTimeout := liandongRequestTimeout
	if settingsSnapshot.ProxyEnabled {
		proxyTimeoutSeconds := settingsSnapshot.ProxyTimeoutSeconds
		if proxyTimeoutSeconds < setting.MinLiandongProxyTimeoutSeconds ||
			proxyTimeoutSeconds > setting.MaxLiandongProxyTimeoutSeconds {
			proxyTimeoutSeconds = setting.DefaultLiandongProxyTimeoutSeconds
		}
		proxyTimeout := time.Duration(proxyTimeoutSeconds) * time.Second
		dialTimeout = proxyTimeout
		tlsHandshakeTimeout = proxyTimeout
		responseHeaderTimeout = proxyTimeout
		requestTimeout = proxyTimeout

		configuredProxyFunc, proxyDialContext, proxyConfigErr := liandongProxyTransport(
			settingsSnapshot,
		)
		if proxyConfigErr != nil {
			if configErr == nil {
				configErr = proxyConfigErr
			}
			proxyErr := proxyConfigErr
			proxyFunc = nil
			dialContext = func(context.Context, string, string) (net.Conn, error) {
				return nil, proxyErr
			}
		} else {
			proxyFunc = configuredProxyFunc
			dialContext = proxyDialContext
		}
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil && configErr == nil {
		configErr = err
	}
	dialer := &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		Proxy:                 proxyFunc,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   5,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	if dialContext != nil {
		transport.DialContext = dialContext
	}
	return &liandongClient{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return errors.New("too many redirects")
				}
				if parsedBaseURL == nil ||
					!strings.EqualFold(req.URL.Scheme, parsedBaseURL.Scheme) ||
					!strings.EqualFold(req.URL.Host, parsedBaseURL.Host) {
					return errors.New("redirect target is not allowed")
				}
				return nil
			},
		},
		baseURL:   baseURL,
		configErr: configErr,
	}
}

func liandongProxyTransport(
	settingsSnapshot setting.LiandongPaymentSettings,
) (
	func(*http.Request) (*url.URL, error),
	func(context.Context, string, string) (net.Conn, error),
	error,
) {
	config, err := setting.ParseLiandongProxy(settingsSnapshot.ProxyURL)
	if err != nil {
		return nil, nil, err
	}
	if config.URL == "" {
		return nil, nil, errors.New("proxy URL is required when the proxy is enabled")
	}
	username := config.Username
	password := config.Password
	if username == "" && password == "" {
		username = strings.TrimSpace(settingsSnapshot.ProxyUsername)
		password = settingsSnapshot.ProxyPassword
	}
	hasUsername := username != ""
	hasPassword := password != ""
	if hasUsername != hasPassword {
		return nil, nil, errors.New("proxy username and password must be configured together")
	}
	parsed, err := url.Parse(config.URL)
	if err != nil {
		return nil, nil, errors.New("invalid proxy URL")
	}
	if hasUsername {
		parsed.User = url.UserPassword(username, password)
	}
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		return http.ProxyURL(parsed), nil, nil
	}
	var auth *xproxy.Auth
	if hasUsername {
		auth = &xproxy.Auth{User: username, Password: password}
	}
	dialer, err := xproxy.SOCKS5("tcp", parsed.Host, auth, xproxy.Direct)
	if err != nil {
		return nil, nil, errors.New("SOCKS5 proxy dialer could not be created")
	}
	if contextDialer, ok := dialer.(xproxy.ContextDialer); ok {
		return nil, contextDialer.DialContext, nil
	}
	return nil, func(ctx context.Context, network, address string) (net.Conn, error) {
		type dialResult struct {
			conn net.Conn
			err  error
		}
		resultCh := make(chan dialResult, 1)
		go func() {
			conn, err := dialer.Dial(network, address)
			resultCh <- dialResult{conn: conn, err: err}
		}()
		select {
		case result := <-resultCh:
			return result.conn, result.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, nil
}

func ValidateLiandongProxy(
	ctx context.Context,
	settingsSnapshot setting.LiandongPaymentSettings,
) error {
	if strings.TrimSpace(settingsSnapshot.ProxyURL) == "" {
		return errors.New("proxy URL is required")
	}
	settingsSnapshot.ProxyEnabled = true
	client := newLiandongClientWithSettings(settingsSnapshot)
	if client.configErr != nil {
		return client.configErr
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL, nil)
	if err != nil {
		return errors.New("proxy URL is invalid")
	}
	req.Header.Set("Accept", "*/*")
	validationClient := *client.httpClient
	validationClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := validationClient.Do(req)
	if err != nil {
		return classifyLiandongProxyError(err, settingsSnapshot)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusProxyAuthRequired {
		return errors.New("Proxy authentication failed")
	}
	return nil
}

func classifyLiandongProxyError(
	err error,
	settingsSnapshot setting.LiandongPaymentSettings,
) error {
	if err == nil {
		return nil
	}
	var unknownAuthorityError x509.UnknownAuthorityError
	var certificateInvalidError x509.CertificateInvalidError
	lower := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		strings.Contains(lower, "i/o timeout"),
		strings.Contains(lower, "timed out"):
		return errors.New("Proxy connection timed out")
	case strings.Contains(lower, "connection refused"):
		return errors.New("Proxy connection refused")
	case strings.Contains(lower, "authentication failed"),
		strings.Contains(lower, "username/password"),
		strings.Contains(lower, "no acceptable authentication"),
		strings.Contains(lower, "proxy authentication required"),
		strings.Contains(lower, "status code 407"):
		return errors.New("Proxy authentication failed")
	case strings.Contains(lower, "no such host"),
		strings.Contains(lower, "temporary failure in name resolution"),
		strings.Contains(lower, "name or service not known"):
		return errors.New("Proxy host could not be resolved")
	case errors.As(err, &unknownAuthorityError),
		errors.As(err, &certificateInvalidError),
		strings.Contains(lower, "x509:"),
		strings.Contains(lower, "tls: failed to verify certificate"):
		return errors.New("Proxy TLS certificate validation failed")
	default:
		proxyUsername := settingsSnapshot.ProxyUsername
		proxyPassword := settingsSnapshot.ProxyPassword
		if config, parseErr := setting.ParseLiandongProxy(settingsSnapshot.ProxyURL); parseErr == nil &&
			config.Username != "" {
			proxyUsername = config.Username
			proxyPassword = config.Password
		}
		return fmt.Errorf(
			"Proxy connection failed: %s",
			sanitizeLiandongDiagnostic(
				err.Error(),
				proxyUsername,
				proxyPassword,
			),
		)
	}
}

func (c *liandongClient) createOrder(
	ctx context.Context,
	goodsKey string,
	contact string,
	juuid string,
) (string, error) {
	payload := struct {
		GoodsKey   string            `json:"goods_key"`
		Quantity   int               `json:"quantity"`
		CouponCode string            `json:"coupon_code"`
		ChannelID  int               `json:"channel_id"`
		Contact    string            `json:"contact"`
		Extend     map[string]string `json:"extend"`
	}{
		GoodsKey:   goodsKey,
		Quantity:   1,
		CouponCode: "",
		ChannelID:  1,
		Contact:    contact,
		Extend: map[string]string{
			"juuid": juuid,
		},
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return "", err
	}
	statusCode, responseBody, err := c.doJSON(ctx, http.MethodPost, liandongCreatePath, body, "")
	if err != nil {
		return "", &liandongCreateError{definitive: false, err: err}
	}
	if statusCode < 200 || statusCode >= 300 {
		providerMessage := ""
		var payload liandongCreateResponse
		if err := common.Unmarshal(responseBody, &payload); err == nil {
			providerMessage = strings.TrimSpace(payload.Message)
			if providerMessage == "" {
				providerMessage = strings.TrimSpace(payload.MessageV2)
			}
		}
		responseErr := fmt.Errorf("provider returned HTTP %d", statusCode)
		if providerMessage != "" {
			responseErr = fmt.Errorf("provider returned HTTP %d: %s", statusCode, providerMessage)
		}
		return "", &liandongCreateError{
			definitive: statusCode == http.StatusBadRequest ||
				statusCode == http.StatusUnauthorized ||
				statusCode == http.StatusForbidden ||
				statusCode == http.StatusNotFound ||
				statusCode == http.StatusUnprocessableEntity,
			err: responseErr,
		}
	}
	tradeNo, err := parseLiandongCreateTradeNoForBaseURL(responseBody, c.baseURL)
	if err != nil {
		var rejection *liandongProviderRejection
		return "", &liandongCreateError{
			definitive: errors.As(err, &rejection),
			err:        err,
		}
	}
	return tradeNo, nil
}

func (c *liandongClient) queryOrderWithSettings(
	ctx context.Context,
	settingsSnapshot setting.LiandongPaymentSettings,
	order *model.LiandongOrder,
) (*liandongVerification, error) {
	if order == nil || order.ProviderTradeNo == nil {
		return nil, errors.New("provider trade number is missing")
	}
	payload := struct {
		Current  int    `json:"current"`
		PageSize int    `json:"pageSize"`
		Status   int    `json:"status"`
		TradeNo  string `json:"trade_no"`
	}{
		Current:  1,
		PageSize: 1,
		Status:   999,
		TradeNo:  *order.ProviderTradeNo,
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	statusCode, responseBody, tokenUsed, err := c.doAuthenticatedJSON(
		ctx,
		liandongOrderListPath,
		body,
		settingsSnapshot,
	)
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, &liandongQueryError{
			statusCode: statusCode,
			systemic:   statusCode == http.StatusForbidden || statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError,
			err:        fmt.Errorf("provider returned HTTP %d", statusCode),
		}
	}
	verification, err := parseLiandongOrderVerification(responseBody, *order.ProviderTradeNo)
	if err != nil {
		return nil, &liandongQueryError{systemic: true, err: err}
	}
	_ = tokenUsed
	return verification, nil
}

func (c *liandongClient) queryOrderBatch(
	ctx context.Context,
	settingsSnapshot setting.LiandongPaymentSettings,
) ([]liandongOrderRecord, string, error) {
	payload := struct {
		Current  int `json:"current"`
		PageSize int `json:"pageSize"`
		Status   int `json:"status"`
	}{
		Current:  1,
		PageSize: settingsSnapshot.ReconcileBatchSize,
		Status:   999,
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	statusCode, responseBody, tokenUsed, err := c.doAuthenticatedJSON(
		ctx,
		liandongOrderListPath,
		body,
		settingsSnapshot,
	)
	if err != nil {
		return nil, tokenUsed, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, tokenUsed, &liandongQueryError{
			statusCode: statusCode,
			systemic:   statusCode == http.StatusForbidden || statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError,
			err:        fmt.Errorf("provider returned HTTP %d", statusCode),
		}
	}
	records, err := parseLiandongOrderRecords(responseBody)
	if err != nil {
		return nil, tokenUsed, &liandongQueryError{systemic: true, err: err}
	}
	return records, tokenUsed, nil
}

func (c *liandongClient) doAuthenticatedJSON(
	ctx context.Context,
	path string,
	body []byte,
	settingsSnapshot setting.LiandongPaymentSettings,
) (int, []byte, string, error) {
	token := strings.TrimSpace(settingsSnapshot.MerchantToken)
	if token == "" && settingsSnapshot.AuthMode == setting.LiandongAuthModeCredentials {
		refreshed, err := c.refreshMerchantToken(ctx, "")
		if err != nil {
			return 0, nil, "", err
		}
		token = refreshed
	}
	if token == "" {
		return 0, nil, "", errors.New("liandong merchant token is not configured")
	}
	statusCode, responseBody, err := c.doJSON(ctx, http.MethodPost, path, body, token)
	if err != nil {
		return 0, nil, token, &liandongQueryError{systemic: true, err: err}
	}
	if !liandongUnauthorizedResponse(statusCode, responseBody) {
		return statusCode, responseBody, token, nil
	}
	if settingsSnapshot.AuthMode != setting.LiandongAuthModeCredentials {
		return statusCode, responseBody, token, &liandongQueryError{
			statusCode: http.StatusUnauthorized,
			systemic:   true,
			err:        errors.New("liandong authentication failed"),
		}
	}
	refreshed, err := c.refreshMerchantToken(ctx, token)
	if err != nil {
		return statusCode, responseBody, token, err
	}
	retryStatus, retryBody, retryErr := c.doJSON(ctx, http.MethodPost, path, body, refreshed)
	if retryErr != nil {
		return 0, nil, refreshed, &liandongQueryError{systemic: true, err: retryErr}
	}
	if liandongUnauthorizedResponse(retryStatus, retryBody) {
		return retryStatus, retryBody, refreshed, &liandongQueryError{
			statusCode: http.StatusUnauthorized,
			systemic:   true,
			err:        errors.New("liandong authentication failed after token refresh"),
		}
	}
	return retryStatus, retryBody, refreshed, nil
}

func (c *liandongClient) refreshMerchantToken(ctx context.Context, staleToken string) (string, error) {
	liandongAuthRefreshMu.Lock()
	defer liandongAuthRefreshMu.Unlock()

	latest, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return "", err
	}
	currentToken := strings.TrimSpace(latest.MerchantToken)
	if currentToken != "" && currentToken != strings.TrimSpace(staleToken) {
		return currentToken, nil
	}
	if latest.AuthMode != setting.LiandongAuthModeCredentials ||
		strings.TrimSpace(latest.Username) == "" ||
		latest.Password == "" {
		return "", errors.New("liandong credentials are not configured")
	}
	payload := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{
		Username: latest.Username,
		Password: latest.Password,
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return "", err
	}
	statusCode, responseBody, err := c.doJSON(ctx, http.MethodPost, liandongLoginPath, body, "")
	if err != nil {
		return "", &liandongQueryError{systemic: true, err: err}
	}
	if statusCode < 200 || statusCode >= 300 {
		return "", &liandongQueryError{
			statusCode: statusCode,
			systemic:   true,
			err:        fmt.Errorf("liandong login returned HTTP %d", statusCode),
		}
	}
	token, err := parseLiandongLoginToken(responseBody)
	if err != nil {
		return "", &liandongQueryError{systemic: true, err: err}
	}
	if err := model.UpdateOptionsBulk(map[string]string{"LiandongMerchantToken": token}); err != nil {
		return "", err
	}
	return token, nil
}

func (c *liandongClient) doJSON(
	ctx context.Context,
	method string,
	path string,
	body []byte,
	merchantToken string,
) (int, []byte, error) {
	if c.configErr != nil {
		return 0, nil, c.configErr
	}
	if path != liandongCreatePath &&
		path != liandongOrderListPath &&
		path != liandongGoodsListPath &&
		path != liandongLoginPath {
		return 0, nil, errors.New("provider path is not allowed")
	}
	endpoint, err := liandongEndpointURL(c.baseURL, path)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if path == liandongOrderListPath || path == liandongGoodsListPath {
		if merchantToken == "" {
			return 0, nil, errors.New("merchant token is missing")
		}
		req.Header.Set("merchant-token", merchantToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, liandongMaxBodyBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(responseBody) > liandongMaxBodyBytes {
		return resp.StatusCode, nil, errors.New("provider response is too large")
	}
	return resp.StatusCode, normalizeLiandongJSONBody(responseBody), nil
}

func normalizeLiandongJSONBody(body []byte) []byte {
	normalized := bytes.TrimSpace(body)
	normalized = bytes.TrimPrefix(normalized, []byte{0xef, 0xbb, 0xbf})
	return bytes.TrimSpace(normalized)
}

func invalidLiandongJSONResponse(kind string, body []byte) error {
	normalized := normalizeLiandongJSONBody(body)
	format := "malformed JSON"
	switch {
	case len(normalized) == 0:
		format = "empty body"
	case isLiandongBrowserVerificationPage(normalized):
		format = "received an upstream browser verification page instead of JSON"
	case normalized[0] == '<':
		format = "received HTML instead of JSON"
	case len(normalized) >= 2 && normalized[0] == 0x1f && normalized[1] == 0x8b:
		format = "received compressed bytes instead of decoded JSON"
	case len(normalized) >= 2 &&
		((normalized[0] == 0xff && normalized[1] == 0xfe) ||
			(normalized[0] == 0xfe && normalized[1] == 0xff)):
		format = "received unsupported UTF-16 JSON"
	case normalized[0] != '{' && normalized[0] != '[':
		format = "unexpected response format"
	}
	return fmt.Errorf(
		"provider %s response is invalid (%s; %d bytes)",
		kind,
		format,
		len(body),
	)
}

func isLiandongBrowserVerificationPage(body []byte) bool {
	normalized := bytes.ToLower(normalizeLiandongJSONBody(body))
	return bytes.Contains(normalized, []byte("<script")) &&
		bytes.Contains(normalized, []byte("var arg1="))
}

func parseLiandongCreateTradeNo(body []byte) (string, error) {
	return parseLiandongCreateTradeNoForBaseURL(body, setting.DefaultLiandongBaseURL)
}

func parseLiandongCreateTradeNoForBaseURL(
	body []byte,
	baseURL string,
) (string, error) {
	var payload liandongCreateResponse
	body = normalizeLiandongJSONBody(body)
	if err := common.Unmarshal(body, &payload); err != nil {
		return "", invalidLiandongJSONResponse("create", body)
	}
	tradeNo := strings.TrimSpace(payload.TradeNo)
	if tradeNo == "" {
		tradeNo = strings.TrimSpace(payload.TradeNoV2)
	}
	payURL := strings.TrimSpace(payload.PayURL)
	if payURL == "" {
		payURL = strings.TrimSpace(payload.PayURLV2)
	}
	if payURL == "" && len(payload.Data) > 0 && string(payload.Data) != "null" {
		var data liandongCreateResponseData
		if err := common.Unmarshal(payload.Data, &data); err == nil {
			payURL = strings.TrimSpace(data.PayURL)
			if payURL == "" {
				payURL = strings.TrimSpace(data.PayURLV2)
			}
			if tradeNo == "" {
				tradeNo = strings.TrimSpace(data.TradeNo)
			}
			if tradeNo == "" {
				tradeNo = strings.TrimSpace(data.TradeNoV2)
			}
		}
		if payURL == "" && tradeNo == "" {
			var rawURL string
			if err := common.Unmarshal(payload.Data, &rawURL); err == nil {
				rawValue := strings.TrimSpace(rawURL)
				if liandongTradeNoPattern.MatchString(rawValue) {
					tradeNo = rawValue
				} else {
					payURL = rawValue
				}
			}
		}
	}
	if tradeNo != "" {
		if !liandongTradeNoPattern.MatchString(tradeNo) {
			return "", errors.New("provider trade number is invalid")
		}
		return tradeNo, nil
	}
	if payURL == "" {
		codeIndicatesRejection := false
		if len(payload.Code) > 0 && string(payload.Code) != "null" {
			var numericCode int
			if err := common.Unmarshal(payload.Code, &numericCode); err == nil {
				codeIndicatesRejection = numericCode == 0
			} else {
				var textCode string
				if err := common.Unmarshal(payload.Code, &textCode); err == nil {
					codeIndicatesRejection = strings.TrimSpace(textCode) == "0"
				}
			}
		}
		if codeIndicatesRejection {
			message := strings.TrimSpace(payload.Message)
			if message == "" {
				message = strings.TrimSpace(payload.MessageV2)
			}
			return "", &liandongProviderRejection{message: message}
		}
		return "", errors.New("provider create response has no payment URL")
	}

	parsed, err := url.Parse(payURL)
	if err != nil {
		return "", errors.New("provider payment URL is invalid")
	}
	configuredPaymentURL, err := liandongEndpointURL(baseURL, liandongPaymentPath)
	if err != nil {
		return "", err
	}
	configuredPayment, err := url.Parse(configuredPaymentURL)
	if err != nil {
		return "", err
	}
	officialPayment, err := url.Parse(
		setting.DefaultLiandongBaseURL + liandongPaymentPath,
	)
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() {
		matchesConfiguredOrigin := strings.EqualFold(parsed.Scheme, configuredPayment.Scheme) &&
			strings.EqualFold(parsed.Host, configuredPayment.Host)
		matchesOfficialOrigin := strings.EqualFold(parsed.Scheme, officialPayment.Scheme) &&
			strings.EqualFold(parsed.Host, officialPayment.Host)
		if !matchesConfiguredOrigin && !matchesOfficialOrigin {
			return "", errors.New("provider payment URL host is invalid")
		}
	} else if parsed.Host != "" || parsed.Scheme != "" {
		return "", errors.New("provider payment URL host is invalid")
	}
	if parsed.Path != liandongPaymentPath &&
		parsed.Path != configuredPayment.Path {
		return "", errors.New("provider payment URL path is invalid")
	}
	tradeNos := parsed.Query()["trade_no"]
	if len(tradeNos) != 1 {
		return "", errors.New("provider payment URL has no unambiguous trade number")
	}
	tradeNo = strings.TrimSpace(tradeNos[0])
	if !liandongTradeNoPattern.MatchString(tradeNo) {
		return "", errors.New("provider trade number is invalid")
	}
	return tradeNo, nil
}

func parseLiandongOrderVerification(body []byte, expectedTradeNo string) (*liandongVerification, error) {
	var payload liandongOrderListResponse
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("provider order response is invalid")
	}
	codeIndicatesRejection := false
	if len(payload.Code) > 0 && string(payload.Code) != "null" {
		var numericCode int
		if err := common.Unmarshal(payload.Code, &numericCode); err == nil {
			codeIndicatesRejection = numericCode == 0
		} else {
			var textCode string
			if err := common.Unmarshal(payload.Code, &textCode); err == nil {
				codeIndicatesRejection = strings.TrimSpace(textCode) == "0"
			}
		}
	}
	if codeIndicatesRejection {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = strings.TrimSpace(payload.MessageV2)
		}
		return nil, &liandongProviderRejection{message: message}
	}
	records := firstLiandongOrderRecords(payload.List, payload.Records, payload.Items)
	if len(records) == 0 && len(payload.Data) > 0 && string(payload.Data) != "null" {
		var data liandongOrderListData
		if err := common.Unmarshal(payload.Data, &data); err == nil {
			records = firstLiandongOrderRecords(data.List, data.Records, data.Items)
		}
		if len(records) == 0 {
			var direct []liandongOrderRecord
			if err := common.Unmarshal(payload.Data, &direct); err == nil {
				records = direct
			}
		}
	}
	if len(records) == 0 {
		return &liandongVerification{}, nil
	}
	if len(records) != 1 {
		return &liandongVerification{ReviewRequired: true}, nil
	}

	record := records[0]
	tradeNo := strings.TrimSpace(record.TradeNo)
	if tradeNo == "" {
		tradeNo = strings.TrimSpace(record.TradeNoV2)
	}
	if tradeNo != expectedTradeNo {
		return &liandongVerification{ReviewRequired: true}, nil
	}
	status, err := parseLiandongOrderStatus(record.Status)
	if err != nil {
		return &liandongVerification{ReviewRequired: true}, nil
	}
	summaryJSON, err := common.Marshal(map[string]any{
		"trade_no": tradeNo,
		"status":   status,
	})
	if err != nil {
		return nil, err
	}
	return &liandongVerification{
		Paid:             status == 1,
		SanitizedSummary: string(summaryJSON),
	}, nil
}

func parseLiandongOrderRecords(body []byte) ([]liandongOrderRecord, error) {
	var payload liandongOrderListResponse
	body = normalizeLiandongJSONBody(body)
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, invalidLiandongJSONResponse("order", body)
	}
	if liandongRawCodeEquals(payload.Code, 0) {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = strings.TrimSpace(payload.MessageV2)
		}
		return nil, &liandongProviderRejection{message: message}
	}
	records, present := firstPresentLiandongOrderRecords(
		payload.List,
		payload.Records,
		payload.Items,
	)
	if present {
		return records, nil
	}
	if len(payload.Data) == 0 || string(payload.Data) == "null" {
		return nil, nil
	}
	var data liandongOrderListData
	if err := common.Unmarshal(payload.Data, &data); err == nil {
		records, present = firstPresentLiandongOrderRecords(
			data.List,
			data.Records,
			data.Items,
		)
		if present {
			return records, nil
		}
	}
	var direct []liandongOrderRecord
	if err := common.Unmarshal(payload.Data, &direct); err == nil {
		return direct, nil
	}
	return nil, errors.New("provider order list response shape is unsupported")
}

func liandongRawCodeEquals(raw json.RawMessage, expected int) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var numeric int
	if err := common.Unmarshal(raw, &numeric); err == nil {
		return numeric == expected
	}
	var text string
	if err := common.Unmarshal(raw, &text); err != nil {
		return false
	}
	numeric, err := strconv.Atoi(strings.TrimSpace(text))
	return err == nil && numeric == expected
}

func liandongUnauthorizedResponse(statusCode int, body []byte) bool {
	if statusCode == http.StatusUnauthorized {
		return true
	}
	var payload struct {
		Code json.RawMessage `json:"code"`
	}
	if err := common.Unmarshal(body, &payload); err != nil {
		return false
	}
	return liandongRawCodeEquals(payload.Code, http.StatusUnauthorized)
}

func parseLiandongLoginToken(body []byte) (string, error) {
	var payload liandongLoginResponse
	body = normalizeLiandongJSONBody(body)
	if err := common.Unmarshal(body, &payload); err != nil {
		return "", invalidLiandongJSONResponse("login", body)
	}
	if liandongRawCodeEquals(payload.Code, http.StatusUnauthorized) {
		return "", errors.New("liandong login was rejected")
	}
	token := strings.TrimSpace(payload.Token)
	if token == "" {
		token = strings.TrimSpace(payload.MerchantToken)
	}
	if token == "" {
		token = strings.TrimSpace(payload.MerchantDash)
	}
	if token == "" && len(payload.Data) > 0 && string(payload.Data) != "null" {
		var data liandongLoginResponseData
		if err := common.Unmarshal(payload.Data, &data); err == nil {
			token = strings.TrimSpace(data.Token)
			if token == "" {
				token = strings.TrimSpace(data.MerchantToken)
			}
			if token == "" {
				token = strings.TrimSpace(data.MerchantDash)
			}
		}
	}
	if token != "" && len(token) <= 512 {
		return token, nil
	}
	if liandongRawCodeEquals(payload.Code, 0) {
		return "", errors.New("liandong login was rejected")
	}
	return "", errors.New("liandong login response has no valid merchant token")
}

func ListLiandongProviderGoods(
	ctx context.Context,
	goodsType string,
	name string,
) ([]LiandongProviderGoods, error) {
	goodsType = strings.TrimSpace(goodsType)
	switch goodsType {
	case "", "article", "card", "resource", "equity":
	default:
		return nil, errors.New("invalid liandong goods type")
	}
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return nil, err
	}
	payload := struct {
		Current   int    `json:"current"`
		PageSize  int    `json:"pageSize"`
		GoodsType string `json:"goods_type"`
		Status    int    `json:"status"`
		Name      string `json:"name"`
		IsProxy   string `json:"is_proxy"`
	}{
		Current:   1,
		PageSize:  500,
		GoodsType: goodsType,
		Status:    1,
		Name:      strings.TrimSpace(name),
		IsProxy:   "0",
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	client := newLiandongClient()
	statusCode, responseBody, tokenUsed, err := client.doAuthenticatedJSON(
		ctx,
		liandongGoodsListPath,
		body,
		settingsSnapshot,
	)
	if err != nil {
		return nil, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf(
			"provider returned HTTP %d: %s",
			statusCode,
			liandongProviderResponseDiagnostic(
				responseBody,
				settingsSnapshot.JUUID,
				settingsSnapshot.Username,
				settingsSnapshot.Password,
				settingsSnapshot.MerchantToken,
				tokenUsed,
				settingsSnapshot.ProxyUsername,
				settingsSnapshot.ProxyPassword,
			),
		)
	}
	goods, err := parseLiandongGoods(responseBody)
	if err != nil {
		return nil, fmt.Errorf(
			"%w; upstream HTTP %d response: %s",
			err,
			statusCode,
			liandongProviderResponseDiagnostic(
				responseBody,
				settingsSnapshot.JUUID,
				settingsSnapshot.Username,
				settingsSnapshot.Password,
				settingsSnapshot.MerchantToken,
				tokenUsed,
				settingsSnapshot.ProxyUsername,
				settingsSnapshot.ProxyPassword,
			),
		)
	}
	return goods, nil
}

func parseLiandongGoods(body []byte) ([]LiandongProviderGoods, error) {
	var payload liandongGoodsListResponse
	body = normalizeLiandongJSONBody(body)
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, invalidLiandongJSONResponse("goods", body)
	}
	if liandongRawCodeEquals(payload.Code, 0) {
		return nil, errors.New("provider rejected goods query")
	}
	records := firstLiandongGoodsRecords(payload.List, payload.Records, payload.Items)
	if len(records) == 0 && len(payload.Data) > 0 && string(payload.Data) != "null" {
		var data liandongGoodsListData
		if err := common.Unmarshal(payload.Data, &data); err == nil {
			records = firstLiandongGoodsRecords(data.List, data.Records, data.Items)
		}
		if len(records) == 0 {
			var direct []liandongGoodsRecord
			if err := common.Unmarshal(payload.Data, &direct); err == nil {
				records = direct
			}
		}
	}
	goods := make([]LiandongProviderGoods, 0, len(records))
	for _, record := range records {
		goodsKey := strings.TrimSpace(record.GoodsKey)
		if goodsKey == "" {
			goodsKey = strings.TrimSpace(record.GoodsKey2)
		}
		productName := strings.TrimSpace(record.Name)
		if productName == "" {
			productName = strings.TrimSpace(record.GoodsName)
		}
		if goodsKey == "" || productName == "" {
			continue
		}
		goods = append(goods, LiandongProviderGoods{
			GoodsKey:  goodsKey,
			Name:      productName,
			GoodsType: strings.TrimSpace(record.GoodsType),
		})
	}
	if len(records) > 0 && len(goods) == 0 {
		return nil, errors.New("provider goods response has no usable records")
	}
	return goods, nil
}

func firstLiandongGoodsRecords(groups ...[]liandongGoodsRecord) []liandongGoodsRecord {
	for _, records := range groups {
		if len(records) > 0 {
			return records
		}
	}
	return nil
}

func firstLiandongOrderRecords(groups ...[]liandongOrderRecord) []liandongOrderRecord {
	for _, records := range groups {
		if len(records) > 0 {
			return records
		}
	}
	return nil
}

func firstPresentLiandongOrderRecords(
	groups ...[]liandongOrderRecord,
) ([]liandongOrderRecord, bool) {
	for _, records := range groups {
		if records != nil {
			return records, true
		}
	}
	return nil, false
}

func parseLiandongOrderStatus(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, errors.New("provider order status is missing")
	}
	var numeric int
	if err := common.Unmarshal(raw, &numeric); err == nil {
		return numeric, nil
	}
	var text string
	if err := common.Unmarshal(raw, &text); err != nil {
		return 0, errors.New("provider order status is invalid")
	}
	status, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, errors.New("provider order status is invalid")
	}
	return status, nil
}

func LiandongPaymentURL(providerTradeNo string) string {
	return liandongPaymentURL(setting.DefaultLiandongBaseURL, providerTradeNo)
}

func liandongPaymentURL(baseURL string, providerTradeNo string) string {
	if !liandongTradeNoPattern.MatchString(providerTradeNo) {
		return ""
	}
	normalizedBaseURL, err := setting.NormalizeLiandongBaseURL(baseURL)
	if err != nil {
		return ""
	}
	endpoint, err := liandongEndpointURL(normalizedBaseURL, liandongPaymentPath)
	if err != nil {
		return ""
	}
	return endpoint + "?trade_no=" + url.QueryEscape(providerTradeNo)
}

func liandongEndpointURL(baseURL string, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", errors.New("card marketplace base URL is invalid")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawPath = ""
	return parsed.String(), nil
}

func sanitizeLiandongDiagnostic(message string, secrets ...string) string {
	redactions := make([]string, 0, len(secrets)*2)
	seen := make(map[string]struct{}, len(secrets)*2)
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		for _, candidate := range []string{secret, url.QueryEscape(secret)} {
			if _, exists := seen[candidate]; exists {
				continue
			}
			seen[candidate] = struct{}{}
			redactions = append(redactions, candidate)
		}
	}
	sort.Slice(redactions, func(i int, j int) bool {
		return len(redactions[i]) > len(redactions[j])
	})

	sanitized := message
	for _, secret := range redactions {
		sanitized = strings.ReplaceAll(sanitized, secret, "[redacted]")
	}
	sanitized = liandongSensitiveDiagnosticValuePattern.ReplaceAllString(
		sanitized,
		"${1}[redacted]",
	)
	sanitized = strings.Map(func(char rune) rune {
		if char < ' ' || char == '\u007f' {
			return ' '
		}
		return char
	}, sanitized)
	sanitized = strings.Join(strings.Fields(sanitized), " ")
	messageRunes := []rune(sanitized)
	if len(messageRunes) > liandongMaxDiagnosticRunes {
		sanitized = string(messageRunes[:liandongMaxDiagnosticRunes])
	}
	return sanitized
}

func liandongProviderResponseDiagnostic(body []byte, secrets ...string) string {
	if isLiandongBrowserVerificationPage(body) {
		return "<browser verification page omitted>"
	}
	diagnostic := sanitizeLiandongDiagnostic(
		string(normalizeLiandongJSONBody(body)),
		secrets...,
	)
	if diagnostic == "" {
		return "<empty>"
	}
	return diagnostic
}

func SanitizeLiandongOrderDiagnostic(
	order *model.LiandongOrder,
	settingsSnapshot setting.LiandongPaymentSettings,
) string {
	if order == nil || strings.TrimSpace(order.LastError) == "" {
		return ""
	}
	return sanitizeLiandongDiagnostic(
		order.LastError,
		order.JUUIDSnapshot,
		order.GoodsKeySnapshot,
		order.ContactSnapshot,
		settingsSnapshot.JUUID,
		settingsSnapshot.Username,
		settingsSnapshot.Password,
		settingsSnapshot.MerchantToken,
		settingsSnapshot.ProxyUsername,
		settingsSnapshot.ProxyPassword,
	)
}

func LiandongOrderView(order *model.LiandongOrder) LiandongPaymentView {
	view := LiandongPaymentView{}
	if order == nil {
		return view
	}
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	gatewayEnabled := err == nil && settingsSnapshot.Enabled
	view = LiandongPaymentView{
		LocalTradeNo:              order.LocalTradeNo,
		ProductName:               order.ProductNameSnapshot,
		BusinessType:              order.BusinessType,
		PaymentStatus:             order.PaymentStatus,
		FulfillmentStatus:         order.FulfillmentStatus,
		IframeAllowed:             gatewayEnabled && settingsSnapshot.IframeEnabled,
		CreatedAt:                 order.CreatedAt,
		PaidAt:                    order.PaidAt,
		FulfilledAt:               order.FulfilledAt,
		ExpiresAt:                 order.ExpiresAt,
		LatePayment:               order.LatePayment,
		ClientPollIntervalSeconds: setting.DefaultLiandongClientPollIntervalSeconds,
	}
	if err == nil {
		view.ClientPollIntervalSeconds = settingsSnapshot.ClientPollIntervalSeconds
	}
	if gatewayEnabled &&
		(order.PaymentStatus == model.LiandongPaymentStatusPending ||
			order.PaymentStatus == model.LiandongPaymentStatusCreateUnknown) &&
		order.ProviderTradeNo != nil {
		view.PaymentURL = liandongPaymentURL(
			settingsSnapshot.BaseURL,
			*order.ProviderTradeNo,
		)
	}
	return view
}

func liandongAuthenticationConfigured(settingsSnapshot setting.LiandongPaymentSettings) bool {
	switch settingsSnapshot.AuthMode {
	case setting.LiandongAuthModeCredentials:
		return strings.TrimSpace(settingsSnapshot.Username) != "" &&
			settingsSnapshot.Password != ""
	case setting.LiandongAuthModeManualToken:
		return strings.TrimSpace(settingsSnapshot.MerchantToken) != ""
	default:
		return false
	}
}

func CreateLiandongPayment(
	ctx context.Context,
	userID int,
	productID int,
) (*LiandongPaymentView, error) {
	return createLiandongPayment(ctx, userID, productID, newLiandongClient())
}

func acquireLiandongUserOperationLease(ctx context.Context, userID int) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(liandongOperationWait)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		token, acquired, err := model.TryAcquireLiandongUserOperationLease(
			userID,
			int64(liandongOperationLeaseTTL/time.Second),
		)
		if err != nil {
			return "", err
		}
		if acquired {
			return token, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "", model.ErrLiandongOrderBusy
		}
		wait := liandongOperationRetry
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return "", ctx.Err()
		case <-timer.C:
		}
	}
}

func releaseLiandongUserOperationLease(userID int, token string) {
	if err := model.ReleaseLiandongUserOperationLease(userID, token); err != nil &&
		!errors.Is(err, model.ErrLiandongOrderBusy) {
		common.SysError(fmt.Sprintf(
			"failed to release liandong user operation lease for user %d: %v",
			userID,
			err,
		))
	}
}

func createLiandongPayment(
	ctx context.Context,
	userID int,
	productID int,
	client *liandongClient,
) (*LiandongPaymentView, error) {
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return nil, err
	}
	if !settingsSnapshot.Enabled || !settingsSnapshot.CreateEnabled {
		return nil, errors.New("Liandong gateway disabled")
	}
	if strings.TrimSpace(settingsSnapshot.JUUID) == "" {
		return nil, errors.New("Verification is not configured properly")
	}

	leaseToken, err := acquireLiandongUserOperationLease(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer releaseLiandongUserOperationLease(userID, leaseToken)

	settingsSnapshot, err = model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return nil, err
	}
	if !settingsSnapshot.Enabled || !settingsSnapshot.CreateEnabled {
		return nil, errors.New("Liandong gateway disabled")
	}
	if strings.TrimSpace(settingsSnapshot.JUUID) == "" {
		return nil, errors.New("Verification is not configured properly")
	}

	var order *model.LiandongOrder
	for attempt := 0; attempt < 3; attempt++ {
		contact, err := generateLiandongContact()
		if err != nil {
			return nil, errors.New("Failed to create payment order")
		}
		createResult, err := model.CreateLiandongOrderWithTimeout(
			userID,
			productID,
			contact,
			settingsSnapshot.JUUID,
			settingsSnapshot.PaymentTimeoutMinutes,
		)
		if !errors.Is(err, model.ErrLiandongContactConflict) {
			if err != nil {
				return nil, err
			}
			order = createResult.Order
			break
		}
	}
	if order == nil {
		return nil, errors.New("Failed to create payment order")
	}

	latestSettings, settingsErr := model.GetLiandongPaymentSettingsFromDB()
	if settingsErr != nil || !latestSettings.Enabled || !latestSettings.CreateEnabled {
		diagnostic := "liandong payment creation was disabled before provider request"
		if settingsErr != nil {
			diagnostic = "liandong payment settings became unavailable before provider request"
		}
		if markErr := model.MarkLiandongCreateFailure(
			order.LocalTradeNo,
			model.LiandongPaymentStatusCreateFailed,
			diagnostic,
		); markErr != nil {
			common.SysError(fmt.Sprintf(
				"failed to close disabled liandong create order %s: %v",
				order.LocalTradeNo,
				markErr,
			))
		}
		if settingsErr != nil {
			return nil, errors.New("Failed to create payment order")
		}
		return nil, errors.New("Liandong gateway disabled")
	}

	providerTradeNo, createErr := client.createOrder(
		ctx,
		order.GoodsKeySnapshot,
		order.ContactSnapshot,
		order.JUUIDSnapshot,
	)
	if createErr != nil {
		status := model.LiandongPaymentStatusCreateUnknown
		var typedErr *liandongCreateError
		if errors.As(createErr, &typedErr) && typedErr.definitive {
			status = model.LiandongPaymentStatusCreateFailed
		}
		diagnostic := sanitizeLiandongDiagnostic(
			createErr.Error(),
			order.GoodsKeySnapshot,
			order.ContactSnapshot,
			order.JUUIDSnapshot,
			settingsSnapshot.JUUID,
			settingsSnapshot.MerchantToken,
			settingsSnapshot.ProxyUsername,
			settingsSnapshot.ProxyPassword,
		)
		if diagnostic == "" {
			diagnostic = "liandong provider returned no diagnostic information"
		}
		if markErr := model.MarkLiandongCreateFailure(
			order.LocalTradeNo,
			status,
			diagnostic,
		); markErr != nil {
			common.SysError(fmt.Sprintf(
				"failed to persist liandong create failure for order %s: %v",
				order.LocalTradeNo,
				markErr,
			))
		}
		return nil, errors.New("Failed to create payment order")
	}
	if err := model.MarkLiandongCreateResult(
		order.LocalTradeNo,
		&providerTradeNo,
		model.LiandongPaymentStatusPending,
		"",
	); err != nil {
		if markErr := model.MarkLiandongCreatePersistenceFailure(
			order.LocalTradeNo,
			providerTradeNo,
			err.Error(),
		); markErr != nil {
			common.SysError(fmt.Sprintf(
				"failed to persist liandong create recovery state for order %s: %v",
				order.LocalTradeNo,
				markErr,
			))
		}
		return nil, errors.New("This payment order requires administrator review")
	}
	order.ProviderTradeNo = &providerTradeNo
	order.PaymentStatus = model.LiandongPaymentStatusPending
	order.NextCheckAt = 0
	WakeSystemTaskRunner()
	view := LiandongOrderView(order)
	return &view, nil
}

func closeLiandongOrderAfterVerification(
	ctx context.Context,
	client *liandongClient,
	order *model.LiandongOrder,
	userID int,
	reason string,
) (bool, error) {
	if order == nil || order.ProviderTradeNo == nil {
		return false, model.ErrLiandongOrderNotFound
	}
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return false, err
	}
	if !settingsSnapshot.Enabled || !settingsSnapshot.ReconcileEnabled {
		return false, errors.New("liandong payment verification is disabled")
	}
	if !liandongAuthenticationConfigured(settingsSnapshot) {
		return false, errors.New("liandong authentication is not configured")
	}
	claimed, err := model.ClaimLiandongUnsettledOrder(order.LocalTradeNo)
	if err != nil {
		if errors.Is(err, model.ErrLiandongOrderBusy) {
			current, reloadErr := model.GetLiandongOrder(order.LocalTradeNo)
			if reloadErr == nil && current.PaymentStatus == model.LiandongPaymentStatusPaid {
				return true, nil
			}
		}
		return false, err
	}
	verification, queryErr := client.queryOrderWithSettings(ctx, settingsSnapshot, claimed)
	if queryErr != nil {
		diagnostic := sanitizeLiandongDiagnostic(
			queryErr.Error(),
			claimed.JUUIDSnapshot,
			settingsSnapshot.JUUID,
			settingsSnapshot.Username,
			settingsSnapshot.Password,
			settingsSnapshot.MerchantToken,
			settingsSnapshot.ProxyUsername,
			settingsSnapshot.ProxyPassword,
		)
		if diagnostic == "" {
			diagnostic = "liandong provider returned no diagnostic information"
		}
		markErr := model.FailLiandongOrderCheck(
			claimed.LocalTradeNo,
			claimed.CheckLockUntil,
			claimed.ConsecutiveErrorCount+1,
			diagnostic,
		)
		if markErr != nil && !errors.Is(markErr, model.ErrLiandongOrderBusy) {
			return false, errors.Join(errors.New(diagnostic), markErr)
		}
		return false, errors.New(diagnostic)
	}
	latestSettings, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		return false, err
	}
	if !latestSettings.Enabled || !latestSettings.ReconcileEnabled {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		return false, errors.New("liandong payment verification was disabled")
	}
	if verification == nil {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		return false, errors.New("liandong provider returned no verification result")
	}
	if verification.ReviewRequired {
		if err := model.MarkLiandongOrderReviewRequired(
			claimed.LocalTradeNo,
			claimed.CheckLockUntil,
			verification.SanitizedSummary,
			"provider order identity is ambiguous or invalid",
		); err != nil && !errors.Is(err, model.ErrLiandongOrderBusy) {
			return false, err
		}
		return false, model.ErrLiandongOrderReviewRequired
	}
	if verification.Paid {
		transition, err := model.ApplyClaimedLiandongPaidTradeNo(
			*claimed.ProviderTradeNo,
			claimed.CheckLockUntil,
			verification.SanitizedSummary,
		)
		if err != nil {
			return false, err
		}
		if transition.Late {
			return true, model.ErrLiandongOrderReviewRequired
		}
		if err := maybeFulfillLiandongPaidTransition(transition); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := model.CloseClaimedLiandongOrder(
		claimed.LocalTradeNo,
		userID,
		claimed.CheckLockUntil,
		verification.SanitizedSummary,
		reason,
	); err != nil {
		return false, err
	}
	return false, nil
}

func maybeFulfillLiandongPaidTransition(transition *model.LiandongPaidTransition) error {
	if transition == nil || transition.Order == nil || transition.Late {
		return nil
	}
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return err
	}
	if !settingsSnapshot.Enabled || !settingsSnapshot.FulfillEnabled {
		return nil
	}
	_, err = fulfillLiandongOrder(transition.Order, settingsSnapshot.PollIntervalSeconds)
	return err
}

func RefreshLiandongPaymentForUser(
	ctx context.Context,
	userID int,
	localTradeNo string,
) (*LiandongPaymentView, error) {
	order, err := model.GetLiandongOrderForUser(userID, localTradeNo)
	if err != nil {
		return nil, err
	}
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return nil, err
	}
	if (order.PaymentStatus != model.LiandongPaymentStatusPending &&
		order.PaymentStatus != model.LiandongPaymentStatusCreateUnknown) ||
		order.ProviderTradeNo == nil ||
		!settingsSnapshot.Enabled ||
		!settingsSnapshot.ReconcileEnabled ||
		!liandongAuthenticationConfigured(settingsSnapshot) {
		view := LiandongOrderView(order)
		return &view, nil
	}
	claimed, err := model.ClaimLiandongPendingOrderAfter(
		order.LocalTradeNo,
		settingsSnapshot.ClientPollIntervalSeconds,
	)
	if err != nil {
		if errors.Is(err, model.ErrLiandongOrderBusy) {
			current, reloadErr := model.GetLiandongOrderForUser(userID, localTradeNo)
			if reloadErr != nil {
				return nil, reloadErr
			}
			view := LiandongOrderView(current)
			return &view, nil
		}
		return nil, err
	}
	client := newLiandongClient()
	verification, queryErr := client.queryOrderWithSettings(ctx, settingsSnapshot, claimed)
	if queryErr != nil {
		diagnostic := sanitizeLiandongDiagnostic(
			queryErr.Error(),
			claimed.JUUIDSnapshot,
			settingsSnapshot.JUUID,
			settingsSnapshot.Username,
			settingsSnapshot.Password,
			settingsSnapshot.MerchantToken,
			settingsSnapshot.ProxyUsername,
			settingsSnapshot.ProxyPassword,
		)
		if diagnostic == "" {
			diagnostic = "liandong provider returned no diagnostic information"
		}
		_ = model.FailLiandongOrderCheck(
			claimed.LocalTradeNo,
			claimed.CheckLockUntil,
			claimed.ConsecutiveErrorCount+1,
			diagnostic,
		)
		common.SysError("liandong client verification failed: " + diagnostic)
		current, reloadErr := model.GetLiandongOrderForUser(userID, localTradeNo)
		if reloadErr != nil {
			return nil, reloadErr
		}
		view := LiandongOrderView(current)
		return &view, nil
	}
	latestSettings, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		return nil, err
	}
	if !latestSettings.Enabled || !latestSettings.ReconcileEnabled {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		current, reloadErr := model.GetLiandongOrderForUser(userID, localTradeNo)
		if reloadErr != nil {
			return nil, reloadErr
		}
		view := LiandongOrderView(current)
		return &view, nil
	}
	if verification == nil {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		return nil, errors.New("liandong provider returned no verification result")
	}
	if verification.ReviewRequired {
		_ = model.MarkLiandongOrderReviewRequired(
			claimed.LocalTradeNo,
			claimed.CheckLockUntil,
			verification.SanitizedSummary,
			"provider order identity is ambiguous or invalid",
		)
	} else if verification.Paid {
		transition, applyErr := model.ApplyClaimedLiandongPaidTradeNo(
			*claimed.ProviderTradeNo,
			claimed.CheckLockUntil,
			verification.SanitizedSummary,
		)
		if applyErr != nil && !errors.Is(applyErr, model.ErrLiandongOrderBusy) {
			return nil, applyErr
		}
		if applyErr == nil {
			_ = maybeFulfillLiandongPaidTransition(transition)
		}
	} else {
		_ = model.CompleteLiandongOrderCheck(
			claimed.LocalTradeNo,
			claimed.CheckLockUntil,
			"",
			verification.SanitizedSummary,
		)
	}
	current, err := model.GetLiandongOrderForUser(userID, localTradeNo)
	if err != nil {
		return nil, err
	}
	view := LiandongOrderView(current)
	return &view, nil
}

func CloseLiandongPaymentForUser(
	ctx context.Context,
	userID int,
	localTradeNo string,
) (*LiandongPaymentView, error) {
	leaseToken, err := acquireLiandongUserOperationLease(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer releaseLiandongUserOperationLease(userID, leaseToken)

	order, err := model.GetLiandongOrderForUser(userID, localTradeNo)
	if err != nil {
		return nil, err
	}
	switch order.PaymentStatus {
	case model.LiandongPaymentStatusCreating:
		return nil, model.ErrLiandongOrderBusy
	case model.LiandongPaymentStatusPending, model.LiandongPaymentStatusCreateUnknown:
		if order.ProviderTradeNo == nil {
			return nil, model.ErrLiandongOrderBusy
		}
		if _, err := closeLiandongOrderAfterVerification(
			ctx,
			newLiandongClient(),
			order,
			userID,
			"closed by user",
		); err != nil && !errors.Is(err, model.ErrLiandongOrderReviewRequired) {
			return nil, err
		}
	}
	current, err := model.GetLiandongOrderForUser(userID, localTradeNo)
	if err != nil {
		return nil, err
	}
	view := LiandongOrderView(current)
	return &view, nil
}

func CloseLiandongPaymentForRoot(
	ctx context.Context,
	localTradeNo string,
) (*LiandongPaymentView, error) {
	order, err := model.GetLiandongOrder(localTradeNo)
	if err != nil {
		return nil, err
	}
	leaseToken, err := acquireLiandongUserOperationLease(ctx, order.UserID)
	if err != nil {
		return nil, err
	}
	defer releaseLiandongUserOperationLease(order.UserID, leaseToken)

	order, err = model.GetLiandongOrder(localTradeNo)
	if err != nil {
		return nil, err
	}
	if order.PaymentStatus == model.LiandongPaymentStatusCreating {
		return nil, model.ErrLiandongOrderBusy
	}
	if order.ProviderTradeNo == nil {
		if err := model.CloseLiandongOrder(localTradeNo); err != nil {
			return nil, err
		}
		closed, err := model.GetLiandongOrder(localTradeNo)
		if err != nil {
			return nil, err
		}
		view := LiandongOrderView(closed)
		return &view, nil
	}

	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return nil, err
	}
	if !settingsSnapshot.Enabled || !settingsSnapshot.ReconcileEnabled {
		return nil, errors.New("liandong payment verification is disabled")
	}
	if !liandongAuthenticationConfigured(settingsSnapshot) {
		return nil, errors.New("liandong authentication is not configured")
	}
	claimed, err := model.ClaimLiandongClosableOrder(localTradeNo)
	if err != nil {
		return nil, err
	}
	verification, queryErr := newLiandongClient().queryOrderWithSettings(
		ctx,
		settingsSnapshot,
		claimed,
	)
	if queryErr != nil {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		diagnostic := sanitizeLiandongDiagnostic(
			queryErr.Error(),
			claimed.JUUIDSnapshot,
			settingsSnapshot.JUUID,
			settingsSnapshot.Username,
			settingsSnapshot.Password,
			settingsSnapshot.MerchantToken,
			settingsSnapshot.ProxyUsername,
			settingsSnapshot.ProxyPassword,
		)
		if diagnostic == "" {
			diagnostic = "liandong payment verification failed"
		}
		return nil, errors.New(diagnostic)
	}
	latestSettings, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		return nil, err
	}
	if !latestSettings.Enabled || !latestSettings.ReconcileEnabled {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		return nil, errors.New("liandong payment verification was disabled")
	}
	if verification == nil {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		return nil, errors.New("liandong provider returned no verification result")
	}
	if verification.ReviewRequired {
		if claimed.PaymentStatus == model.LiandongPaymentStatusPending ||
			claimed.PaymentStatus == model.LiandongPaymentStatusCreateUnknown {
			_ = model.MarkLiandongOrderReviewRequired(
				claimed.LocalTradeNo,
				claimed.CheckLockUntil,
				verification.SanitizedSummary,
				"provider order identity is ambiguous or invalid",
			)
		} else {
			_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		}
		return nil, model.ErrLiandongOrderReviewRequired
	}
	if verification.Paid {
		transition, err := model.ApplyClaimedLiandongPaidTradeNo(
			*claimed.ProviderTradeNo,
			claimed.CheckLockUntil,
			verification.SanitizedSummary,
		)
		if err != nil {
			return nil, err
		}
		if !transition.Late {
			_ = maybeFulfillLiandongPaidTransition(transition)
		}
		return nil, model.ErrLiandongOrderReviewRequired
	}
	if err := model.CloseClaimedLiandongOrder(
		claimed.LocalTradeNo,
		0,
		claimed.CheckLockUntil,
		verification.SanitizedSummary,
		"closed by root operator",
	); err != nil {
		return nil, err
	}
	closed, err := model.GetLiandongOrder(localTradeNo)
	if err != nil {
		return nil, err
	}
	view := LiandongOrderView(closed)
	return &view, nil
}

func ManualFulfillLiandongLatePayment(localTradeNo string) (*LiandongPaymentView, error) {
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return nil, err
	}
	if !settingsSnapshot.Enabled {
		return nil, errors.New("liandong payment gateway is disabled")
	}
	order, err := model.PrepareLiandongLatePaymentFulfillment(localTradeNo)
	if err != nil {
		return nil, err
	}
	latestSettings, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return nil, err
	}
	if !latestSettings.Enabled {
		return nil, errors.New("liandong payment gateway was disabled")
	}
	fulfilled, err := fulfillLiandongOrder(order, latestSettings.PollIntervalSeconds)
	if err != nil {
		return nil, err
	}
	if !fulfilled {
		return nil, errors.New("liandong late payment fulfillment failed")
	}
	updated, err := model.GetLiandongOrder(localTradeNo)
	if err != nil {
		return nil, err
	}
	view := LiandongOrderView(updated)
	return &view, nil
}

func RequeueLiandongOrder(localTradeNo string) error {
	return RequeueLiandongOrderContext(context.Background(), localTradeNo)
}

func RequeueLiandongOrderContext(ctx context.Context, localTradeNo string) error {
	order, err := model.GetLiandongOrder(localTradeNo)
	if err != nil {
		return err
	}
	leaseToken, err := acquireLiandongUserOperationLease(ctx, order.UserID)
	if err != nil {
		return err
	}
	defer releaseLiandongUserOperationLease(order.UserID, leaseToken)

	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return err
	}
	return model.RequeueLiandongOrderWithTimeout(
		localTradeNo,
		settingsSnapshot.PaymentTimeoutMinutes,
	)
}

func generateLiandongContact() (string, error) {
	first, err := rand.Int(rand.Reader, big.NewInt(9))
	if err != nil {
		return "", err
	}
	digits := make([]byte, 12)
	digits[0] = byte(first.Int64()+1) + '0'
	for index := 1; index < len(digits); index++ {
		value, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		digits[index] = byte(value.Int64()) + '0'
	}
	return string(digits), nil
}

func RunLiandongReconcileOnce(ctx context.Context) (map[string]int, error) {
	return runLiandongReconcileOnce(ctx, newLiandongClient())
}

func runLiandongReconcileOnce(ctx context.Context, client *liandongClient) (map[string]int, error) {
	result := map[string]int{"processed": 0, "paid": 0, "fulfilled": 0, "failed": 0}
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return result, err
	}
	if !settingsSnapshot.Enabled {
		return result, nil
	}

	var reconcileErr error
	if settingsSnapshot.ReconcileEnabled {
		switch {
		case !liandongAuthenticationConfigured(settingsSnapshot):
			reconcileErr = errors.New("liandong authentication is not configured")
		default:
			if err := closeExpiredLiandongOrdersWithoutProvider(ctx, result); err != nil {
				reconcileErr = err
			} else if err := reconcileStaleLiandongCreatingOrders(ctx, result); err != nil {
				reconcileErr = err
			} else if err := reconcilePendingLiandongOrders(ctx, client, result); err != nil {
				reconcileErr = err
			} else if err := reconcileExpiredLiandongOrders(ctx, client, result); err != nil {
				reconcileErr = err
			}
		}
	}

	var fulfillErr error
	if settingsSnapshot.FulfillEnabled {
		fulfillErr = fulfillDueLiandongOrders(ctx, result)
	}
	return result, errors.Join(reconcileErr, fulfillErr)
}

func closeExpiredLiandongOrdersWithoutProvider(ctx context.Context, result map[string]int) error {
	orders, err := model.FindExpiredLiandongOrdersWithoutProvider(liandongReconcileBatchSize)
	if err != nil {
		return err
	}
	for _, order := range orders {
		if err := ctx.Err(); err != nil {
			return err
		}
		settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
		if err != nil {
			return err
		}
		if !settingsSnapshot.Enabled || !settingsSnapshot.ReconcileEnabled {
			return nil
		}
		if err := model.CloseLiandongOrderForUser(
			order.UserID,
			order.LocalTradeNo,
			"payment timeout",
		); err != nil {
			if errors.Is(err, model.ErrLiandongOrderBusy) ||
				errors.Is(err, model.ErrLiandongOrderNotFound) {
				continue
			}
			return err
		}
		result["processed"]++
	}
	return nil
}

func reconcileStaleLiandongCreatingOrders(ctx context.Context, result map[string]int) error {
	orders, err := model.FindStaleCreatingLiandongOrders(liandongReconcileBatchSize)
	if err != nil {
		return err
	}
	for _, dueOrder := range orders {
		if err := ctx.Err(); err != nil {
			return err
		}
		settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
		if err != nil {
			return err
		}
		if !settingsSnapshot.Enabled || !settingsSnapshot.ReconcileEnabled {
			return nil
		}
		order, err := model.ClaimLiandongStaleCreatingOrder(dueOrder.LocalTradeNo)
		if err != nil {
			if !errors.Is(err, model.ErrLiandongOrderBusy) {
				result["failed"]++
			}
			continue
		}
		result["processed"]++
		if err := model.MarkLiandongStaleCreateReviewRequired(
			order.LocalTradeNo,
			order.CheckLockUntil,
			"order creation remained incomplete for more than five minutes",
		); err != nil && !errors.Is(err, model.ErrLiandongOrderBusy) {
			return err
		}
		result["failed"]++
	}
	return nil
}

func reconcilePendingLiandongOrders(
	ctx context.Context,
	client *liandongClient,
	result map[string]int,
) error {
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return err
	}
	if !settingsSnapshot.Enabled || !settingsSnapshot.ReconcileEnabled {
		return nil
	}
	records, tokenUsed, err := client.queryOrderBatch(ctx, settingsSnapshot)
	if err != nil {
		diagnostic := sanitizeLiandongDiagnostic(
			err.Error(),
			settingsSnapshot.JUUID,
			settingsSnapshot.Username,
			settingsSnapshot.Password,
			settingsSnapshot.MerchantToken,
			tokenUsed,
			settingsSnapshot.ProxyUsername,
			settingsSnapshot.ProxyPassword,
		)
		if diagnostic == "" {
			diagnostic = "liandong provider returned no diagnostic information"
		}
		result["failed"]++
		common.SysError("liandong reconciliation batch stopped: " + diagnostic)
		return errors.New(diagnostic)
	}
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		tradeNo := strings.TrimSpace(record.TradeNo)
		if tradeNo == "" {
			tradeNo = strings.TrimSpace(record.TradeNoV2)
		}
		if !liandongTradeNoPattern.MatchString(tradeNo) {
			continue
		}
		if _, exists := seen[tradeNo]; exists {
			continue
		}
		seen[tradeNo] = struct{}{}
		status, err := parseLiandongOrderStatus(record.Status)
		if err != nil || status != 1 {
			continue
		}
		summary, err := common.Marshal(map[string]any{
			"trade_no": tradeNo,
			"status":   status,
		})
		if err != nil {
			return err
		}
		claimed, err := model.ClaimLiandongOrderByProviderTradeNo(tradeNo)
		if errors.Is(err, model.ErrLiandongOrderNotFound) ||
			errors.Is(err, model.ErrLiandongOrderBusy) {
			continue
		}
		if err != nil {
			result["failed"]++
			return err
		}
		latestSettings, err := model.GetLiandongPaymentSettingsFromDB()
		if err != nil {
			_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
			return err
		}
		if !latestSettings.Enabled || !latestSettings.ReconcileEnabled {
			_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
			return nil
		}
		transition, err := model.ApplyClaimedLiandongPaidTradeNo(
			tradeNo,
			claimed.CheckLockUntil,
			string(summary),
		)
		if errors.Is(err, model.ErrLiandongOrderBusy) {
			continue
		}
		if err != nil {
			result["failed"]++
			return err
		}
		result["processed"]++
		if transition.Late {
			result["failed"]++
			continue
		}
		if !transition.NewlyPaid {
			continue
		}
		result["paid"]++
		fulfillmentSettings, err := model.GetLiandongPaymentSettingsFromDB()
		if err != nil {
			return err
		}
		if !fulfillmentSettings.Enabled {
			return nil
		}
		if !fulfillmentSettings.FulfillEnabled {
			continue
		}
		fulfilled, err := fulfillLiandongOrder(transition.Order, fulfillmentSettings.PollIntervalSeconds)
		if err != nil {
			result["failed"]++
			return err
		}
		if fulfilled {
			result["fulfilled"]++
		} else {
			result["failed"]++
		}
	}
	return nil
}

func reconcileExpiredLiandongOrders(
	ctx context.Context,
	client *liandongClient,
	result map[string]int,
) error {
	orders, err := model.FindExpiredLiandongOrders(liandongReconcileBatchSize)
	if err != nil {
		return err
	}
	for _, dueOrder := range orders {
		if err := ctx.Err(); err != nil {
			return err
		}
		settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
		if err != nil {
			return err
		}
		if !settingsSnapshot.Enabled {
			return nil
		}
		result["processed"]++
		if dueOrder.ProviderTradeNo == nil {
			continue
		}
		paid, err := closeLiandongOrderAfterVerification(
			ctx,
			client,
			&dueOrder,
			dueOrder.UserID,
			"payment timeout",
		)
		if err != nil {
			if errors.Is(err, model.ErrLiandongOrderBusy) ||
				errors.Is(err, model.ErrLiandongOrderNotFound) ||
				errors.Is(err, model.ErrLiandongOrderReviewRequired) {
				continue
			}
			result["failed"]++
			return err
		}
		if !paid {
			continue
		}
		result["paid"]++
		updated, reloadErr := model.GetLiandongOrder(dueOrder.LocalTradeNo)
		if reloadErr == nil &&
			updated.FulfillmentStatus == model.LiandongFulfillmentStatusFulfilled {
			result["fulfilled"]++
		}
	}
	return nil
}

func fulfillDueLiandongOrders(ctx context.Context, result map[string]int) error {
	dueOrders, err := model.FindDuePaidLiandongOrders(liandongReconcileBatchSize)
	if err != nil {
		return err
	}
	for _, dueOrder := range dueOrders {
		if err := ctx.Err(); err != nil {
			return err
		}
		settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
		if err != nil {
			return err
		}
		if !settingsSnapshot.Enabled || !settingsSnapshot.FulfillEnabled {
			return nil
		}
		order, err := model.ClaimLiandongPaidOrder(dueOrder.LocalTradeNo)
		if err != nil {
			if !errors.Is(err, model.ErrLiandongOrderBusy) {
				result["failed"]++
			}
			continue
		}
		result["processed"]++
		beforeFulfillmentSettings, err := model.GetLiandongPaymentSettingsFromDB()
		if err != nil {
			releaseErr := model.ReleaseLiandongOrderCheck(order.LocalTradeNo, order.CheckLockUntil)
			return errors.Join(err, releaseErr)
		}
		if !beforeFulfillmentSettings.Enabled || !beforeFulfillmentSettings.FulfillEnabled {
			if err := model.ReleaseLiandongOrderCheck(order.LocalTradeNo, order.CheckLockUntil); err != nil {
				return err
			}
			return nil
		}
		fulfilled, err := fulfillLiandongOrder(order, beforeFulfillmentSettings.PollIntervalSeconds)
		if err != nil {
			result["failed"]++
			return err
		}
		if fulfilled {
			result["fulfilled"]++
		} else {
			result["failed"]++
		}
	}
	return nil
}

func fulfillLiandongOrder(order *model.LiandongOrder, pollIntervalSeconds int) (bool, error) {
	if order == nil {
		return false, errors.New("liandong order is missing")
	}
	if _, err := model.FulfillLiandongOrder(order.LocalTradeNo); err != nil {
		consecutiveErrors := order.ConsecutiveErrorCount + 1
		nextCheckAt := common.GetTimestamp() +
			liandongErrorBackoffSeconds(pollIntervalSeconds, consecutiveErrors)
		markErr := model.MarkLiandongFulfillmentFailure(
			order.LocalTradeNo,
			consecutiveErrors,
			err.Error(),
			nextCheckAt,
		)
		if markErr != nil && !errors.Is(markErr, model.ErrLiandongOrderBusy) {
			return false, markErr
		}
		return false, nil
	}
	return true, nil
}

func RetryLiandongFulfillment(localTradeNo string) (*LiandongPaymentView, error) {
	order, err := model.GetLiandongOrder(localTradeNo)
	if err != nil {
		return nil, err
	}
	if order.PaymentStatus != model.LiandongPaymentStatusPaid {
		return nil, model.ErrLiandongOrderNotPaid
	}
	if order.FulfillmentStatus == model.LiandongFulfillmentStatusReviewRequired {
		return nil, model.ErrLiandongOrderReviewRequired
	}
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return nil, err
	}
	if !settingsSnapshot.Enabled {
		return nil, errors.New("Operation failed")
	}
	latestSettings, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return nil, err
	}
	if !latestSettings.Enabled {
		return nil, errors.New("liandong payment gateway was disabled")
	}
	fulfilled, fulfillErr := fulfillLiandongOrder(order, latestSettings.PollIntervalSeconds)
	if fulfillErr != nil {
		return nil, fulfillErr
	}
	if !fulfilled {
		return nil, errors.New("Operation failed")
	}
	updated, err := model.GetLiandongOrder(localTradeNo)
	if err != nil {
		return nil, err
	}
	view := LiandongOrderView(updated)
	return &view, nil
}

func liandongErrorBackoffSeconds(pollIntervalSeconds int, consecutiveErrors int) int64 {
	if pollIntervalSeconds < 1 {
		pollIntervalSeconds = 30
	}
	if consecutiveErrors < 1 {
		consecutiveErrors = 1
	}
	if consecutiveErrors > 6 {
		consecutiveErrors = 6
	}
	seconds := int64(pollIntervalSeconds) * int64(1<<consecutiveErrors)
	if seconds > 3600 {
		return 3600
	}
	return seconds
}

func LiandongProviderTradeNoValid(value string) bool {
	return liandongTradeNoPattern.MatchString(value)
}
