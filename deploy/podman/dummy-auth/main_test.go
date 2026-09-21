package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestProxy(t *testing.T) {
	t.Run("forwards derived identity and upstream response", func(t *testing.T) {
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

		token, err := generateToken("alice", "alice@example.com", true)
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		proxy := httptest.NewServer(newProxy(mustParseURL(t, upstream.URL)))
		defer proxy.Close()

		request, err := http.NewRequest(http.MethodGet, proxy.URL+"/api/example", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-Forwarded-User", "mallory")
		request.Header.Set("X-Forwarded-Email", "mallory@example.com")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("request proxy: %v", err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		if response.StatusCode != http.StatusCreated {
			t.Errorf("status = %d, want %d", response.StatusCode, http.StatusCreated)
		}
		if got := string(body); got != "upstream response" {
			t.Errorf("body = %q, want upstream response", got)
		}
	})

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
		defer response.Body.Close()
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
		defer response.Body.Close()
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
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", response.StatusCode, http.StatusOK)
		}
	})
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	return parsed
}
