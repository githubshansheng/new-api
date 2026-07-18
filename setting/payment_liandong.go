package setting

import (
	"errors"
	"net"
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

type LiandongSOCKS5ProxyConfig struct {
	URL      string
	Username string
	Password string
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

func ParseLiandongSOCKS5Proxy(value string) (LiandongSOCKS5ProxyConfig, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return LiandongSOCKS5ProxyConfig{}, nil
	}
	if len(value) > 2048 {
		return LiandongSOCKS5ProxyConfig{}, errors.New("SOCKS5 proxy URL is too long")
	}

	scheme := "socks5h"
	raw := value
	hasScheme := false
	if schemeEnd := strings.Index(raw, "://"); schemeEnd >= 0 {
		hasScheme = true
		scheme = strings.ToLower(strings.TrimSpace(raw[:schemeEnd]))
		raw = raw[schemeEnd+3:]
	}
	if scheme != "socks5" && scheme != "socks5h" {
		return LiandongSOCKS5ProxyConfig{}, errors.New("SOCKS5 proxy URL must use socks5:// or socks5h://")
	}
	raw = strings.TrimRight(raw, "/")
	if strings.ContainsAny(raw, "/?#") {
		return LiandongSOCKS5ProxyConfig{}, errors.New("SOCKS5 proxy URL must contain only a host, port, and optional credentials")
	}

	if hasScheme && strings.Count(raw, "@") == 1 {
		parsed, err := url.Parse(value)
		if err == nil &&
			parsed.User != nil &&
			parsed.Path == "" &&
			parsed.RawQuery == "" &&
			parsed.Fragment == "" {
			if endpoint, ok := normalizeLiandongSOCKS5Endpoint(parsed.Host); ok {
				password, hasPassword := parsed.User.Password()
				if !hasPassword {
					return LiandongSOCKS5ProxyConfig{}, errors.New("SOCKS5 proxy username and password must be configured together")
				}
				username, password, err := normalizeLiandongSOCKS5Credentials(
					parsed.User.Username(),
					password,
				)
				if err != nil {
					return LiandongSOCKS5ProxyConfig{}, err
				}
				return LiandongSOCKS5ProxyConfig{
					URL:      scheme + "://" + endpoint,
					Username: username,
					Password: password,
				}, nil
			}
		}
	}

	if strings.Count(raw, "@") == 1 {
		parts := strings.SplitN(raw, "@", 2)
		leftEndpoint, leftOK := normalizeLiandongSOCKS5Endpoint(parts[0])
		rightEndpoint, rightOK := normalizeLiandongSOCKS5Endpoint(parts[1])
		leftUsername, leftPassword, leftCredentialsOK := parseLiandongSOCKS5Credentials(parts[0])
		rightUsername, rightPassword, rightCredentialsOK := parseLiandongSOCKS5Credentials(parts[1])

		if leftCredentialsOK && rightOK {
			return LiandongSOCKS5ProxyConfig{
				URL:      scheme + "://" + rightEndpoint,
				Username: leftUsername,
				Password: leftPassword,
			}, nil
		}
		if leftOK && rightCredentialsOK {
			return LiandongSOCKS5ProxyConfig{
				URL:      scheme + "://" + leftEndpoint,
				Username: rightUsername,
				Password: rightPassword,
			}, nil
		}
		return LiandongSOCKS5ProxyConfig{}, errors.New("SOCKS5 proxy address or credentials are invalid")
	}

	if strings.HasPrefix(raw, "[") {
		if closeBracket := strings.IndexByte(raw, ']'); closeBracket > 0 {
			remaining := raw[closeBracket+1:]
			if strings.HasPrefix(remaining, ":") {
				fields := strings.SplitN(remaining[1:], ":", 3)
				if len(fields) == 3 {
					endpoint, endpointOK := normalizeLiandongSOCKS5Endpoint(
						raw[:closeBracket+1] + ":" + fields[0],
					)
					if endpointOK {
						username, password, err := normalizeLiandongSOCKS5Credentials(
							fields[1],
							fields[2],
						)
						if err != nil {
							return LiandongSOCKS5ProxyConfig{}, err
						}
						return LiandongSOCKS5ProxyConfig{
							URL:      scheme + "://" + endpoint,
							Username: username,
							Password: password,
						}, nil
					}
				}
			}
		}
	}

	if separator := strings.Index(raw, ":["); separator > 0 {
		username, password, credentialsOK := parseLiandongSOCKS5Credentials(raw[:separator])
		if credentialsOK {
			if endpoint, endpointOK := normalizeLiandongSOCKS5Endpoint(raw[separator+1:]); endpointOK {
				return LiandongSOCKS5ProxyConfig{
					URL:      scheme + "://" + endpoint,
					Username: username,
					Password: password,
				}, nil
			}
		}
	}

	if endpoint, ok := normalizeLiandongSOCKS5Endpoint(raw); ok {
		return LiandongSOCKS5ProxyConfig{
			URL: scheme + "://" + endpoint,
		}, nil
	}

	parts := strings.Split(raw, ":")
	if len(parts) == 4 {
		if endpoint, ok := normalizeLiandongSOCKS5Endpoint(parts[0] + ":" + parts[1]); ok {
			username, password, err := normalizeLiandongSOCKS5Credentials(parts[2], parts[3])
			if err != nil {
				return LiandongSOCKS5ProxyConfig{}, err
			}
			return LiandongSOCKS5ProxyConfig{
				URL:      scheme + "://" + endpoint,
				Username: username,
				Password: password,
			}, nil
		}
		if endpoint, ok := normalizeLiandongSOCKS5Endpoint(parts[2] + ":" + parts[3]); ok {
			username, password, err := normalizeLiandongSOCKS5Credentials(parts[0], parts[1])
			if err != nil {
				return LiandongSOCKS5ProxyConfig{}, err
			}
			return LiandongSOCKS5ProxyConfig{
				URL:      scheme + "://" + endpoint,
				Username: username,
				Password: password,
			}, nil
		}
	}

	return LiandongSOCKS5ProxyConfig{}, errors.New("SOCKS5 proxy URL must include a valid host and port")
}

func NormalizeLiandongSOCKS5ProxyURL(value string) (string, error) {
	config, err := ParseLiandongSOCKS5Proxy(value)
	if err != nil {
		return "", err
	}
	return config.URL, nil
}

func normalizeLiandongSOCKS5Endpoint(value string) (string, bool) {
	value = strings.TrimSpace(value)
	host, port, err := net.SplitHostPort(value)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return "", false
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", false
	}
	return net.JoinHostPort(host, strconv.Itoa(portNumber)), true
}

func parseLiandongSOCKS5Credentials(value string) (string, string, bool) {
	separator := strings.IndexByte(value, ':')
	if separator < 1 {
		return "", "", false
	}
	username, password, err := normalizeLiandongSOCKS5Credentials(
		value[:separator],
		value[separator+1:],
	)
	if err != nil {
		return "", "", false
	}
	return username, password, true
}

func normalizeLiandongSOCKS5Credentials(username, password string) (string, string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" || len(username) > 128 || len(password) > 256 {
		return "", "", errors.New("SOCKS5 proxy username and password must be configured together")
	}
	if strings.ContainsAny(username, "\r\n") || strings.ContainsAny(password, "\r\n") {
		return "", "", errors.New("SOCKS5 proxy credentials contain invalid characters")
	}
	return username, password, nil
}
