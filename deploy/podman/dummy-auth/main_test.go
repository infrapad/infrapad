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
