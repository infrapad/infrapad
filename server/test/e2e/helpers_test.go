package e2e

// Shared transport, setup, and assertion helpers for API scenarios.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

func grpcAddr() string {
	if s := os.Getenv("GRPC_ADDR"); s != "" {
		return s
	}
	return "localhost:50061"
}

func httpBaseURL() string {
	if s := os.Getenv("HTTP_ADDR"); s != "" {
		return s
	}
	return "http://localhost:8088"
}

// httpDo is a small helper that performs an HTTP request, checks for a 2xx
// status, and decodes the JSON response body into dest.
func httpDo(t *testing.T, method, url string, body any, dest any) {
	t.Helper()

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("%s %s returned %d: %s", method, url, resp.StatusCode, string(respBody))
	}

	if dest != nil {
		if err := json.Unmarshal(respBody, dest); err != nil {
			t.Fatalf("decode response JSON: %v\nbody: %s", err, string(respBody))
		}
	}
}

// httpPost posts body as JSON and returns the status code and raw body.
func httpPost(t *testing.T, url string, body any) (int, string) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(raw)
}

// helperCreateDocument creates a valid document and returns its name, failing the test
// on error.
func helperCreateDocument(t *testing.T, client pb.InfrapadServiceClient) string {
	t.Helper()
	resp, err := client.CreateDocument(context.Background(), &pb.CreateDocumentRequest{
		Title: "helper document for invalid-data tests",
	})
	if err != nil {
		t.Fatalf("helperCreateDocument: %v", err)
	}
	return resp.GetDocument().GetName()
}

// helperAddBlock adds a valid markdown block and returns it.
func helperAddBlock(t *testing.T, client pb.InfrapadServiceClient, docName string) *pb.Block {
	t.Helper()
	resp, err := client.AddBlock(context.Background(), &pb.AddBlockRequest{
		Parent: docName,
		Block: &pb.Block{
			Type:   "Core/Markdown",
			Record: contentRecord(t, map[string]any{"text": "valid block"}),
		},
	})
	if err != nil {
		t.Fatalf("helperAddBlock: %v", err)
	}
	return resp.GetBlock()
}

// requireCode fails the test if the error does not carry the expected gRPC
// status code.  It also fails when err is nil but a non-OK code is expected.
func requireCode(t *testing.T, err error, want codes.Code, msgAndArgs ...interface{}) {
	t.Helper()
	got := codes.OK
	if err != nil {
		got = status.Code(err)
	}
	if got != want {
		label := ""
		if len(msgAndArgs) > 0 {
			label = msgAndArgs[0].(string) + ": "
		}
		t.Errorf("%sexpected code %s, got %s (err: %v)", label, want, got, err)
	}
}

// expectRejection checks the status code, the ErrorInfo reason, and (when
// given) a BadRequest field violation.
func expectRejection(t *testing.T, err error, code codes.Code, reason, field string) {
	t.Helper()
	st := status.Convert(err)
	if err == nil || st.Code() != code {
		t.Fatalf("expected %s (%s), got %v", code, reason, err)
	}
	var gotReason string
	var fields []string
	for _, detail := range st.Details() {
		switch d := detail.(type) {
		case *errdetails.ErrorInfo:
			gotReason = d.GetReason()
			if d.GetMetadata()["document"] == "" || d.GetMetadata()["block_number"] == "" {
				t.Errorf("%s: ErrorInfo lacks target metadata: %v", reason, d.GetMetadata())
			}
		case *errdetails.BadRequest:
			for _, v := range d.GetFieldViolations() {
				fields = append(fields, v.GetField())
			}
		}
	}
	if gotReason != reason {
		t.Fatalf("expected reason %s, got %q (%v)", reason, gotReason, err)
	}
	if field == "" {
		return
	}
	for _, f := range fields {
		if f == field {
			return
		}
	}
	t.Fatalf("%s: expected field violation %q, got %v", reason, field, fields)
}

// mustStruct creates a structpb.Struct from a map.
func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	return s
}
