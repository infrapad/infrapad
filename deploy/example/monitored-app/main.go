package main

import (
	"fmt"
	"io"
	"net/http"
	"sync"
)

type endpointState struct {
	mu     sync.RWMutex
	status map[string]string // endpoint -> "ok" or "fail"
}

func newEndpointState() *endpointState {
	return &endpointState{
		status: map[string]string{
			"endpoint1": "ok",
			"endpoint2": "ok",
		},
	}
}

func (s *endpointState) get(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.status[name]
	return v, ok
}

func (s *endpointState) set(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[name] = value
}

func main() {
	state := newEndpointState()

	handler := func(endpoint string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				val, ok := state.get(endpoint)
				if !ok {
					http.NotFound(w, r)
					return
				}
				if val == "fail" {
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, "fail")
				} else {
					fmt.Fprint(w, "ok")
				}
			case http.MethodPost:
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				val := string(body)
				if val != "ok" && val != "fail" {
					http.Error(w, "body must be 'ok' or 'fail'", http.StatusBadRequest)
					return
				}
				state.set(endpoint, val)
				fmt.Fprint(w, val)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		}
	}

	http.HandleFunc("/endpoint1", handler("endpoint1"))
	http.HandleFunc("/endpoint2", handler("endpoint2"))

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		state.mu.RLock()
		defer state.mu.RUnlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		for endpoint, val := range state.status {
			up := 1
			if val == "fail" {
				up = 0
			}
			fmt.Fprintf(w, "monitored_app_up{endpoint=\"%s\"} %d\n", endpoint, up)
		}
	})

	fmt.Println("monitored-app listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Printf("server error: %v\n", err)
	}
}
