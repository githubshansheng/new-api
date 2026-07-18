package setting

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

const (
	DefaultLiandongBaseURL                   = "https://pay.ldxp.cn"
	DefaultLiandongJUUID                     = ""
	DefaultLiandongPollIntervalSeconds       = 30
	MinLiandongPollIntervalSeconds           = 1
	MaxLiandongPollIntervalSeconds           = 3600
	DefaultLiandongClientPollIntervalSeconds = 5
	MinLiandongClientPollIntervalSeconds     = 1
	MaxLiandongClientPollIntervalSeconds     = 60
	DefaultLiandongReconcileBatchSize        = 50
	MinLiandongReconcileBatchSize            = 1
	MaxLiandongReconcileBatchSize            = 500
	DefaultLiandongPaymentTimeoutMinutes     = 30
	MinLiandongPaymentTimeoutMinutes         = 1
	MaxLiandongPaymentTimeoutMinutes         = 1440

	LiandongAuthModeManualToken = "manual_token"
	LiandongAuthModeCredentials = "credentials"
)

type LiandongPaymentSettings struct {
	Enabled                   bool
	CreateEnabled             bool
	ReconcileEnabled          bool
	FulfillEnabled            bool
	IframeEnabled             bool
	BaseURL                   string
	ProxyEnabled              bool
	ProxyURL                  string
	ProxyUsername             string
	ProxyPassword             string
	PollIntervalSeconds       int
	ClientPollIntervalSeconds int
	ReconcileBatchSize        int
	PaymentTimeoutMinutes     int
	JUUID                     string
	AuthMode                  string
	Username                  string
	Password                  string
	MerchantToken             string
}

func NormalizeLiandongBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultLiandongBaseURL, nil
	}
	if len(value) > 2048 {
		return "", errors.New("card marketplace base URL is too long")
	}
	parsed, err := url.Parse(value)
	if err != nil ||
		!strings.EqualFold(parsed.Scheme, "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", errors.New("card marketplace base URL must be an HTTPS URL without credentials, query, or fragment")
	}
	parsed.Scheme = "https"
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed.String(), nil
}

func NormalizeLiandongSOCKS5ProxyURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 2048 {
		return "", errors.New("SOCKS5 proxy URL is too long")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", errors.New("invalid SOCKS5 proxy URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "socks5" && scheme != "socks5h" {
		return "", errors.New("SOCKS5 proxy URL must use socks5:// or socks5h://")
	}
	if parsed.Hostname() == "" ||
		parsed.Port() == "" ||
		parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", errors.New("SOCKS5 proxy URL must contain only host and port; configure credentials separately")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("SOCKS5 proxy port must be between 1 and 65535")
	}
	parsed.Scheme = scheme
	parsed.Path = ""
	parsed.RawPath = ""
	return parsed.String(), nil
}
