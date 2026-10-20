package http

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// testUpstream creates an HTTPS test server with a self-signed CA.
// Returns the server and PEM CA bytes.
func testUpstream(t *testing.T, handler http.HandlerFunc) (*httptest.Server, []byte) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	srvKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, _ := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)

	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{srvDER},
			PrivateKey:  srvKey,
		}},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, caPEM
}

func writeCAFile(t *testing.T, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "ca-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(data)
	f.Close()
	return f.Name()
}

func newTestRelay(t *testing.T, base string, caPEM []byte) *Relay {
	t.Helper()
	relay, err := NewRelay(RelayConfig{UpstreamBase: base, CAFile: writeCAFile(t, caPEM)})
	if err != nil {
		t.Fatal(err)
	}
	return relay
}

// TestRelayEndToEnd verifies native-token to upstream bearer conversion,
// path/query mapping, and that cookies/forwarded headers are not passed on.
func TestRelayEndToEnd(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotCookie, gotForwardedUser, gotForwardedToken string
	upstream, caPEM := testUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		gotForwardedUser = r.Header.Get("X-Forwarded-User")
		gotForwardedToken = r.Header.Get("X-Forwarded-Access-Token")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	}))

	relay := newTestRelay(t, upstream.URL+"/thanos", caPEM)

	req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query_range?query=up&match[]=a&match[]=b", nil)
	// Token contents are opaque, not restricted to a local token alphabet.
	req.Header.Set("X-Forwarded-Access-Token", "opaque:token=with,punctuation")
	// The native header alone controls the upstream credential, even when the
	// caller supplies a competing Authorization header.
	req.Header.Set("Authorization", "Bearer competing-token")
	req.Header.Set("Cookie", "session=secret")
	req.Header.Set("X-Forwarded-User", "should-not-forward")
	rec := httptest.NewRecorder()
	relay.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/thanos/api/v1/query_range" {
		t.Errorf("upstream path = %q, want /thanos/api/v1/query_range", gotPath)
	}
	if gotQuery != "query=up&match[]=a&match[]=b" {
		t.Errorf("upstream query = %q", gotQuery)
	}
	if gotAuth != "Bearer opaque:token=with,punctuation" {
		t.Errorf("upstream auth = %q", gotAuth)
	}
	if gotCookie != "" {
		t.Errorf("cookies forwarded: %q", gotCookie)
	}
	if gotForwardedUser != "" || gotForwardedToken != "" {
		t.Errorf("forwarded headers leaked upstream: user=%q token=%q", gotForwardedUser, gotForwardedToken)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("missing no-store")
	}
}

func TestRelayUpstreamErrorResponses(t *testing.T) {
	for _, tc := range []struct {
		status      int
		contentType string
		body        string
	}{
		{http.StatusBadRequest, "application/json", `{"status":"error","errorType":"bad_data","error":"invalid query"}`},
		{http.StatusForbidden, "text/plain", "monitoring access denied"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			upstream, caPEM := testUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.Header().Set("Set-Cookie", "upstream-session=private")
				w.Header().Set("Location", "/login")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			relay := newTestRelay(t, upstream.URL, caPEM)
			req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
			req.Header.Set("X-Forwarded-Access-Token", "tok")
			rec := httptest.NewRecorder()
			relay.ServeHTTP(rec, req)

			if rec.Code != tc.status || rec.Body.String() != tc.body || rec.Header().Get("Content-Type") != tc.contentType {
				t.Fatalf("status=%d content-type=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("Location") != "" {
				t.Fatal("upstream headers leaked or response is cacheable")
			}
		})
	}
}

func TestUpstreamErrorDetail(t *testing.T) {
	err := &url.Error{
		Op:  "Get",
		URL: "https://upstream.example/api/v1/query?query=private-query",
		Err: fmt.Errorf("connection failed for secret-token"),
	}
	if got := upstreamErrorDetail(err, "secret-token"); got != "connection failed for [redacted]" {
		t.Fatalf("detail = %q", got)
	}
}

// Missing, empty, or multiple X-Forwarded-Access-Token values fail before
// upstream access, even when an incoming Authorization header is present.
func TestRelayMissingOrAmbiguousCredentials(t *testing.T) {
	upstream, caPEM := testUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid credentials reached upstream")
		w.WriteHeader(http.StatusOK)
	}))
	relay := newTestRelay(t, upstream.URL, caPEM)
	for _, tc := range []struct {
		name   string
		values []string
	}{
		{"missing", nil},
		{"empty", []string{""}},
		{"multiple", []string{"secret-sentinel", "other-token"}},
		{"duplicate identical", []string{"secret-sentinel", "secret-sentinel"}},
		{"empty duplicate", []string{"secret-sentinel", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
			for _, value := range tc.values {
				req.Header.Add("X-Forwarded-Access-Token", value)
			}
			req.Header.Set("Authorization", "Bearer authorization-sentinel")
			rec := httptest.NewRecorder()
			relay.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d cache=%q, want non-cacheable 401", rec.Code, rec.Header().Get("Cache-Control"))
			}
			if strings.Contains(rec.Body.String(), "sentinel") {
				t.Fatal("credential echoed in error")
			}
		})
	}
}

// TestRelayTraversalHandledByServeMux verifies that http.ServeMux cleans
// ".." paths before routing, so the relay handler never sees traversal
// attempts — no explicit check in the relay is needed.
func TestRelayTraversalHandledByServeMux(t *testing.T) {
	var called bool
	upstream, caPEM := testUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	relay := newTestRelay(t, upstream.URL+"/thanos", caPEM)

	mux := http.NewServeMux()
	mux.Handle("/prometheus/api/", relay)

	req := httptest.NewRequest(http.MethodGet, "/prometheus/api/../../secret", nil)
	req.Header.Set("X-Forwarded-Access-Token", "tok")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// ServeMux cleans the path to "/secret" which doesn't match
	// "/prometheus/api/", so the relay is never called.
	if called {
		t.Fatal("relay handler was called with traversal path")
	}
}

// TestRelayTLSWithCA verifies the relay uses the supplied CA and rejects
// untrusted certs by default.
func TestRelayTLSWithCA(t *testing.T) {
	upstream, caPEM := testUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"success"}`)
	}))

	t.Run("untrusted", func(t *testing.T) {
		relay, err := NewRelay(RelayConfig{UpstreamBase: upstream.URL})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query?query=private-query", nil)
		req.Header.Set("X-Forwarded-Access-Token", "secret-token")
		rec := httptest.NewRecorder()
		relay.ServeHTTP(rec, req)
		if rec.Code != 502 {
			t.Errorf("status = %d, want 502", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "x509: certificate signed by unknown authority") {
			t.Errorf("missing TLS diagnostic: %s", body)
		}
		if strings.Contains(body, upstream.URL) || strings.Contains(body, "private-query") || strings.Contains(body, "secret-token") {
			t.Fatal("request URL or credential leaked in error")
		}
	})

	t.Run("with CA", func(t *testing.T) {
		relay := newTestRelay(t, upstream.URL, caPEM)
		req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
		req.Header.Set("X-Forwarded-Access-Token", "tok")
		rec := httptest.NewRecorder()
		relay.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})
}
