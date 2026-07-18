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

func TestNormalizeLiandongSOCKS5ProxyURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  string
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
			name:      "rejects missing port",
			input:     "socks5://proxy.example.com",
			wantError: true,
		},
		{
			name:      "rejects embedded credentials",
			input:     "socks5://user:password@proxy.example.com:1080",
			wantError: true,
		},
		{
			name:      "rejects HTTP proxy",
			input:     "http://proxy.example.com:8080",
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := NormalizeLiandongSOCKS5ProxyURL(test.input)
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.expected, actual)
		})
	}
}
