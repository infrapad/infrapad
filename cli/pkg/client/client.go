package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const maxErrorBody = 8 * 1024

// Client calls the InfraPad HTTP/JSON gateway.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

// New creates an HTTP gateway client for apiURL.
func New(apiURL string) (*Client, error) {
	baseURL, err := parseAPIURL(apiURL)
	if err != nil {
		return nil, err
	}

	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}, nil
}

func parseAPIURL(apiURL string) (*url.URL, error) {
	parsed, err := url.Parse(apiURL)
	if err != nil {
		return nil, fmt.Errorf("invalid API URL %q: %w", apiURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid API URL %q: scheme must be http or https", apiURL)
	}
	if !parsed.IsAbs() || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid API URL %q: an absolute URL with a host is required", apiURL)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("invalid API URL %q: user information is not allowed", apiURL)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return nil, fmt.Errorf("invalid API URL %q: query strings are not allowed", apiURL)
	}
	if parsed.Fragment != "" || strings.Contains(apiURL, "#") {
		return nil, fmt.Errorf("invalid API URL %q: fragments are not allowed", apiURL)
	}

	// Gateway routes are appended below this optional deployment prefix. Trim
	// only literal trailing slashes so percent-encoded path data is preserved.
	escapedPath := strings.TrimRight(parsed.EscapedPath(), "/")
	parsed.Path, err = url.PathUnescape(escapedPath)
	if err != nil {
		return nil, fmt.Errorf("invalid API URL %q: invalid path: %w", apiURL, err)
	}
	parsed.RawPath = escapedPath
	if parsed.RawPath == parsed.Path {
		parsed.RawPath = ""
	}
	return parsed, nil
}

// Close releases idle HTTP connections held by the client.
func (c *Client) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// CreateDocument creates a new document.
func (c *Client) CreateDocument(ctx context.Context, title, namespace string) (*pb.Document, error) {
	request := &pb.CreateDocumentRequest{
		Title:     title,
		Namespace: namespace,
	}
	response := &pb.CreateDocumentResponse{}
	if err := c.do(ctx, "create document", http.MethodPost, []string{"v1", "documents"}, request, response); err != nil {
		return nil, err
	}
	return response.GetDocument(), nil
}

// GetDocument retrieves a document by a bare ID or canonical resource name.
func (c *Client) GetDocument(ctx context.Context, name string) (*pb.Document, error) {
	id, err := documentID(name)
	if err != nil {
		return nil, fmt.Errorf("get document: %w", err)
	}

	response := &pb.GetDocumentResponse{}
	if err := c.do(ctx, "get document", http.MethodGet, []string{"v1", "documents", id}, nil, response); err != nil {
		return nil, err
	}
	return response.GetDocument(), nil
}

// ListDocuments lists all documents.
func (c *Client) ListDocuments(ctx context.Context) ([]*pb.Document, error) {
	response := &pb.ListDocumentsResponse{}
	if err := c.do(ctx, "list documents", http.MethodGet, []string{"v1", "documents"}, nil, response); err != nil {
		return nil, err
	}
	return response.GetDocuments(), nil
}

// AddBlock adds a block to a document.
func (c *Client) AddBlock(ctx context.Context, parent string, block *pb.Block) (*pb.Block, error) {
	id, err := documentID(parent)
	if err != nil {
		return nil, fmt.Errorf("add block: %w", err)
	}

	request := &pb.AddBlockRequest{
		Parent: "documents/" + id,
		Block:  block,
	}
	response := &pb.AddBlockResponse{}
	if err := c.do(ctx, "add block", http.MethodPost, []string{"v1", "documents", id, "blocks"}, request, response); err != nil {
		return nil, err
	}
	return response.GetBlock(), nil
}

// UpdateBlock updates an existing block.
func (c *Client) UpdateBlock(ctx context.Context, parent string, blockNumber int32, block *pb.Block) (*pb.Block, error) {
	id, err := documentID(parent)
	if err != nil {
		return nil, fmt.Errorf("update block: %w", err)
	}

	request := &pb.UpdateBlockRequest{
		Parent:      "documents/" + id,
		BlockNumber: blockNumber,
		Block:       block,
	}
	response := &pb.UpdateBlockResponse{}
	route := []string{"v1", "documents", id, "blocks", strconv.FormatInt(int64(blockNumber), 10)}
	if err := c.do(ctx, "update block", http.MethodPut, route, request, response); err != nil {
		return nil, err
	}
	return response.GetBlock(), nil
}

// GetBlock retrieves a specific block.
func (c *Client) GetBlock(ctx context.Context, parent string, blockNumber int32) (*pb.Block, error) {
	id, err := documentID(parent)
	if err != nil {
		return nil, fmt.Errorf("get block: %w", err)
	}

	response := &pb.GetBlockResponse{}
	route := []string{"v1", "documents", id, "blocks", strconv.FormatInt(int64(blockNumber), 10)}
	if err := c.do(ctx, "get block", http.MethodGet, route, nil, response); err != nil {
		return nil, err
	}
	return response.GetBlock(), nil
}

// ListBlocks lists all blocks for a document.
func (c *Client) ListBlocks(ctx context.Context, parent string) ([]*pb.Block, error) {
	id, err := documentID(parent)
	if err != nil {
		return nil, fmt.Errorf("list blocks: %w", err)
	}

	response := &pb.ListBlocksResponse{}
	if err := c.do(ctx, "list blocks", http.MethodGet, []string{"v1", "documents", id, "blocks"}, nil, response); err != nil {
		return nil, err
	}
	return response.GetBlocks(), nil
}

// ListBlockHistory returns revision history for a block.
func (c *Client) ListBlockHistory(ctx context.Context, parent string, blockNumber int32) ([]*pb.Block, error) {
	id, err := documentID(parent)
	if err != nil {
		return nil, fmt.Errorf("list block history: %w", err)
	}

	response := &pb.ListBlockHistoryResponse{}
	route := []string{"v1", "documents", id, "blocks", strconv.FormatInt(int64(blockNumber), 10), "history"}
	if err := c.do(ctx, "list block history", http.MethodGet, route, nil, response); err != nil {
		return nil, err
	}
	return response.GetBlocks(), nil
}

func documentID(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("document name must not be empty")
	}

	id := name
	if strings.HasPrefix(name, "documents/") {
		id = strings.TrimPrefix(name, "documents/")
	}
	if id == "" || strings.Contains(id, "/") {
		return "", fmt.Errorf("invalid document name %q: use a bare ID or documents/<id>", name)
	}
	return id, nil
}

func (c *Client) do(ctx context.Context, operation, method string, route []string, request, response proto.Message) error {
	var body io.Reader
	if request != nil {
		payload, err := protojson.Marshal(request)
		if err != nil {
			return fmt.Errorf("%s: encode request: %w", operation, err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(route), body)
	if err != nil {
		return fmt.Errorf("%s: construct request: %w", operation, err)
	}
	req.Header.Set("Accept", "application/json")
	if request != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: send request: %w", operation, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return gatewayError(operation, resp)
	}

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read response: %w", operation, err)
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(payload, response); err != nil {
		return fmt.Errorf("%s: decode response: %w", operation, err)
	}
	return nil
}

func (c *Client) endpoint(route []string) string {
	endpoint := *c.baseURL
	path := endpoint.Path
	escapedPath := endpoint.EscapedPath()
	for _, segment := range route {
		path += "/" + segment
		escapedPath += "/" + url.PathEscape(segment)
	}
	endpoint.Path = path
	endpoint.RawPath = escapedPath
	if endpoint.RawPath == endpoint.Path {
		endpoint.RawPath = ""
	}
	return endpoint.String()
}

func gatewayError(operation string, resp *http.Response) error {
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody+1))
	if err != nil {
		return fmt.Errorf("%s: read error response (HTTP %s): %w", operation, resp.Status, err)
	}

	truncated := len(payload) > maxErrorBody
	if truncated {
		payload = payload[:maxErrorBody]
	}

	var gatewayStatus struct {
		Message string `json:"message"`
	}
	if !truncated && json.Unmarshal(payload, &gatewayStatus) == nil && gatewayStatus.Message != "" {
		return fmt.Errorf("%s: HTTP %s: %s", operation, resp.Status, gatewayStatus.Message)
	}

	body := strings.Join(strings.Fields(strings.ToValidUTF8(string(payload), "?")), " ")
	if body == "" {
		body = "empty response body"
	}
	if truncated {
		body += " [truncated]"
	}
	return fmt.Errorf("%s: HTTP %s: %s", operation, resp.Status, body)
}
