package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeLiandongBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  string
		wantError bool
	}{
		{
			name:     "default",
			expected: DefaultLiandongBaseURL,
		},
		{
			name:     "custom origin and path",
			input:    "https://gateway.example.com/card/",
			expected: "https://gateway.example.com/card",
		},
		{
			name:      "rejects HTTP",
			input:     "http://gateway.example.com",
			wantError: true,
		},
		{
			name:      "rejects credentials",
			input:     "https://user:password@gateway.example.com",
			wantError: true,
		},
		{
			name:      "rejects query",
			input:     "https://gateway.example.com?token=secret",
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := NormalizeLiandongBaseURL(test.input)
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.expected, actual)
		})
	}
}

func TestParseLiandongProxy(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  string
		username  string
		password  string
		wantError bool
	}{
		{
			name:     "socks5h",
			input:    "socks5h://127.0.0.1:1080/",
			expected: "socks5h://127.0.0.1:1080",
		},
		{
			name:     "IPv6",
			input:    "socks5://[::1]:1080",
			expected: "socks5://[::1]:1080",
		},
		{
			name:     "HTTP",
			input:    "http://127.0.0.1:7890/",
			expected: "http://127.0.0.1:7890",
		},
		{
			name:     "HTTP with embedded credentials",
			input:    "http://user:password@proxy.example.com:8080",
			expected: "http://proxy.example.com:8080",
			username: "user",
			password: "password",
		},
		{
			name:     "HTTPS with embedded credentials",
			input:    "https://user:password@proxy.example.com:8443",
			expected: "https://proxy.example.com:8443",
			username: "user",
			password: "password",
		},
		{
			name:      "rejects missing port",
			input:     "socks5://proxy.example.com",
			wantError: true,
		},
		{
			name:     "scheme with embedded credentials",
			input:    "socks5://user:password@proxy.example.com:1080",
			expected: "socks5://proxy.example.com:1080",
			username: "user",
			password: "password",
		},
		{
			name:     "endpoint then credentials",
			input:    "socks5://127.0.0.1:10808:user:password",
			expected: "socks5://127.0.0.1:10808",
			username: "user",
			password: "password",
		},
		{
			name:     "credentials then endpoint",
			input:    "user:password:127.0.0.1:10808",
			expected: "socks5h://127.0.0.1:10808",
			username: "user",
			password: "password",
		},
		{
			name:     "credentials at endpoint",
			input:    "user:password@127.0.0.1:10808",
			expected: "socks5h://127.0.0.1:10808",
			username: "user",
			password: "password",
		},
		{
			name:     "endpoint at credentials",
			input:    "127.0.0.1:10808@user:password",
			expected: "socks5h://127.0.0.1:10808",
			username: "user",
			password: "password",
		},
		{
			name:     "IPv6 with credentials",
			input:    "socks5://[::1]:1080:user:password",
			expected: "socks5://[::1]:1080",
			username: "user",
			password: "password",
		},
		{
			name:      "rejects incomplete credentials",
			input:     "socks5://127.0.0.1:10808:user",
			wantError: true,
		},
		{
			name:      "rejects unsupported scheme",
			input:     "ftp://proxy.example.com:21",
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := ParseLiandongProxy(test.input)
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.expected, actual.URL)
			assert.Equal(t, test.username, actual.Username)
			assert.Equal(t, test.password, actual.Password)
		})
	}
}
