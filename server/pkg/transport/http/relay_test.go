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
	"os"
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

// TestRelayEndToEnd verifies bearer forwarding, path/query mapping with a
// base prefix, and that browser cookies/identity headers are not forwarded.
func TestRelayEndToEnd(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotCookie, gotForwardedUser string
	upstream, caPEM := testUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		gotForwardedUser = r.Header.Get("X-Forwarded-User")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	}))

	relay := newTestRelay(t, upstream.URL+"/thanos", caPEM)

	req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query_range?query=up&match[]=a&match[]=b", nil)
	req.Header.Set("Authorization", "Bearer my-token")
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
	if gotAuth != "Bearer my-token" {
		t.Errorf("upstream auth = %q", gotAuth)
	}
	if gotCookie != "" {
		t.Errorf("cookies forwarded: %q", gotCookie)
	}
	if gotForwardedUser != "" {
		t.Errorf("X-Forwarded-User forwarded: %q", gotForwardedUser)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("missing no-store")
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
	req.Header.Set("Authorization", "Bearer tok")
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
		req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		relay.ServeHTTP(rec, req)
		if rec.Code != 502 {
			t.Errorf("status = %d, want 502", rec.Code)
		}
	})

	t.Run("with CA", func(t *testing.T) {
		relay := newTestRelay(t, upstream.URL, caPEM)
		req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		relay.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})
}
