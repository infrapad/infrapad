package main

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBrowserSessionJourney(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("X-Forwarded-User"); got != "alice" {
			t.Errorf("X-Forwarded-User = %q, want alice", got)
		}
		if got := request.Header.Get("X-Forwarded-Email"); got != "alice@example.com" {
			t.Errorf("X-Forwarded-Email = %q, want alice@example.com", got)
		}
		if got := request.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization reached upstream: %q", got)
		}
		if _, err := request.Cookie(sessionCookieName); err != http.ErrNoCookie {
			t.Errorf("session cookie reached upstream: %v", err)
		}
		if cookie, err := request.Cookie("unrelated"); err != nil || cookie.Value != "kept" {
			t.Errorf("unrelated cookie = %v, %v; want value kept", cookie, err)
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	proxy := httptest.NewServer(newProxy(mustParseURL(t, upstream.URL)))
	defer proxy.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	response, err := client.Get(proxy.URL + "/auth")
	if err != nil {
		t.Fatalf("load login page: %v", err)
	}
	loginPage := readBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(loginPage, "Log in") {
		t.Fatalf("login page status/body = %d, %q", response.StatusCode, loginPage)
	}

	response, err = client.PostForm(proxy.URL+"/auth/login", url.Values{
		"username": {"alice"},
		"email":    {"alice@example.com"},
	})
	if err != nil {
		t.Fatalf("log in: %v", err)
	}
	authenticatedPage := readBody(t, response)
	if response.StatusCode != http.StatusOK ||
		!strings.Contains(authenticatedPage, "Current session") ||
		!strings.Contains(authenticatedPage, "alice@example.com") {
		t.Fatalf("authenticated page status/body = %d, %q", response.StatusCode, authenticatedPage)
	}

	proxyRequest, err := http.NewRequest(http.MethodGet, proxy.URL+"/api/example", nil)
	if err != nil {
		t.Fatalf("new proxy request: %v", err)
	}
	proxyRequest.AddCookie(&http.Cookie{Name: "unrelated", Value: "kept"})
	response, err = client.Do(proxyRequest)
	if err != nil {
		t.Fatalf("request proxy: %v", err)
	}
	_ = readBody(t, response)
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("proxied status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}

	response, err = client.PostForm(proxy.URL+"/auth/logout", url.Values{})
	if err != nil {
		t.Fatalf("log out: %v", err)
	}
	loggedOutPage := readBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(loggedOutPage, "Log in") || strings.Contains(loggedOutPage, "Current session") {
		t.Fatalf("logged-out page status/body = %d, %q", response.StatusCode, loggedOutPage)
	}
}

func TestLoginReturnTo(t *testing.T) {
	proxy := httptest.NewServer(newProxy(mustParseURL(t, "http://127.0.0.1:1")))
	defer proxy.Close()
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	t.Run("preserves a local target", func(t *testing.T) {
		const returnTo = "/documents/doc-1?view=history#revision-2"
		response, err := client.Get(proxy.URL + "/auth?returnTo=" + url.QueryEscape(returnTo))
		if err != nil {
			t.Fatalf("load login page: %v", err)
		}
		page := readBody(t, response)
		if !strings.Contains(page, `name="returnTo" value="`+returnTo+`"`) {
			t.Fatalf("login page did not preserve returnTo: %q", page)
		}

		response, err = client.PostForm(proxy.URL+"/auth/login", url.Values{
			"username": {"alice"},
			"email":    {""},
			"returnTo": {returnTo},
		})
		if err != nil {
			t.Fatalf("log in: %v", err)
		}
		_ = readBody(t, response)
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusSeeOther)
		}
		if got := response.Header.Get("Location"); got != "/documents/doc-1?view=history#revision-2" {
			t.Errorf("Location = %q, want local return target", got)
		}
	})

	for _, unsafe := range []string{
		"https://example.com/steal",
		"//example.com/steal",
		"/%2Fexample.com/steal",
		"/\\example.com/steal",
		"/%5Cexample.com/steal",
	} {
		t.Run("rejects "+unsafe, func(t *testing.T) {
			response, err := client.PostForm(proxy.URL+"/auth/login", url.Values{
				"username": {"alice"},
				"email":    {""},
				"returnTo": {unsafe},
			})
			if err != nil {
				t.Fatalf("log in: %v", err)
			}
			_ = readBody(t, response)
			if got := response.Header.Get("Location"); got != "/auth" {
				t.Errorf("Location = %q, want /auth", got)
			}
		})
	}
}

func TestTokenBearerJourney(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("X-Forwarded-User"); got != "alice" {
			t.Errorf("X-Forwarded-User = %q, want alice", got)
		}
		if got := request.Header.Get("X-Forwarded-Email"); got != "alice@example.com" {
			t.Errorf("X-Forwarded-Email = %q, want alice@example.com", got)
		}
		if got := request.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization reached upstream: %q", got)
		}
		response.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(response, "upstream response")
	}))
	defer upstream.Close()

	proxy := httptest.NewServer(newProxy(mustParseURL(t, upstream.URL)))
	defer proxy.Close()
	response, err := http.PostForm(proxy.URL+"/auth/token", url.Values{
		"username": {"alice"},
		"email":    {"alice@example.com"},
	})
	if err != nil {
		t.Fatalf("request token: %v", err)
	}
	tokenBody := readBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if len(response.Cookies()) != 0 {
		t.Fatalf("token response created cookies: %v", response.Cookies())
	}
	token := strings.TrimSuffix(tokenBody, "\n")
	if token == tokenBody || token == "" {
		t.Fatalf("token response = %q, want a token followed by one newline", tokenBody)
	}

	request, err := http.NewRequest(http.MethodGet, proxy.URL+"/api/example", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Forwarded-User", "mallory")
	request.Header.Set("X-Forwarded-Email", "mallory@example.com")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request proxy: %v", err)
	}
	body := readBody(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	if body != "upstream response" {
		t.Errorf("body = %q, want upstream response", body)
	}
}

func TestBrowserAPICORS(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		upstreamRequests.Add(1)
		if request.URL.Path == "/v1/documents" {
			if got := request.Header.Get("X-Forwarded-User"); got != "alice" {
				t.Errorf("upstream identity = %q, want alice", got)
			}
			if got := request.Header.Get("Authorization"); got != "" {
				t.Errorf("Authorization reached upstream: %q", got)
			}
			if got := request.Header.Get("X-Forwarded-Email"); got != "" {
				t.Errorf("client identity reached upstream: %q", got)
			}
		}
		if browserAPIPath(request.URL.Path) {
			response.Header().Add("Access-Control-Allow-Origin", "https://other.example")
			response.Header().Add("Access-Control-Allow-Origin", "*")
			response.Header().Set("Access-Control-Allow-Credentials", "true")
			response.Header().Set("Access-Control-Allow-Headers", "X-Other")
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(newProxy(mustParseURL(t, upstream.URL)))
	defer proxy.Close()

	request := func(method, path string, headers http.Header) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, proxy.URL+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header = headers
		result, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request proxy: %v", err)
		}
		_ = readBody(t, result)
		return result
	}
	checkCORS := func(result *http.Response) {
		t.Helper()
		if got := result.Header.Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "*" {
			t.Errorf("allow origins = %q, want single wildcard", got)
		}
		if got := result.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
			t.Errorf("allowed headers = %q, missing Authorization", got)
		}
		if got := result.Header.Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("credential allowance = %q, want none", got)
		}
	}

	preflight := http.Header{
		"Origin":                         {"https://fleetshift-sandbox.localhost:8085"},
		"Access-Control-Request-Method":  {"GET"},
		"Access-Control-Request-Headers": {"authorization"},
	}
	for _, path := range []string{"/ui/config", "/v1", "/v1/documents"} {
		result := request(http.MethodOptions, path, preflight)
		if result.StatusCode != http.StatusNoContent {
			t.Errorf("%s preflight status = %d, want 204", path, result.StatusCode)
		}
		checkCORS(result)
	}
	if got := upstreamRequests.Load(); got != 0 {
		t.Fatalf("preflight reached upstream %d times", got)
	}

	token, err := generateToken("alice", "", false)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	result := request(http.MethodGet, "/v1/documents", http.Header{
		"Authorization":     {"Bearer " + token},
		"X-Forwarded-User":  {"mallory"},
		"X-Forwarded-Email": {"mallory@example.com"},
	})
	if result.StatusCode != http.StatusOK {
		t.Errorf("bearer status = %d, want 200", result.StatusCode)
	}
	checkCORS(result)
	if got := upstreamRequests.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want 1", got)
	}

	for _, headers := range []http.Header{
		{"Authorization": {"Bearer not-a-jwt"}},
		{"Authorization": {"Bearer " + token, "Bearer " + token}},
	} {
		result = request(http.MethodGet, "/ui/config", headers)
		if result.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid bearer status = %d, want 400", result.StatusCode)
		}
		checkCORS(result)
	}
	if got := upstreamRequests.Load(); got != 1 {
		t.Errorf("invalid bearer reached upstream: %d total requests", got)
	}

	// Neither the auth page nor similarly prefixed upstream paths get proxy-owned CORS.
	for _, path := range []string{"/auth", "/v1ish", "/ui/configish"} {
		result = request(http.MethodGet, path, nil)
		if got := result.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s allow origin = %q, want none", path, got)
		}
	}
}

func TestBrowserAPIUpstreamErrorCORS(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstreamURL := mustParseURL(t, upstream.URL)
	upstream.Close()
	proxy := httptest.NewServer(newProxy(upstreamURL))
	defer proxy.Close()

	response, err := http.Get(proxy.URL + "/ui/config")
	if err != nil {
		t.Fatalf("request proxy: %v", err)
	}
	_ = readBody(t, response)
	if response.StatusCode != http.StatusBadGateway || response.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("upstream error status/CORS = %d, %q; want 502, *", response.StatusCode, response.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestProxyAdjacentBehavior(t *testing.T) {
	t.Run("anonymous request strips client identity", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if got := request.Header.Get("X-Forwarded-User"); got != "" {
				t.Errorf("X-Forwarded-User reached upstream: %q", got)
			}
			if got := request.Header.Get("X-Forwarded-Email"); got != "" {
				t.Errorf("X-Forwarded-Email reached upstream: %q", got)
			}
			response.WriteHeader(http.StatusNoContent)
		}))
		defer upstream.Close()

		proxy := httptest.NewServer(newProxy(mustParseURL(t, upstream.URL)))
		defer proxy.Close()
		request, err := http.NewRequest(http.MethodGet, proxy.URL+"/", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		request.Header.Set("X-Forwarded-User", "mallory")
		request.Header.Set("X-Forwarded-Email", "mallory@example.com")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("request proxy: %v", err)
		}
		_ = readBody(t, response)
		if response.StatusCode != http.StatusNoContent {
			t.Errorf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
		}
	})

	t.Run("malformed token does not reach upstream", func(t *testing.T) {
		var upstreamRequests atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			upstreamRequests.Add(1)
			response.WriteHeader(http.StatusNoContent)
		}))
		defer upstream.Close()

		proxy := httptest.NewServer(newProxy(mustParseURL(t, upstream.URL)))
		defer proxy.Close()
		request, err := http.NewRequest(http.MethodGet, proxy.URL+"/", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		request.Header.Set("Authorization", "Bearer not-a-jwt")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("request proxy: %v", err)
		}
		_ = readBody(t, response)
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
		}
		if got := upstreamRequests.Load(); got != 0 {
			t.Errorf("upstream received %d requests, want 0", got)
		}
	})

	t.Run("health succeeds without upstream", func(t *testing.T) {
		proxy := httptest.NewServer(newProxy(mustParseURL(t, "http://127.0.0.1:1")))
		defer proxy.Close()
		response, err := http.Get(proxy.URL + "/oauth/healthz")
		if err != nil {
			t.Fatalf("request health endpoint: %v", err)
		}
		_ = readBody(t, response)
		if response.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", response.StatusCode, http.StatusOK)
		}
	})
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return string(body)
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	return parsed
}
