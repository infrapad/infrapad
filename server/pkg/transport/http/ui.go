package http

import (
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type uiConfig struct {
	Identity struct {
		Username string `json:"username"`
		Email    string `json:"email,omitempty"`
	} `json:"identity"`
	Services struct {
		InfrapadAPIBaseURL   string `json:"infrapadApiBaseUrl"`
		PrometheusAPIBaseURL string `json:"prometheusApiBaseUrl"`
	} `json:"services"`
}

// MarshalJSON keeps an anonymous identity null while using value fields in uiConfig.
func (config uiConfig) MarshalJSON() ([]byte, error) {
	var identity any
	if config.Identity.Username != "" {
		identity = config.Identity
	}
	return json.Marshal(struct {
		Identity any `json:"identity"`
		Services any `json:"services"`
	}{Identity: identity, Services: config.Services})
}

// RegisterUIRoutes mounts the runtime configuration regardless of whether a
// frontend build is available. Forwarded identity headers must only be accepted
// when this server is reached through a trusted authentication proxy.
func RegisterUIRoutes(mux *http.ServeMux, dir, prometheusURL string) {
	mux.HandleFunc("/ui/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		config := uiConfig{}
		config.Identity.Username = r.Header.Get("X-Forwarded-User")
		if config.Identity.Username != "" {
			config.Identity.Email = r.Header.Get("X-Forwarded-Email")
		}
		config.Services.InfrapadAPIBaseURL = "/v1"
		config.Services.PrometheusAPIBaseURL = prometheusURL
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(config)
	})

	if dir == "" {
		return
	}
	files := &uiFiles{dir: dir}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if _, err := files.root(); err != nil {
			http.Error(w, "UI directory or entry file is unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Redirect(w, r, "/ui/documents", http.StatusFound)
	})
	mux.Handle("/ui/", http.StripPrefix("/ui", files))
}

type uiFiles struct{ dir string }

func (h *uiFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	root, err := h.root()
	if err != nil {
		http.Error(w, "UI directory or entry file is unavailable", http.StatusServiceUnavailable)
		return
	}
	entry := filepath.Join(root, "index.html")
	// URL.Path is decoded by net/http. Reject traversal rather than relying on
	// Clean (which would turn a malicious path into a different, valid file).
	rel := strings.TrimPrefix(r.URL.Path, "/")
	if rel == "" {
		http.ServeFile(w, r, entry)
		return
	}
	fileLike := strings.HasPrefix(rel, "assets/") || path.Ext(strings.TrimSuffix(rel, "/")) != ""
	if strings.HasSuffix(rel, "/") {
		if fileLike {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, entry)
		return
	}
	fs := uiFileSystem{root: root}
	file, err := fs.Open("/" + rel)
	if err == nil {
		_ = file.Close()
		// FileServer handles MIME types, HEAD, conditional requests and ranges.
		// The filesystem is the single authority for opening built assets.
		http.FileServer(fs).ServeHTTP(w, r)
		return
	}
	if !os.IsNotExist(err) || fileLike {
		// An escaping symlink, unreadable file or directory is not a SPA route.
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, entry)
}

func (h *uiFiles) root() (string, error) {
	root, err := filepath.EvalSymlinks(h.dir)
	if err != nil {
		return "", err
	}
	_, err = safeFile(root, filepath.Join(root, "index.html"))
	return root, err
}

// safeFile ensures the resolved regular file remains within the configured
// directory, even when the build contains symlinks.
func safeFile(root, name string) (string, error) {
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(rel) {
		return "", os.ErrPermission
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", os.ErrPermission
	}
	return resolved, nil
}

// uiFileSystem prevents FileServer's default directory listings and http.Dir's
// ability to follow symlinks outside the build directory.
type uiFileSystem struct{ root string }

func (fs uiFileSystem) Open(name string) (http.File, error) {
	resolved, err := safeFile(fs.root, filepath.Join(fs.root, filepath.FromSlash(strings.TrimPrefix(name, "/"))))
	if err != nil {
		return nil, err
	}
	return os.Open(resolved)
}
