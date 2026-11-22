package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	_ "golang.org/x/image/webp"
	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

// A data-URL iframe cannot inherit the server's proxy or CookieJar. Embed the
// QR image instead of returning HTML whose scripts and resources bypass them.
// Countdown and settlement polling remain owned by the authenticated wallet.
func (c *liandongClient) embedPaymentQRCode(ctx context.Context, page *LiandongPaymentPage) (embedded *LiandongPaymentPage, embedErr error) {
	started := time.Now()
	defer func() {
		if embedErr != nil {
			recordLiandongUpstreamCall(ctx, http.MethodGet, liandongPaymentPath, 0, time.Since(started), false, sanitizeLiandongDiagnostic(embedErr.Error()), nil, nil)
		}
	}()
	if page == nil {
		return nil, errors.New("payment page returned no response")
	}
	documentURL, err := url.Parse(page.documentURL)
	if err != nil || documentURL.Host == "" {
		documentURL, err = url.Parse(c.baseURL + liandongPaymentPath)
		if err != nil {
			return nil, errors.New("payment page URL is invalid")
		}
	}
	client := *c.httpClient
	providerSession := true
	if page.RedirectURL != "" {
		documentURL, err = url.Parse(page.RedirectURL)
		if err != nil {
			return nil, errors.New("payment page returned an invalid redirect")
		}
		origin, parseErr := url.Parse(c.baseURL)
		if parseErr != nil {
			return nil, errors.New("payment page URL is invalid")
		}
		providerSession = sameLiandongPaymentOrigin(origin, documentURL)
		if !providerSession {
			// Never send the merchant session to an external payment processor.
			transport, ok := c.httpClient.Transport.(*http.Transport)
			if !ok {
				return nil, errors.New("payment redirect transport is unavailable")
			}
			client.Transport = &liandongPublicPaymentTransport{transport: transport}
			client.Jar, err = cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
			if err != nil {
				return nil, err
			}
		}
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !sameLiandongPaymentOrigin(documentURL, req.URL) || req.URL.User != nil {
			return errors.New("payment resource redirect target is not allowed")
		}
		return nil
	}
	if page.RedirectURL != "" {
		resp, body, requestErr := c.fetchPaymentResource(ctx, &client, documentURL, providerSession)
		if requestErr != nil {
			return nil, requestErr
		}
		if err := validateLiandongPaymentPageResponse(resp, body); err != nil {
			return nil, err
		}
		page = &LiandongPaymentPage{HTML: string(body)}
		documentURL = resp.Request.URL
	}

	source, err := liandongPaymentQRCodeSource(page.HTML)
	if err != nil {
		return nil, err
	}
	var imageBytes []byte
	if strings.HasPrefix(source, "data:") {
		metadata, encoded, ok := strings.Cut(source, ",")
		if !ok || !strings.HasSuffix(metadata, ";base64") || len(encoded) > base64.StdEncoding.EncodedLen(liandongMaxBodyBytes) {
			return nil, errors.New("payment QR image is invalid")
		}
		imageBytes, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, errors.New("payment QR image is invalid")
		}
	} else {
		reference, parseErr := url.Parse(source)
		if parseErr != nil {
			return nil, errors.New("payment QR image URL is invalid")
		}
		imageURL := documentURL.ResolveReference(reference)
		if imageURL.User != nil || !sameLiandongPaymentOrigin(documentURL, imageURL) {
			return nil, errors.New("payment QR image origin is not allowed")
		}
		_, imageBytes, err = c.fetchPaymentResource(ctx, &client, imageURL, providerSession)
	}
	if err != nil {
		return nil, err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil || !slices.Contains([]string{"png", "jpeg", "gif", "webp"}, format) ||
		config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || config.Width*config.Height > 4<<20 {
		return nil, errors.New("payment QR image is invalid")
	}
	decoded, _, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, errors.New("payment QR image is invalid")
	}
	// Re-encode only pixels, not upstream metadata or trailing response data.
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, decoded); err != nil {
		return nil, errors.New("payment QR image is invalid")
	}
	imageData := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pixels.Bytes())
	return &LiandongPaymentPage{HTML: `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><style>html,body{margin:0;min-height:100%;background:#fff}body{display:grid;place-items:center;min-height:400px}img{display:block;width:288px;max-width:85vw;height:auto;image-rendering:pixelated}</style></head><body><img alt="" src="` + imageData + `"></body></html>`}, nil
}

func sameLiandongPaymentOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

// Only a QR image identified by the checkout markup is accepted. Do not guess
// from arbitrary images (logos or expired-code placeholders can be adjacent).
func liandongPaymentQRCodeSource(document string) (string, error) {
	root, err := html.Parse(strings.NewReader(document))
	if err != nil {
		return "", errors.New("payment page HTML is invalid")
	}
	var images []string
	stack := []*html.Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for child := node.LastChild; child != nil; child = child.PrevSibling {
			stack = append(stack, child)
		}
		if node.Type != html.ElementNode || node.Data != "img" {
			continue
		}
		var source, identifier string
		for _, attr := range node.Attr {
			switch attr.Key {
			case "src":
				source = strings.TrimSpace(attr.Val)
			case "class", "id":
				identifier += " " + strings.ToLower(attr.Val)
			}
		}
		qrImage := strings.Contains(strings.ToLower(source), "/generateqrcode")
		for _, name := range strings.Fields(identifier) {
			qrImage = qrImage || slices.Contains([]string{"code", "qrcode", "qr-code", "qr_code", "qrimage"}, name)
		}
		if qrImage && source != "" && !slices.Contains(images, source) {
			images = append(images, source)
		}
	}
	if len(images) != 1 {
		return "", errors.New("payment page does not contain a unique embeddable QR image")
	}
	return images[0], nil
}

func (c *liandongClient) fetchPaymentResource(ctx context.Context, client *http.Client, target *url.URL, providerSession bool) (response *http.Response, body []byte, requestErr error) {
	for attempt := range 2 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, nil, errors.New("payment resource URL is invalid")
		}
		response, err = client.Do(req)
		if err != nil {
			// url.Error contains the upstream query; unwrap without exposing it.
			var urlErr *url.Error
			if errors.As(err, &urlErr) {
				err = urlErr.Err
			}
			return response, nil, fmt.Errorf("payment resource request failed: %w", err)
		}
		body, err = io.ReadAll(io.LimitReader(response.Body, liandongMaxBodyBytes+1))
		response.Body.Close()
		if err != nil {
			return response, nil, errors.New("payment resource could not be read")
		}
		if len(body) > liandongMaxBodyBytes {
			return response, nil, errors.New("payment resource response is too large")
		}
		if providerSession && attempt == 0 && isLiandongBrowserVerificationPage(body) && c.applyLiandongBrowserVerificationCookie(target.String(), body) {
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return response, nil, fmt.Errorf("payment resource returned HTTP %d", response.StatusCode)
		}
		return response, body, nil
	}
	return nil, nil, errors.New("payment resource browser verification failed")
}

// External processor URLs are supplied only by the authenticated provider,
// never by a browser. Pin a validated public IP even when dialing via SOCKS or
// HTTP CONNECT, so proxy-side DNS cannot redirect the request to a private host.
type liandongPublicPaymentTransport struct {
	transport *http.Transport
	resolver  ssrfResolver
}

func (t *liandongPublicPaymentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target := req.URL
	if target.Scheme != "https" || target.User != nil || target.Hostname() == "" ||
		(target.Port() != "" && target.Port() != "443") {
		return nil, errors.New("payment redirect target is not allowed")
	}
	resolver := t.resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupIPAddr(req.Context(), target.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("payment redirect host could not be resolved")
	}
	protection := common.SSRFProtection{}
	for _, address := range addresses {
		if !protection.IsIPAccessAllowed(address.IP) {
			return nil, errors.New("payment redirect target is not a public host")
		}
	}
	transport := t.transport.Clone()
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	if transport.Proxy != nil {
		proxyURL, err := transport.Proxy(req)
		if err != nil {
			return nil, errors.New("payment proxy configuration is invalid")
		}
		if proxyURL != nil && proxyURL.Scheme == "https" {
			// An HTTPS proxy has its own TLS peer before CONNECT. The pinned
			// processor's SNI must not be used for that first TLS handshake.
			proxyTLS := transport.TLSClientConfig.Clone()
			proxyTLS.ServerName = proxyURL.Hostname()
			proxyTLS.NextProtos = []string{"http/1.1"}
			transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if transport.TLSHandshakeTimeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, transport.TLSHandshakeTimeout)
					defer cancel()
				}
				dial := transport.DialContext
				if dial == nil {
					dial = (&net.Dialer{}).DialContext
				}
				conn, err := dial(ctx, network, address)
				if err != nil {
					return nil, err
				}
				secure := tls.Client(conn, proxyTLS)
				if err := secure.HandshakeContext(ctx); err != nil {
					conn.Close()
					return nil, err
				}
				return secure, nil
			}
		}
	}
	transport.TLSClientConfig.ServerName = target.Hostname()
	pinned := req.Clone(req.Context())
	pinned.URL.Host = net.JoinHostPort(addresses[0].IP.String(), "443")
	pinned.Host = target.Host
	response, err := transport.RoundTrip(pinned)
	if response != nil {
		response.Request = req
	}
	return response, err
}
