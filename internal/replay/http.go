package replay

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// netError carries a pr-audit-specific exit code alongside the original
// network failure. The replay command's RunE inspects errors via
// errors.As to map DNS / TLS / timeout to exit 31 / 32 / 33.
type netError struct {
	Code int
	Msg  string
}

func (e *netError) Error() string { return e.Msg }

// classifyNetError converts a transport-level error into a netError when
// the underlying cause matches one of the categories pr-audit tracks.
// Anything we don't recognise is returned unchanged (callers degrade L3).
func classifyNetError(err error) error {
	if err == nil {
		return nil
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &netError{Code: 31, Msg: "DNS resolution failed: " + dnsErr.Error()}
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return &netError{Code: 32, Msg: "TLS certificate verification failed: " + certErr.Error()}
	}
	// Generic "x509: ..." TLS errors that aren't wrapped in
	// CertificateVerificationError — common on self-signed/MITM proxies.
	if strings.Contains(err.Error(), "x509:") || strings.Contains(err.Error(), "tls:") {
		return &netError{Code: 32, Msg: "TLS handshake failed: " + err.Error()}
	}
	if isTimeout(err) {
		return &netError{Code: 33, Msg: "upstream timeout: " + err.Error()}
	}
	return err
}

func isTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// http.Client wraps deadline exceeded as url.Error → net.OpError;
	// fall back to substring as a belt-and-braces check.
	return strings.Contains(strings.ToLower(err.Error()), "timeout") ||
		strings.Contains(strings.ToLower(err.Error()), "deadline exceeded")
}

// httpClient returns the shared client used by all upstream-vendor calls.
// 30s is generous for count_tokens which is typically <500ms.
//
// httpDoer is overridable in tests via the package-level var below.
var httpDoer = func() httpClientLike {
	return &http.Client{Timeout: 30 * time.Second}
}

// httpClientLike is the minimal interface we depend on, so tests can
// inject an httptest.Server-backed client without going through the
// real network.
type httpClientLike interface {
	Do(*http.Request) (*http.Response, error)
}

// readError extracts a short human-readable message from a non-2xx HTTP
// response body. We cap at 256 bytes to avoid printing pages of HTML.
func readError(resp *http.Response, vendor string) error {
	const limit = 256
	buf := make([]byte, limit)
	n, _ := resp.Body.Read(buf)
	body := strings.TrimSpace(string(buf[:n]))
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%s authentication failed (HTTP %d) — check vendor-key: %s", vendor, resp.StatusCode, body)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%s rate-limited (HTTP 429): %s", vendor, body)
	}
	if resp.StatusCode >= 500 {
		return &netError{Code: 33, Msg: fmt.Sprintf("%s upstream error HTTP %d: %s", vendor, resp.StatusCode, body)}
	}
	return fmt.Errorf("%s unexpected status HTTP %d: %s", vendor, resp.StatusCode, body)
}
