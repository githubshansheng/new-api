package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
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
	"github.com/bytedance/gopkg/util/gopool"
	xproxy "golang.org/x/net/proxy"
	"golang.org/x/net/publicsuffix"
)

const (
	liandongBaseURL                              = setting.DefaultLiandongBaseURL
	liandongCreatePath                           = "/shopApi/Pay/order"
	liandongPaymentPath                          = "/shopApi/Pay/payment"
	liandongOrderListPath                        = "/merchantApi/order/list"
	liandongGoodsListPath                        = "/merchantApi/Goods/list"
	liandongLoginPath                            = "/merchantApi/user/login"
	liandongMaxBodyBytes                         = 1 << 20
	liandongDialTimeout                          = 10 * time.Second
	liandongTLSHandshakeTimeout                  = 15 * time.Second
	liandongResponseHeaderTimeout                = 20 * time.Second
	liandongRequestTimeout                       = 30 * time.Second
	liandongMaxDiagnosticRunes                   = 256
	liandongMaxMonitorPayloadRunes               = 4096
	liandongReconcileBatchSize                   = 100
	liandongOperationLeaseTTL                    = 90 * time.Second
	liandongOperationWait                        = 15 * time.Second
	liandongOperationRetry                       = 100 * time.Millisecond
	liandongOrderQueryFailuresBeforeLoginRefresh = 3
)

var liandongTradeNoPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,128}$`)
var liandongACWArgumentPattern = regexp.MustCompile(`(?i)\barg1\s*=\s*['"]([0-9a-f]{40})['"]`)
var liandongACWPermutationPattern = regexp.MustCompile(`(?i)for\s*\(\s*var\s+m\s*=\s*\[([^]]+)\]\s*,\s*p\s*=`)
var liandongSensitiveDiagnosticValuePattern = regexp.MustCompile(
	`(?i)(["']?(?:merchant[-_ ]?token|token|password|username|juuid|contact|goods[-_ ]?key)["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^,\s;&]+)`,
)
var liandongAuthRefreshMu sync.Mutex
var liandongCookieJars sync.Map

type liandongCookieContext struct {
	jar                    http.CookieJar
	origin                 *url.URL
	orderQueryFailureState liandongOrderQueryFailureState
}

type liandongOrderQueryFailureState struct {
	mu       sync.Mutex
	failures int
}

func (s *liandongOrderQueryFailureState) recordFailure() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures++
	return s.failures > liandongOrderQueryFailuresBeforeLoginRefresh
}

func (s *liandongOrderQueryFailureState) reset() {
	s.mu.Lock()
	s.failures = 0
	s.mu.Unlock()
}

func (c *liandongCookieContext) SetCookies(requestURL *url.URL, cookies []*http.Cookie) {
	if requestURL == nil || !strings.EqualFold(requestURL.Scheme, c.origin.Scheme) ||
		!strings.EqualFold(requestURL.Host, c.origin.Host) {
		return
	}
	normalized := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		copy := *cookie
		copy.Domain = ""
		copy.Path = "/"
		normalized = append(normalized, &copy)
	}
	c.jar.SetCookies(c.origin, normalized)
}

func (c *liandongCookieContext) Cookies(requestURL *url.URL) []*http.Cookie {
	if requestURL == nil || !strings.EqualFold(requestURL.Scheme, c.origin.Scheme) ||
		!strings.EqualFold(requestURL.Host, c.origin.Host) {
		return nil
	}
	return c.jar.Cookies(c.origin)
}

type LiandongPaymentView struct {
	LocalTradeNo              string `json:"local_trade_no"`
	ProductName               string `json:"product_name"`
	BusinessType              string `json:"business_type"`
	PaymentStatus             string `json:"payment_status"`
	FulfillmentStatus         string `json:"fulfillment_status"`
	PaymentURL                string `json:"payment_url,omitempty"`
	FallbackURL               string `json:"fallback_url,omitempty"`
	FallbackContact           string `json:"fallback_contact,omitempty"`
	IframeAllowed             bool   `json:"iframe_allowed"`
	CreatedAt                 int64  `json:"created_at"`
	PaidAt                    int64  `json:"paid_at,omitempty"`
	FulfilledAt               int64  `json:"fulfilled_at,omitempty"`
	ExpiresAt                 int64  `json:"expires_at,omitempty"`
	LatePayment               bool   `json:"late_payment,omitempty"`
	ClientPollIntervalSeconds int    `json:"client_poll_interval_seconds"`
}

type LiandongPaymentPage struct {
	HTML        string `json:"html,omitempty"`
	RedirectURL string `json:"redirect_url,omitempty"`
}

type liandongClient struct {
	httpClient                  *http.Client
	baseURL                     string
	configErr                   error
	orderQueryFailureState      *liandongOrderQueryFailureState
	localOrderQueryFailureState liandongOrderQueryFailureState
}

func (c *liandongClient) paymentStatusFailureState() *liandongOrderQueryFailureState {
	if c.orderQueryFailureState != nil {
		return c.orderQueryFailureState
	}
	return &c.localOrderQueryFailureState
}

type liandongMonitorContextKey struct{}

type liandongMonitorMetadata struct {
	Source    string
	Reference string
}

func withLiandongMonitor(ctx context.Context, source string, reference string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, liandongMonitorContextKey{}, liandongMonitorMetadata{
		Source:    source,
		Reference: reference,
	})
}

func liandongMonitorOperation(path string) string {
	switch path {
	case liandongCreatePath:
		return "create_order"
	case liandongPaymentPath:
		return "payment_page_probe"
	case liandongOrderListPath:
		return "query_orders"
	case liandongGoodsListPath:
		return "query_goods"
	case liandongLoginPath:
		return "login"
	default:
		return "proxy_validation"
	}
}

func recordLiandongUpstreamCall(
	ctx context.Context,
	method string,
	path string,
	statusCode int,
	duration time.Duration,
	success bool,
	diagnostic string,
	requestBody []byte,
	responseBody []byte,
	secrets ...string,
) {
	metadata := liandongMonitorMetadata{Source: "unspecified"}
	if ctx != nil {
		if configured, ok := ctx.Value(liandongMonitorContextKey{}).(liandongMonitorMetadata); ok {
			metadata = configured
		}
	}
	if metadata.Source == "" {
		metadata.Source = "unspecified"
	}
	if err := model.RecordLiandongUpstreamCall(model.LiandongUpstreamCall{
		Source:     metadata.Source,
		Reference:  metadata.Reference,
		Operation:  liandongMonitorOperation(path),
		Method:     method,
		Path:       path,
		StatusCode: statusCode,
		Success:    success,
		DurationMS: duration.Milliseconds(),
		RequestBody: sanitizeLiandongMonitorPayload(
			requestBody,
			secrets...,
		),
		ResponseBody: sanitizeLiandongMonitorPayload(
			responseBody,
			secrets...,
		),
		Error: diagnostic,
	}); err != nil {
		common.SysError("failed to record card marketplace upstream call: " + err.Error())
	}
}

func classifyLiandongUpstreamCall(
	baseURL string,
	path string,
	statusCode int,
	responseBody []byte,
	requestErr error,
	secrets ...string,
) (bool, string) {
	if requestErr != nil {
		return false, sanitizeLiandongDiagnostic(requestErr.Error(), secrets...)
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return false, fmt.Sprintf("provider returned HTTP %d", statusCode)
	}
	var err error
	switch path {
	case liandongCreatePath:
		_, err = parseLiandongCreateTradeNoForBaseURL(responseBody, baseURL)
	case liandongOrderListPath:
		_, err = parseLiandongOrderRecords(responseBody)
	case liandongGoodsListPath:
		_, err = parseLiandongGoods(responseBody)
	case liandongLoginPath:
		_, err = parseLiandongLoginToken(responseBody)
	}
	if err != nil {
		return false, sanitizeLiandongDiagnostic(err.Error(), secrets...)
	}
	return true, ""
}

type liandongCreateError struct {
	definitive  bool
	refreshAuth bool
	fallback    bool
	err         error
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
	ProviderTradeNo  string
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
	Contact   string          `json:"contact"`
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
	var cookieJar http.CookieJar
	var cookieContext *liandongCookieContext
	if configErr == nil {
		if sharedJar, ok := liandongCookieJars.Load(baseURL); ok {
			cookieContext = sharedJar.(*liandongCookieContext)
			cookieJar = cookieContext
		} else {
			standardJar, jarErr := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
			if jarErr != nil {
				configErr = jarErr
			} else {
				newJar := &liandongCookieContext{
					jar: standardJar,
					origin: &url.URL{
						Scheme: parsedBaseURL.Scheme,
						Host:   parsedBaseURL.Host,
						Path:   "/",
					},
				}
				sharedJar, _ := liandongCookieJars.LoadOrStore(baseURL, newJar)
				cookieContext = sharedJar.(*liandongCookieContext)
				cookieJar = cookieContext
			}
		}
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
	var orderQueryFailureState *liandongOrderQueryFailureState
	if cookieContext != nil {
		orderQueryFailureState = &cookieContext.orderQueryFailureState
	}
	return &liandongClient{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
			Jar:       cookieJar,
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
		baseURL:                baseURL,
		configErr:              configErr,
		orderQueryFailureState: orderQueryFailureState,
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
) (validationErr error) {
	ctx = withLiandongMonitor(ctx, "proxy_validation", "")
	startedAt := time.Now()
	statusCode := 0
	defer func() {
		success, diagnostic := classifyLiandongUpstreamCall(
			settingsSnapshot.BaseURL,
			"/",
			statusCode,
			nil,
			validationErr,
			settingsSnapshot.ProxyUsername,
			settingsSnapshot.ProxyPassword,
		)
		recordLiandongUpstreamCall(
			ctx,
			http.MethodGet,
			"/",
			statusCode,
			time.Since(startedAt),
			success,
			diagnostic,
			nil,
			nil,
			settingsSnapshot.ProxyUsername,
			settingsSnapshot.ProxyPassword,
		)
	}()
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
	statusCode = resp.StatusCode
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
		browserVerification := isLiandongBrowserVerificationPage(responseBody)
		return "", &liandongCreateError{
			definitive:  browserVerification || errors.As(err, &rejection),
			refreshAuth: browserVerification,
			fallback:    browserVerification,
			err:         err,
		}
	}
	return tradeNo, nil
}

func (c *liandongClient) createOrderWithSettings(
	ctx context.Context,
	goodsKey string,
	contact string,
	juuid string,
	settingsSnapshot setting.LiandongPaymentSettings,
) (string, error) {
	if settingsSnapshot.AuthMode == setting.LiandongAuthModeCredentials {
		if c.httpClient == nil || c.httpClient.Jar == nil {
			return "", &liandongCreateError{
				definitive: true,
				err:        errors.New("liandong cookie storage is unavailable"),
			}
		}
		endpoint, endpointErr := liandongEndpointURL(c.baseURL, liandongCreatePath)
		endpointURL, parseErr := url.Parse(endpoint)
		if endpointErr != nil {
			return "", &liandongCreateError{definitive: true, err: endpointErr}
		}
		if parseErr != nil {
			return "", &liandongCreateError{definitive: true, err: parseErr}
		}
		if len(c.httpClient.Jar.Cookies(endpointURL)) == 0 {
			if _, err := c.refreshMerchantToken(
				ctx,
				strings.TrimSpace(settingsSnapshot.MerchantToken),
			); err != nil {
				return "", &liandongCreateError{
					definitive: true,
					err:        fmt.Errorf("liandong login before order creation failed: %w", err),
				}
			}
		}
	}

	tradeNo, err := c.createOrder(ctx, goodsKey, contact, juuid)
	if err == nil || settingsSnapshot.AuthMode != setting.LiandongAuthModeCredentials {
		return tradeNo, err
	}
	var createErr *liandongCreateError
	if !errors.As(err, &createErr) || !createErr.refreshAuth {
		return "", err
	}
	if _, refreshErr := c.refreshMerchantToken(
		ctx,
		strings.TrimSpace(settingsSnapshot.MerchantToken),
	); refreshErr != nil {
		return "", &liandongCreateError{
			definitive: true,
			fallback:   true,
			err: fmt.Errorf(
				"provider verification blocked order creation and login refresh failed: %w",
				refreshErr,
			),
		}
	}
	return c.createOrder(ctx, goodsKey, contact, juuid)
}

func (c *liandongClient) probePaymentPage(
	ctx context.Context,
	providerTradeNo string,
	settingsSnapshot setting.LiandongPaymentSettings,
) error {
	_, err := c.loadPaymentPage(ctx, providerTradeNo, settingsSnapshot)
	return err
}

func (c *liandongClient) loadPaymentPage(
	ctx context.Context,
	providerTradeNo string,
	settingsSnapshot setting.LiandongPaymentSettings,
) (*LiandongPaymentPage, error) {
	merchantToken := strings.TrimSpace(settingsSnapshot.MerchantToken)
	if merchantToken == "" && settingsSnapshot.AuthMode == setting.LiandongAuthModeCredentials {
		refreshed, err := c.refreshMerchantToken(ctx, "")
		if err != nil {
			return nil, err
		}
		merchantToken = refreshed
	}
	if merchantToken == "" {
		return nil, errors.New("liandong merchant token is not configured")
	}

	resp, body, err := c.requestPaymentPage(ctx, providerTradeNo, merchantToken)
	if err != nil {
		if settingsSnapshot.AuthMode != setting.LiandongAuthModeCredentials {
			return nil, err
		}
	} else {
		validationErr := validateLiandongPaymentPageResponse(resp, body)
		if !liandongUnauthorizedResponse(resp.StatusCode, body) && validationErr == nil {
			return makeLiandongPaymentPage(resp, body, settingsSnapshot.BaseURL)
		}
		if settingsSnapshot.AuthMode != setting.LiandongAuthModeCredentials {
			if liandongUnauthorizedResponse(resp.StatusCode, body) {
				return nil, errors.New("liandong authentication failed")
			}
			return nil, validationErr
		}
	}
	refreshed, err := c.refreshMerchantToken(ctx, merchantToken)
	if err != nil {
		return nil, err
	}
	resp, body, err = c.requestPaymentPage(ctx, providerTradeNo, refreshed)
	if err != nil {
		return nil, err
	}
	if liandongUnauthorizedResponse(resp.StatusCode, body) {
		return nil, errors.New("liandong authentication failed after token refresh")
	}
	if err := validateLiandongPaymentPageResponse(resp, body); err != nil {
		return nil, err
	}
	return makeLiandongPaymentPage(resp, body, settingsSnapshot.BaseURL)
}

func makeLiandongPaymentPage(
	resp *http.Response,
	body []byte,
	baseURL string,
) (*LiandongPaymentPage, error) {
	if resp == nil {
		return nil, errors.New("payment page returned no response")
	}
	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		location, err := resp.Location()
		if err != nil || !location.IsAbs() || !strings.EqualFold(location.Scheme, "https") {
			return nil, errors.New("payment page returned an invalid redirect")
		}
		return &LiandongPaymentPage{RedirectURL: location.String()}, nil
	}

	normalizedBaseURL, err := setting.NormalizeLiandongBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	baseTag := []byte(`<base href="` + html.EscapeString(strings.TrimRight(normalizedBaseURL, "/")+"/") + `">`)
	lowerBody := bytes.ToLower(body)
	if !bytes.Contains(lowerBody, []byte("<base")) {
		headStart := bytes.Index(lowerBody, []byte("<head"))
		if headStart >= 0 {
			if headEnd := bytes.IndexByte(body[headStart:], '>'); headEnd >= 0 {
				insertAt := headStart + headEnd + 1
				withBase := make([]byte, 0, len(body)+len(baseTag))
				withBase = append(withBase, body[:insertAt]...)
				withBase = append(withBase, baseTag...)
				withBase = append(withBase, body[insertAt:]...)
				body = withBase
			}
		} else {
			body = append(baseTag, body...)
		}
	}
	return &LiandongPaymentPage{HTML: string(body)}, nil
}

func (c *liandongClient) requestPaymentPage(
	ctx context.Context,
	providerTradeNo string,
	merchantToken string,
) (response *http.Response, responseBody []byte, requestErr error) {
	startedAt := time.Now()
	requestBody, _ := common.Marshal(map[string]string{"trade_no": providerTradeNo})
	defer func() {
		success := requestErr == nil
		diagnostic := ""
		statusCode := 0
		if response != nil {
			statusCode = response.StatusCode
		}
		if requestErr != nil {
			diagnostic = sanitizeLiandongDiagnostic(requestErr.Error(), merchantToken)
		} else if err := validateLiandongPaymentPageResponse(response, responseBody); err != nil {
			success = false
			diagnostic = sanitizeLiandongDiagnostic(err.Error(), merchantToken)
		}
		recordLiandongUpstreamCall(
			ctx,
			http.MethodGet,
			liandongPaymentPath,
			statusCode,
			time.Since(startedAt),
			success,
			diagnostic,
			requestBody,
			responseBody,
			merchantToken,
		)
	}()

	response, responseBody, requestErr = c.requestPaymentPageOnce(ctx, providerTradeNo, merchantToken)
	if requestErr != nil || !isLiandongBrowserVerificationPage(responseBody) {
		return response, responseBody, requestErr
	}
	endpoint, endpointErr := liandongEndpointURL(c.baseURL, liandongPaymentPath)
	if endpointErr != nil || !c.applyLiandongBrowserVerificationCookie(endpoint, responseBody) {
		return response, responseBody, requestErr
	}
	return c.requestPaymentPageOnce(ctx, providerTradeNo, merchantToken)
}

func (c *liandongClient) requestPaymentPageOnce(
	ctx context.Context,
	providerTradeNo string,
	merchantToken string,
) (response *http.Response, responseBody []byte, requestErr error) {
	if c.configErr != nil {
		return nil, nil, c.configErr
	}
	if !liandongTradeNoPattern.MatchString(providerTradeNo) {
		return nil, nil, errors.New("provider trade number is invalid")
	}
	if strings.TrimSpace(merchantToken) == "" {
		return nil, nil, errors.New("merchant token is missing")
	}
	endpoint, err := liandongEndpointURL(c.baseURL, liandongPaymentPath)
	if err != nil {
		return nil, nil, err
	}
	paymentURL := endpoint + "?trade_no=" + url.QueryEscape(providerTradeNo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, paymentURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("merchant-token", merchantToken)
	configuredURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, nil, err
	}
	probeHTTPClient := *c.httpClient
	probeHTTPClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if strings.EqualFold(req.URL.Scheme, configuredURL.Scheme) &&
			strings.EqualFold(req.URL.Host, configuredURL.Host) {
			return nil
		}
		if strings.EqualFold(req.URL.Scheme, "https") && req.URL.Host != "" {
			return http.ErrUseLastResponse
		}
		return errors.New("payment page redirect target is not allowed")
	}
	resp, err := probeHTTPClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	response = resp
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, liandongMaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return resp, body, err
	}
	if len(body) > liandongMaxBodyBytes {
		return resp, nil, errors.New("payment page response is too large")
	}
	return resp, body, nil
}

func validateLiandongPaymentPageResponse(resp *http.Response, body []byte) error {
	if resp == nil {
		return errors.New("payment page returned no response")
	}
	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		redirectTarget, err := resp.Location()
		if err == nil && redirectTarget.IsAbs() &&
			strings.EqualFold(redirectTarget.Scheme, "https") {
			return nil
		}
		return errors.New("payment page returned an invalid redirect")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("payment page returned HTTP %d", resp.StatusCode)
	}
	normalized := normalizeLiandongJSONBody(body)
	if len(normalized) == 0 {
		return errors.New("payment page returned an empty response")
	}
	if isLiandongBrowserVerificationPage(normalized) {
		return errors.New("payment page returned an upstream browser verification page")
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/html") && normalized[0] != '<' {
		return errors.New("payment page did not return HTML")
	}
	return nil
}

func (c *liandongClient) queryOrderWithSettings(
	ctx context.Context,
	settingsSnapshot setting.LiandongPaymentSettings,
	order *model.LiandongOrder,
) (*liandongVerification, error) {
	if order == nil {
		return nil, errors.New("liandong order is missing")
	}
	payload := struct {
		Current  int    `json:"current"`
		PageSize int    `json:"pageSize"`
		Status   int    `json:"status"`
		TradeNo  string `json:"trade_no,omitempty"`
		Contact  string `json:"contact,omitempty"`
	}{
		Current:  1,
		PageSize: 10,
		Status:   999,
	}
	expectedTradeNo := ""
	expectedContact := ""
	if order.ProviderTradeNo != nil {
		expectedTradeNo = strings.TrimSpace(*order.ProviderTradeNo)
		payload.TradeNo = expectedTradeNo
		payload.PageSize = 1
	} else {
		expectedContact = strings.TrimSpace(order.ContactSnapshot)
		if !model.ValidLiandongContact(expectedContact) {
			return nil, errors.New("provider order identity is missing")
		}
		payload.Contact = expectedContact
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
	verification, err := parseLiandongOrderVerificationByIdentity(
		responseBody,
		expectedTradeNo,
		expectedContact,
	)
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
	unauthorized := err == nil && liandongUnauthorizedResponse(statusCode, responseBody)
	authenticatedRequestFailed := false
	if path == liandongOrderListPath || path == liandongGoodsListPath {
		authenticatedRequestFailed = err != nil || statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices
		if !authenticatedRequestFailed {
			if path == liandongOrderListPath {
				_, err = parseLiandongOrderRecords(responseBody)
			} else {
				_, err = parseLiandongGoods(responseBody)
			}
			authenticatedRequestFailed = err != nil
		}
	}
	if !unauthorized && !authenticatedRequestFailed {
		if err != nil {
			return 0, nil, token, &liandongQueryError{systemic: true, err: err}
		}
		if path == liandongOrderListPath {
			c.paymentStatusFailureState().reset()
		}
		return statusCode, responseBody, token, nil
	}
	shouldRefreshLogin := settingsSnapshot.AuthMode == setting.LiandongAuthModeCredentials
	if shouldRefreshLogin && path == liandongOrderListPath {
		shouldRefreshLogin = c.paymentStatusFailureState().recordFailure()
	}
	if !shouldRefreshLogin {
		if err != nil {
			return 0, nil, token, &liandongQueryError{systemic: true, err: err}
		}
		if !unauthorized {
			return statusCode, responseBody, token, nil
		}
		return statusCode, responseBody, token, &liandongQueryError{
			statusCode: http.StatusUnauthorized,
			systemic:   true,
			err:        errors.New("liandong authentication failed"),
		}
	}
	if ctx.Err() != nil {
		return 0, nil, token, &liandongQueryError{systemic: true, err: ctx.Err()}
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
	if path == liandongOrderListPath {
		if retryStatus < http.StatusOK || retryStatus >= http.StatusMultipleChoices {
			return retryStatus, retryBody, refreshed, nil
		}
		if _, err := parseLiandongOrderRecords(retryBody); err != nil {
			return retryStatus, retryBody, refreshed, &liandongQueryError{systemic: true, err: err}
		}
		c.paymentStatusFailureState().reset()
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
) (statusCode int, responseBody []byte, requestErr error) {
	startedAt := time.Now()
	defer func() {
		success, diagnostic := classifyLiandongUpstreamCall(
			c.baseURL,
			path,
			statusCode,
			responseBody,
			requestErr,
			merchantToken,
		)
		recordLiandongUpstreamCall(
			ctx,
			method,
			path,
			statusCode,
			time.Since(startedAt),
			success,
			diagnostic,
			body,
			responseBody,
			merchantToken,
		)
	}()

	statusCode, responseBody, requestErr = c.doJSONOnce(ctx, method, path, body, merchantToken)
	if requestErr != nil || !isLiandongBrowserVerificationPage(responseBody) {
		return statusCode, responseBody, requestErr
	}
	endpoint, endpointErr := liandongEndpointURL(c.baseURL, path)
	if endpointErr != nil || !c.applyLiandongBrowserVerificationCookie(endpoint, responseBody) {
		return statusCode, responseBody, requestErr
	}
	return c.doJSONOnce(ctx, method, path, body, merchantToken)
}

func (c *liandongClient) doJSONOnce(
	ctx context.Context,
	method string,
	path string,
	body []byte,
	merchantToken string,
) (statusCode int, responseBody []byte, requestErr error) {
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
	responseBody, err = io.ReadAll(limited)
	if err != nil {
		return resp.StatusCode, responseBody, err
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
	acwChallenge := bytes.Contains(normalized, []byte("<script")) &&
		bytes.Contains(normalized, []byte("var arg1="))
	aliyunChallenge := bytes.Contains(normalized, []byte("_waf_is_mobile")) &&
		(bytes.Contains(normalized, []byte("aliyuncaptcha")) ||
			bytes.Contains(normalized, []byte("aliyun-captcha")) ||
			bytes.Contains(normalized, []byte("waf_nc_h5_block")))
	return acwChallenge || aliyunChallenge
}

func (c *liandongClient) applyLiandongBrowserVerificationCookie(
	endpoint string,
	body []byte,
) bool {
	if c == nil || c.httpClient == nil || c.httpClient.Jar == nil {
		return false
	}
	argumentMatch := liandongACWArgumentPattern.FindSubmatch(body)
	permutationMatch := liandongACWPermutationPattern.FindSubmatch(body)
	if len(argumentMatch) != 2 || len(permutationMatch) != 2 {
		return false
	}

	parts := strings.Split(string(permutationMatch[1]), ",")
	if len(parts) != 40 {
		return false
	}
	permutation := make([]int, len(parts))
	seen := make([]bool, len(parts)+1)
	for index, part := range parts {
		value, err := strconv.ParseInt(strings.TrimSpace(part), 0, 32)
		if err != nil || value < 1 || value > int64(len(parts)) || seen[value] {
			return false
		}
		permutation[index] = int(value)
		seen[value] = true
	}

	argument := argumentMatch[1]
	reordered := make([]byte, len(argument))
	for argumentIndex, character := range argument {
		for targetIndex, sourcePosition := range permutation {
			if sourcePosition == argumentIndex+1 {
				reordered[targetIndex] = character
				break
			}
		}
	}
	challenge, err := hex.DecodeString(string(reordered))
	if err != nil {
		return false
	}
	key, err := hex.DecodeString("3000176000856006061501533003690027800375")
	if err != nil || len(challenge) != len(key) {
		return false
	}
	for index := range challenge {
		challenge[index] ^= key[index]
	}

	requestURL, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	c.httpClient.Jar.SetCookies(requestURL, []*http.Cookie{{
		Name:    "acw_sc__v2",
		Value:   hex.EncodeToString(challenge),
		Path:    "/",
		Secure:  strings.EqualFold(requestURL.Scheme, "https"),
		Expires: time.Now().Add(time.Hour),
	}})
	return true
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
	return parseLiandongOrderVerificationByIdentity(body, expectedTradeNo, "")
}

func parseLiandongOrderVerificationByIdentity(
	body []byte,
	expectedTradeNo string,
	expectedContact string,
) (*liandongVerification, error) {
	var payload liandongOrderListResponse
	body = normalizeLiandongJSONBody(body)
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, invalidLiandongJSONResponse("order", body)
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
	expectedTradeNo = strings.TrimSpace(expectedTradeNo)
	expectedContact = strings.TrimSpace(expectedContact)
	matched := make([]liandongOrderRecord, 0, 1)
	for _, record := range records {
		tradeNo := strings.TrimSpace(record.TradeNo)
		if tradeNo == "" {
			tradeNo = strings.TrimSpace(record.TradeNoV2)
		}
		if expectedTradeNo != "" && tradeNo == expectedTradeNo {
			matched = append(matched, record)
			continue
		}
		if expectedTradeNo == "" &&
			expectedContact != "" &&
			strings.TrimSpace(record.Contact) == expectedContact {
			matched = append(matched, record)
		}
	}
	if len(matched) == 0 {
		if len(records) > 0 && expectedTradeNo != "" {
			return &liandongVerification{ReviewRequired: true}, nil
		}
		return &liandongVerification{}, nil
	}
	if len(matched) != 1 {
		return &liandongVerification{ReviewRequired: true}, nil
	}

	record := matched[0]
	tradeNo := strings.TrimSpace(record.TradeNo)
	if tradeNo == "" {
		tradeNo = strings.TrimSpace(record.TradeNoV2)
	}
	if !liandongTradeNoPattern.MatchString(tradeNo) ||
		(expectedTradeNo != "" && tradeNo != expectedTradeNo) {
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
		ProviderTradeNo:  tradeNo,
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
	ctx = withLiandongMonitor(ctx, "provider_goods", "")
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

func liandongProductURL(baseURL string, goodsKey string) string {
	goodsKey = strings.TrimSpace(goodsKey)
	if goodsKey == "" || len(goodsKey) > 128 {
		return ""
	}
	normalizedBaseURL, err := setting.NormalizeLiandongBaseURL(baseURL)
	if err != nil {
		return ""
	}
	parsed, err := url.Parse(normalizedBaseURL)
	if err != nil {
		return ""
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	parsed.Path = basePath + "/item/" + goodsKey
	parsed.RawPath = parsed.EscapedPath()
	return parsed.String()
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

func sanitizeLiandongText(message string, maxRunes int, secrets ...string) string {
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
	if maxRunes > 0 && len(messageRunes) > maxRunes {
		sanitized = string(messageRunes[:maxRunes])
	}
	return sanitized
}

func sanitizeLiandongDiagnostic(message string, secrets ...string) string {
	return sanitizeLiandongText(message, liandongMaxDiagnosticRunes, secrets...)
}

func sanitizeLiandongMonitorPayload(body []byte, secrets ...string) string {
	normalized := normalizeLiandongJSONBody(body)
	if len(normalized) == 0 {
		return ""
	}

	var payload any
	if err := common.Unmarshal(normalized, &payload); err == nil {
		redactLiandongMonitorPayload(payload)
		if encoded, marshalErr := common.Marshal(payload); marshalErr == nil {
			normalized = encoded
		}
	}
	return sanitizeLiandongText(
		string(normalized),
		liandongMaxMonitorPayloadRunes,
		secrets...,
	)
}

func redactLiandongMonitorPayload(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if liandongMonitorPayloadKeyIsSensitive(key) {
				typed[key] = "[redacted]"
				continue
			}
			redactLiandongMonitorPayload(nested)
		}
	case []any:
		for _, nested := range typed {
			redactLiandongMonitorPayload(nested)
		}
	}
}

func liandongMonitorPayloadKeyIsSensitive(key string) bool {
	normalized := strings.Map(func(char rune) rune {
		if char >= 'A' && char <= 'Z' {
			return char + ('a' - 'A')
		}
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			return char
		}
		return -1
	}, key)
	if strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "cookie") ||
		strings.Contains(normalized, "secret") {
		return true
	}
	switch normalized {
	case "username", "account", "passwd", "pwd", "juuid", "contact", "goodskey", "cardkey", "cardpassword", "kami":
		return true
	default:
		return false
	}
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
			order.PaymentStatus == model.LiandongPaymentStatusCreateUnknown) {
		if order.ProviderTradeNo != nil {
			view.PaymentURL = "/api/payment/liandong/orders/" +
				url.PathEscape(order.LocalTradeNo) + "/page"
		} else if order.PaymentStatus == model.LiandongPaymentStatusPending {
			view.FallbackURL = liandongProductURL(
				settingsSnapshot.BaseURL,
				order.GoodsKeySnapshot,
			)
			if view.FallbackURL != "" {
				view.FallbackContact = order.ContactSnapshot
				view.IframeAllowed = false
			}
		}
	}
	return view
}

func LoadLiandongPaymentPageForUser(
	ctx context.Context,
	userID int,
	localTradeNo string,
) (*LiandongPaymentPage, error) {
	ctx = withLiandongMonitor(ctx, "user_payment_page", localTradeNo)
	order, err := model.GetLiandongOrderForUser(userID, localTradeNo)
	if err != nil {
		return nil, err
	}
	if order.ProviderTradeNo == nil ||
		(order.PaymentStatus != model.LiandongPaymentStatusPending &&
			order.PaymentStatus != model.LiandongPaymentStatusCreateUnknown) {
		return nil, errors.New("payment page is not available for this order")
	}
	if order.ExpiresAt > 0 && order.ExpiresAt <= time.Now().Unix() {
		return nil, errors.New("payment order has expired")
	}

	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return nil, err
	}
	if !settingsSnapshot.Enabled {
		return nil, errors.New("liandong payment gateway is disabled")
	}
	if !liandongAuthenticationConfigured(settingsSnapshot) {
		return nil, errors.New("liandong authentication is not configured")
	}
	page, err := newLiandongClientWithSettings(settingsSnapshot).loadPaymentPage(
		ctx,
		*order.ProviderTradeNo,
		settingsSnapshot,
	)
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
	return page, nil
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

	providerTradeNo, createErr := client.createOrderWithSettings(
		withLiandongMonitor(ctx, "user_order_create", order.LocalTradeNo),
		order.GoodsKeySnapshot,
		order.ContactSnapshot,
		order.JUUIDSnapshot,
		latestSettings,
	)
	if createErr != nil {
		var typedErr *liandongCreateError
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
		if errors.As(createErr, &typedErr) &&
			typedErr.fallback &&
			liandongProductURL(latestSettings.BaseURL, order.GoodsKeySnapshot) != "" {
			if markErr := model.MarkLiandongCreateResult(
				order.LocalTradeNo,
				nil,
				model.LiandongPaymentStatusPending,
				diagnostic,
			); markErr != nil {
				return nil, errors.New("Failed to create payment order")
			}
			order.PaymentStatus = model.LiandongPaymentStatusPending
			order.LastError = diagnostic
			WakeSystemTaskRunner()
			view := LiandongOrderView(order)
			return &view, nil
		}

		status := model.LiandongPaymentStatusCreateUnknown
		if errors.As(createErr, &typedErr) && typedErr.definitive {
			status = model.LiandongPaymentStatusCreateFailed
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
	if latestSettings.PaymentProbeEnabled {
		scheduleLiandongPaymentProbe(order)
	}
	WakeSystemTaskRunner()
	view := LiandongOrderView(order)
	return &view, nil
}

func bindLiandongVerificationTradeNo(
	order *model.LiandongOrder,
	verification *liandongVerification,
) error {
	if order == nil || verification == nil {
		return model.ErrLiandongOrderBusy
	}
	providerTradeNo := strings.TrimSpace(verification.ProviderTradeNo)
	if providerTradeNo == "" {
		return nil
	}
	if order.ProviderTradeNo != nil {
		if strings.TrimSpace(*order.ProviderTradeNo) != providerTradeNo {
			return model.ErrLiandongOrderReviewRequired
		}
		return nil
	}
	if err := model.BindClaimedLiandongProviderTradeNo(
		order.LocalTradeNo,
		order.CheckLockUntil,
		providerTradeNo,
	); err != nil {
		return err
	}
	order.ProviderTradeNo = &providerTradeNo
	return nil
}

func closeLiandongOrderAfterVerification(
	ctx context.Context,
	client *liandongClient,
	order *model.LiandongOrder,
	userID int,
	reason string,
) (bool, error) {
	if order == nil {
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
	if err := bindLiandongVerificationTradeNo(claimed, verification); err != nil {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
		return false, err
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
		if claimed.ProviderTradeNo == nil {
			_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
			return false, model.ErrLiandongOrderReviewRequired
		}
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
	ctx = withLiandongMonitor(ctx, "client_order_poll", localTradeNo)
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
	if err := bindLiandongVerificationTradeNo(claimed, verification); err != nil {
		_ = model.ReleaseLiandongOrderCheck(claimed.LocalTradeNo, claimed.CheckLockUntil)
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
	if verification.ReviewRequired {
		_ = model.MarkLiandongOrderReviewRequired(
			claimed.LocalTradeNo,
			claimed.CheckLockUntil,
			verification.SanitizedSummary,
			"provider order identity is ambiguous or invalid",
		)
	} else if verification.Paid && claimed.ProviderTradeNo != nil {
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
	ctx = withLiandongMonitor(ctx, "user_order_close", localTradeNo)
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
	ctx = withLiandongMonitor(ctx, "root_order_close", localTradeNo)
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

func scheduleLiandongPaymentProbe(order *model.LiandongOrder) {
	if order == nil || order.ProviderTradeNo == nil {
		return
	}
	localTradeNo := order.LocalTradeNo
	productName := order.ProductNameSnapshot
	providerTradeNo := *order.ProviderTradeNo
	gopool.Go(func() {
		if err := runLiandongPaymentProbeForCreatedOrder(
			context.Background(),
			localTradeNo,
			productName,
			providerTradeNo,
		); err != nil {
			common.SysError(fmt.Sprintf(
				"liandong payment probe failed for local order %s: %v",
				localTradeNo,
				err,
			))
		}
	})
}

func runLiandongPaymentProbeForCreatedOrder(
	ctx context.Context,
	localTradeNo string,
	productName string,
	providerTradeNo string,
) error {
	ctx = withLiandongMonitor(ctx, "payment_probe", localTradeNo)
	settingsSnapshot, err := model.GetLiandongPaymentSettingsFromDB()
	if err != nil {
		return err
	}
	if !settingsSnapshot.Enabled || !settingsSnapshot.PaymentProbeEnabled {
		return nil
	}

	probeErr := newLiandongClientWithSettings(settingsSnapshot).probePaymentPage(
		ctx,
		providerTradeNo,
		settingsSnapshot,
	)
	if probeErr == nil {
		return nil
	}

	latestSettings, settingsErr := model.GetLiandongPaymentSettingsFromDB()
	if settingsErr != nil {
		return errors.Join(probeErr, settingsErr)
	}
	if !latestSettings.Enabled || !latestSettings.PaymentProbeEnabled {
		return nil
	}

	diagnostic := sanitizeLiandongDiagnostic(
		probeErr.Error(),
		settingsSnapshot.JUUID,
		settingsSnapshot.Username,
		settingsSnapshot.Password,
		settingsSnapshot.MerchantToken,
		settingsSnapshot.ProxyUsername,
		settingsSnapshot.ProxyPassword,
		latestSettings.JUUID,
		latestSettings.Username,
		latestSettings.Password,
		latestSettings.MerchantToken,
		latestSettings.ProxyUsername,
		latestSettings.ProxyPassword,
	)
	if diagnostic == "" {
		diagnostic = "payment QR page probe failed"
	}

	receiver := strings.TrimSpace(latestSettings.PaymentProbeAlertEmail)
	if receiver == "" {
		return errors.New(diagnostic)
	}
	subject := fmt.Sprintf("[%s] 卡网支付二维码接口异常", common.SystemName)
	content := fmt.Sprintf(
		"<p>卡网支付二维码接口探测失败。</p>"+
			"<p>Card marketplace payment QR probe failed after a real order was created.</p>"+
			"<ul><li>时间 / Time: %s</li><li>本地订单 / Local order: %s</li>"+
			"<li>商品 / Product: %s</li><li>接口地址 / Base URL: %s</li>"+
			"<li>代理 / Proxy: %t</li><li>错误 / Error: %s</li></ul>",
		time.Now().Format(time.RFC3339),
		html.EscapeString(localTradeNo),
		html.EscapeString(productName),
		html.EscapeString(settingsSnapshot.BaseURL),
		settingsSnapshot.ProxyEnabled,
		html.EscapeString(diagnostic),
	)
	if err := common.SendEmail(subject, receiver, content); err != nil {
		common.SysError(fmt.Sprintf(
			"failed to send liandong payment probe alert to %s: %v",
			common.MaskEmail(receiver),
			err,
		))
		return errors.Join(
			errors.New(diagnostic),
			errors.New("payment probe alert email could not be sent"),
		)
	}
	return errors.New(diagnostic)
}

func RunLiandongReconcileOnce(ctx context.Context) (map[string]int, error) {
	return runLiandongReconcileOnce(
		withLiandongMonitor(ctx, "scheduled_reconcile", ""),
		newLiandongClient(),
	)
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
			if err := reconcileStaleLiandongCreatingOrders(ctx, result); err != nil {
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
		contact := strings.TrimSpace(record.Contact)
		summary, err := common.Marshal(map[string]any{
			"trade_no": tradeNo,
			"status":   status,
		})
		if err != nil {
			return err
		}
		claimed, err := model.ClaimLiandongOrderByProviderTradeNo(tradeNo)
		if errors.Is(err, model.ErrLiandongOrderNotFound) &&
			model.ValidLiandongContact(contact) {
			claimed, err = model.ClaimLiandongFallbackOrderByContact(contact, tradeNo)
		}
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
