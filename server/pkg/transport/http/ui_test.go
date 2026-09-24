package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"index.html": "<h1>SPA</h1>", "assets/app.js": "console.log('ok')", "assets/..theme.js": "console.log('theme')"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	RegisterHealthRoutes(mux, &Readiness{})
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	RegisterUIRoutes(mux, dir, "https://metrics.example.test")

	get := func(url string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		return w
	}
	for _, url := range []string{"/ui/", "/ui/documents", "/ui/documents/", "/ui/documents/doc-1"} {
		w := get(url)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "SPA") {
			t.Errorf("%s: %d %q", url, w.Code, w.Body.String())
		}
	}
	if w := get("/"); w.Code != 302 || w.Header().Get("Location") != "/ui/documents" {
		t.Errorf("root: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := get("/ui/assets/app.js"); w.Code != 200 || !strings.Contains(w.Body.String(), "console.log") {
		t.Errorf("asset: %d %q", w.Code, w.Body.String())
	}
	if w := get("/ui/assets/..theme.js"); w.Code != 200 || !strings.Contains(w.Body.String(), "theme") {
		t.Errorf("dot-prefixed asset: %d %q", w.Code, w.Body.String())
	}
	for _, url := range []string{"/ui/assets/missing.js", "/ui/assets/app.js/", "/ui/missing.css", "/elsewhere", "/ui/assets/unknown", "/ui/%2e%2e/secret"} {
		if w := get(url); w.Code != 404 {
			t.Errorf("%s: got %d, want 404", url, w.Code)
		}
	}
	if w := get("/v1/documents"); w.Code != 202 {
		t.Errorf("API: %d", w.Code)
	}
	if w := get("/livez"); w.Code != 200 {
		t.Errorf("health: %d", w.Code)
	}

	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "assets", "secret.txt")); err != nil {
		t.Fatal(err)
	}
	if w := get("/ui/assets/secret.txt"); w.Code != 404 || strings.Contains(w.Body.String(), "secret") {
		t.Errorf("symlink: %d %q", w.Code, w.Body.String())
	}

	for _, headers := range []map[string]string{
		{"X-Forwarded-User": "alice", "X-Forwarded-Email": "alice@example.test", "Authorization": "Bearer secret"},
		{"X-Forwarded-User": "bob"},
		{"X-Forwarded-Email": "orphan@example.test"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/ui/config", nil)
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var body struct {
			Identity *struct {
				Username string `json:"username"`
				Email    string `json:"email"`
			} `json:"identity"`
			Services map[string]string `json:"services"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || body.Services["infrapadApiBaseUrl"] != "/v1" || body.Services["prometheusApiBaseUrl"] != "https://metrics.example.test" {
			t.Errorf("config: %d %q", w.Code, w.Body.String())
		}
		if headers["X-Forwarded-User"] == "" && body.Identity != nil || headers["X-Forwarded-User"] != "" && (body.Identity == nil || body.Identity.Username != headers["X-Forwarded-User"] || body.Identity.Email != headers["X-Forwarded-Email"]) {
			t.Errorf("identity: %q", w.Body.String())
		}
		if strings.Contains(w.Body.String(), "Bearer") || strings.Contains(w.Body.String(), "orphan") || headers["X-Forwarded-Email"] == "" && strings.Contains(w.Body.String(), "email") {
			t.Errorf("config leaks headers: %q", w.Body.String())
		}
	}
}

func TestUIOptionalAndUnusable(t *testing.T) {
	for _, tc := range []struct {
		dir, url string
		status   int
	}{
		{"", "/", 404}, {"", "/ui/documents", 404}, {"", "/ui/config", 200},
		{filepath.Join(t.TempDir(), "missing"), "/ui/documents", 503},
		{t.TempDir(), "/ui/", 503},
		{t.TempDir(), "/", 503},
	} {
		mux := http.NewServeMux()
		RegisterUIRoutes(mux, tc.dir, "http://localhost:9090")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", tc.url, nil))
		if w.Code != tc.status {
			t.Errorf("%s (%q): got %d want %d", tc.url, tc.dir, w.Code, tc.status)
		}
	}
}
