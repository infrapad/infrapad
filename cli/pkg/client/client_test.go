package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestNewValidatesAPIURLAndPreservesBasePath(t *testing.T) {
	t.Parallel()

	accepted := map[string]string{
		"http://example.com":             "http://example.com/v1/documents",
		"https://example.com:8443/":      "https://example.com:8443/v1/documents",
		"http://example.com/api/root/":   "http://example.com/api/root/v1/documents",
		"http://example.com/api%2Froot/": "http://example.com/api%2Froot/v1/documents",
	}
	for input, expected := range accepted {
		t.Run(input, func(t *testing.T) {
			client, err := New(input, "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if got := client.endpoint([]string{"v1", "documents"}); got != expected {
				t.Fatalf("endpoint = %q, want %q", got, expected)
			}
		})
	}

	rejected := []string{
		"",
		"localhost:8088",
		"ftp://example.com",
		"http:///api",
		"http://user@example.com",
		"http://example.com?debug=true",
		"http://example.com?",
		"http://example.com#fragment",
		"http://example.com#",
	}
	for _, input := range rejected {
		t.Run("reject_"+input, func(t *testing.T) {
			if _, err := New(input, ""); err == nil {
				t.Fatalf("New(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestAddBlockProtoJSONRoundTrip(t *testing.T) {
	t.Parallel()

	requestErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var requestErr error
		defer func() { requestErrors <- requestErr }()

		if r.Method != http.MethodPost {
			requestErr = fmt.Errorf("method = %s, want POST", r.Method)
			return
		}
		if got, want := r.URL.EscapedPath(), "/gateway/v1/documents/doc%20%25%3F/blocks"; got != want {
			requestErr = fmt.Errorf("path = %q, want %q", got, want)
			return
		}
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			requestErr = err
			return
		}
		request := &pb.AddBlockRequest{}
		if err := protojson.Unmarshal(payload, request); err != nil {
			requestErr = fmt.Errorf("decode request: %w", err)
			return
		}
		if request.GetParent() != "documents/doc %?" || request.GetBlock().GetRecord().AsMap()["content"].(map[string]any)["text"] != "hello" {
			requestErr = fmt.Errorf("unexpected request: %s", payload)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"block": {
				"name": "documents/doc %?/blocks/1",
				"blockNumber": 1,
				"revisionNumber": 2,
				"type": "Core/Markdown",
				"createdAt": "2026-08-20T15:15:03Z",
				"record": {"timestamp": "2026-08-20T15:15:03Z", "content": {"text": "hello"}}
			},
			"futureField": true
		}`)
	}))
	defer server.Close()

	client, err := New(server.URL+"/gateway/", "")
	if err != nil {
		t.Fatal(err)
	}
	record, err := structpb.NewStruct(map[string]any{"content": map[string]any{"text": "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	block, err := client.AddBlock(context.Background(), "documents/doc %?", &pb.Block{Type: "Core/Markdown", Record: record})
	requestErr := <-requestErrors
	if requestErr != nil {
		t.Fatal(requestErr)
	}
	if err != nil {
		t.Fatalf("AddBlock() error = %v", err)
	}
	if block.GetBlockNumber() != 1 || block.GetRevisionNumber() != 2 || block.GetCreatedAt() == nil {
		t.Fatalf("unexpected response block: %v", block)
	}
	if got := block.GetRecord().AsMap()["content"].(map[string]any)["text"]; got != "hello" {
		t.Fatalf("content text = %v, want hello", got)
	}
}

func TestExportDocumentMarkdownReturnsOpaqueMarkdown(t *testing.T) {
	t.Parallel()

	const markdown = "---\ndocument: abc\n---\n::infrapad_block{block=1}\n  body \n\n"
	paths := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.Method + " " + r.URL.Path
		payload, _ := protojson.Marshal(&pb.ExportDocumentMarkdownResponse{Markdown: markdown})
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	client, err := New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ExportDocumentMarkdown(context.Background(), "documents/abc")
	if err != nil {
		t.Fatalf("ExportDocumentMarkdown() error = %v", err)
	}
	if path := <-paths; path != "GET /v1/documents/abc/markdown" {
		t.Fatalf("request = %q", path)
	}
	if got != markdown {
		t.Fatalf("markdown = %q, want %q", got, markdown)
	}
}

func TestPushDocumentMarkdown(t *testing.T) {
	t.Parallel()

	const submitted = "---\ndocument: abc\n---\n::infrapad_block{block=1}\n  edited \n\n"
	const refreshed = "---\ndocument: abc\n---\nrefreshed\n"
	type captured struct {
		request string
		body    *pb.PushDocumentMarkdownRequest
	}
	requests := make(chan captured, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body := &pb.PushDocumentMarkdownRequest{}
		_ = protojson.Unmarshal(data, body)
		requests <- captured{request: r.Method + " " + r.URL.Path, body: body}
		payload, _ := protojson.Marshal(&pb.PushDocumentMarkdownResponse{
			Markdown: refreshed,
			Changes: []*pb.MarkdownChange{
				{BlockNumber: 1, Action: "updated"},
				{BlockNumber: 2, Action: "added"},
			},
		})
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	client, err := New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.PushDocumentMarkdown(context.Background(), "documents/abc", submitted)
	if err != nil {
		t.Fatalf("PushDocumentMarkdown() error = %v", err)
	}
	req := <-requests
	if req.request != "POST /v1/documents/abc/markdown" {
		t.Fatalf("request = %q", req.request)
	}
	if req.body.GetMarkdown() != submitted {
		t.Fatalf("submitted markdown = %q, want %q", req.body.GetMarkdown(), submitted)
	}
	if got.GetMarkdown() != refreshed {
		t.Fatalf("markdown = %q, want %q", got.GetMarkdown(), refreshed)
	}
	changes := got.GetChanges()
	if len(changes) != 2 || changes[0].GetBlockNumber() != 1 || changes[0].GetAction() != "updated" ||
		changes[1].GetBlockNumber() != 2 || changes[1].GetAction() != "added" {
		t.Fatalf("changes = %v", changes)
	}
}

func TestDocumentNameNormalization(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	client, err := New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"abc-123", "documents/abc-123"} {
		if _, err := client.ListBlocks(context.Background(), name); err != nil {
			t.Fatalf("ListBlocks(%q) error = %v", name, err)
		}
	}

	for _, name := range []string{"", "documents/", "documents/a/b", "a/b"} {
		if _, err := client.ListBlocks(context.Background(), name); err == nil {
			t.Errorf("ListBlocks(%q) unexpectedly succeeded", name)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if got, want := strings.Join(paths, ","), "/v1/documents/abc-123/blocks,/v1/documents/abc-123/blocks"; got != want {
		t.Fatalf("paths = %q, want %q", got, want)
	}
}

func TestAuthorizationHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		token       string
		want        string
		wantPresent bool
	}{
		{name: "present", token: "opaque token", want: "Bearer opaque token", wantPresent: true},
		{name: "absent", token: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			type observedHeader struct {
				value   string
				present bool
			}
			authorization := make(chan observedHeader, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, present := r.Header["Authorization"]
				authorization <- observedHeader{value: r.Header.Get("Authorization"), present: present}
				_, _ = io.WriteString(w, `{}`)
			}))
			defer server.Close()

			client, err := New(server.URL, test.token)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.ListDocuments(context.Background()); err != nil {
				t.Fatalf("ListDocuments() error = %v", err)
			}
			got := <-authorization
			if got.present != test.wantPresent || got.value != test.want {
				t.Fatalf("Authorization = (%q, present %t), want (%q, present %t)",
					got.value, got.present, test.want, test.wantPresent)
			}
		})
	}
}

func TestGatewayErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/structured/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":5,"message":"document was not found","details":[]}`)
		default:
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "<html>\n  proxy failed  \n"+strings.Repeat("x", maxErrorBody)+"TAIL")
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.ListBlocks(context.Background(), "structured")
	if err == nil || !strings.Contains(err.Error(), "list blocks: HTTP 404 Not Found: document was not found") {
		t.Fatalf("structured error = %v", err)
	}

	_, err = client.ListBlocks(context.Background(), "fallback")
	if err == nil {
		t.Fatal("unstructured error unexpectedly nil")
	}
	message := err.Error()
	for _, expected := range []string{"list blocks: HTTP 502 Bad Gateway", "<html> proxy failed", "[truncated]"} {
		if !strings.Contains(message, expected) {
			t.Errorf("unstructured error %q does not contain %q", message, expected)
		}
	}
	if strings.Contains(message, "TAIL") {
		t.Errorf("unstructured error contains content beyond limit")
	}
	if len(message) > maxErrorBody+200 {
		t.Errorf("unstructured error length = %d, want bounded near %d", len(message), maxErrorBody)
	}
}
