// relay.go implements an opt-in authenticated metrics relay that forwards
// GET requests under /prometheus/api/ to a configured upstream
// Prometheus-compatible endpoint (e.g. OpenShift Thanos Querier).
//
// The relay accepts a single raw X-Forwarded-Access-Token from the authenticated
// OAuth proxy, constructs a fresh upstream Bearer request, and returns the
// buffered response. It never follows redirects, forwards cookies or identity
// headers. Upstream response bodies are preserved, including errors.
// When disabled (no upstream URL),
// the existing browser-direct Prometheus mode is unchanged.
package http

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Relay constants – used directly, not exposed as operator settings.
// Tests may use internal helpers with smaller values.
const (
	relayTimeout         = 30 * time.Second
	relayMaxResponseSize = 16 << 20 // 16 MiB
)

// RelayConfig holds validated configuration for the metrics relay.
type RelayConfig struct {
	// UpstreamBase is the validated upstream Prometheus-compatible base URL
	// (scheme + host + optional path prefix, no trailing slash).
	UpstreamBase string

	// CAFile is the path to an additional PEM CA bundle for upstream TLS.
	// Empty means system roots only.
	CAFile string

	// InsecureSkipTLSVerify disables upstream certificate verification.
	InsecureSkipTLSVerify bool
}

// Relay is the metrics relay handler. Zero value is not usable; use NewRelay.
type Relay struct {
	upstreamBase string
	client       *http.Client
}

// NewRelay creates a relay from validated configuration.
// It returns an error if CA material is unreadable/invalid or the upstream URL
// is malformed.
func NewRelay(cfg RelayConfig) (*Relay, error) {
	if cfg.UpstreamBase == "" {
		return nil, fmt.Errorf("metrics relay: upstream base URL is required")
	}

	u, err := validateUpstreamURL(cfg.UpstreamBase)
	if err != nil {
		return nil, fmt.Errorf("metrics relay: %w", err)
	}

	tlsCfg := &tls.Config{}

	// Load CA bundle if provided – always validate, even if insecure is set.
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("metrics relay: reading CA file %q: %w", cfg.CAFile, err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("metrics relay: CA file %q contains no valid PEM certificates", cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}

	if cfg.InsecureSkipTLSVerify {
		log.Print("WARNING: metrics relay upstream TLS verification is disabled – this bypasses certificate trust for the configured upstream only")
		tlsCfg.InsecureSkipVerify = true
	}

	transport := &http.Transport{
		TLSClientConfig: tlsCfg,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   relayTimeout,
		// Never follow redirects – treat them as errors.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &Relay{
		upstreamBase: u,
		client:       client,
	}, nil
}

// validateUpstreamURL validates and normalizes the upstream base URL.
func validateUpstreamURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid upstream URL: %w", err)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("upstream URL must use https scheme, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("upstream URL must have a host")
	}
	if u.User != nil {
		return "", fmt.Errorf("upstream URL must not contain credentials")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("upstream URL must not contain query parameters")
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("upstream URL must not contain a fragment")
	}
	// Normalize: strip trailing slash from path.
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

// ServeHTTP handles requests under /prometheus/api/.
func (rl *Relay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	// Only GET is allowed; explicitly reject HEAD and others.
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeRelayError(w, http.StatusMethodNotAllowed, "only GET is supported")
		return
	}

	// Consume only the authenticated proxy's native credential header.
	token, err := extractForwardedAccessToken(r)
	if err != nil {
		writeRelayError(w, http.StatusUnauthorized, err.Error())
		return
	}

	// Build upstream URL from the API suffix.
	suffix := strings.TrimPrefix(r.URL.Path, "/prometheus")
	if !strings.HasPrefix(suffix, "/api/") {
		writeRelayError(w, http.StatusNotFound, "not found")
		return
	}

	upstreamURL := rl.upstreamBase + suffix
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	// Build upstream request.
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		writeRelayError(w, http.StatusBadGateway, "failed to construct upstream request")
		return
	}
	upReq.Header.Set("Authorization", "Bearer "+token)
	upReq.Header.Set("Accept", "application/json")

	resp, err := rl.client.Do(upReq)
	if err != nil {
		if r.Context().Err() != nil {
			// Client disconnected.
			return
		}
		// Distinguish timeout from other errors.
		if isTimeout(err) {
			writeRelayError(w, http.StatusGatewayTimeout, "upstream request timed out: "+upstreamErrorDetail(err, token))
			return
		}
		writeRelayError(w, http.StatusBadGateway, "upstream request failed: "+upstreamErrorDetail(err, token))
		return
	}
	defer resp.Body.Close()

	// Redirects are treated as errors (CheckRedirect returns ErrUseLastResponse,
	// so we get the redirect response directly).
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		writeRelayError(w, http.StatusBadGateway, "upstream returned a redirect")
		return
	}

	// Buffer the response body with size limit.
	body, err := readLimited(resp.Body, relayMaxResponseSize)
	if err != nil {
		if isTimeout(err) {
			writeRelayError(w, http.StatusGatewayTimeout, "upstream response read timed out: "+upstreamErrorDetail(err, token))
			return
		}
		writeRelayError(w, http.StatusBadGateway, "upstream response too large or unreadable: "+upstreamErrorDetail(err, token))
		return
	}

	// Preserve upstream status and body, including error responses.
	ct := resp.Header.Get("Content-Type")
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// upstreamErrorDetail omits the request URL/query and redacts the bearer token.
func upstreamErrorDetail(err error, token string) string {
	if urlErr, ok := err.(*url.Error); ok {
		err = urlErr.Err
	}
	return strings.ReplaceAll(err.Error(), token, "[redacted]")
}

// extractForwardedAccessToken accepts one nonempty, opaque token from the
// authenticating proxy. Token validation belongs to the proxy and upstream;
// direct Go access remains a trusted-development bypass.
func extractForwardedAccessToken(r *http.Request) (string, error) {
	values := r.Header.Values("X-Forwarded-Access-Token")
	if len(values) != 1 || values[0] == "" {
		return "", fmt.Errorf("expected one nonempty forwarded access token")
	}
	return values[0], nil
}

// readLimited reads up to limit+1 bytes. If the body exceeds limit, it returns an error.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	lr := io.LimitReader(r, limit+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds size limit")
	}
	return data, nil
}

// relayErrorBody is the JSON error shape returned by the relay.
type relayErrorBody struct {
	Error  string `json:"error"`
	Status int    `json:"status"`
}

func writeRelayError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(relayErrorBody{Error: msg, Status: status})
}

// isTimeout checks if an error is a timeout.
func isTimeout(err error) bool {
	type timeouter interface {
		Timeout() bool
	}
	if t, ok := err.(timeouter); ok {
		return t.Timeout()
	}
	return false
}
