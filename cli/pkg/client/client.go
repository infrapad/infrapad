package client

import (
	"context"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client wraps the gRPC InfrapadService client.
type Client struct {
	conn   *grpc.ClientConn
	svc    pb.InfrapadServiceClient
}

// New creates a new gRPC client connected to addr.
func New(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{
		conn: conn,
		svc:  pb.NewInfrapadServiceClient(conn),
	}, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// CreateDoc creates a new document.
func (c *Client) CreateDoc(ctx context.Context, title, namespace string) (*pb.Document, error) {
	resp, err := c.svc.CreateDocument(ctx, &pb.CreateDocumentRequest{
		Title:     title,
		Namespace: namespace,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetDocument(), nil
}

// GetDoc retrieves a document by name.
func (c *Client) GetDoc(ctx context.Context, name string) (*pb.Document, error) {
	resp, err := c.svc.GetDocument(ctx, &pb.GetDocumentRequest{Name: name})
	if err != nil {
		return nil, err
	}
	return resp.GetDocument(), nil
}

// ListDocs lists all documents.
func (c *Client) ListDocs(ctx context.Context) ([]*pb.Document, error) {
	resp, err := c.svc.ListDocuments(ctx, &pb.ListDocumentsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetDocuments(), nil
}

// AddBlock adds a block to a document.
func (c *Client) AddBlock(ctx context.Context, parent string, block *pb.Block) (*pb.Block, error) {
	resp, err := c.svc.AddBlock(ctx, &pb.AddBlockRequest{
		Parent: parent,
		Block:  block,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetBlock(), nil
}

// UpdateBlock updates an existing block.
func (c *Client) UpdateBlock(ctx context.Context, parent string, blockNumber int32, block *pb.Block) (*pb.Block, error) {
	resp, err := c.svc.UpdateBlock(ctx, &pb.UpdateBlockRequest{
		Parent:      parent,
		BlockNumber: blockNumber,
		Block:       block,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetBlock(), nil
}

// GetBlock retrieves a specific block.
func (c *Client) GetBlock(ctx context.Context, parent string, blockNumber int32) (*pb.Block, error) {
	resp, err := c.svc.GetBlock(ctx, &pb.GetBlockRequest{
		Parent:      parent,
		BlockNumber: blockNumber,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetBlock(), nil
}

// ListBlocks lists all blocks for a document.
func (c *Client) ListBlocks(ctx context.Context, parent string) ([]*pb.Block, error) {
	resp, err := c.svc.ListBlocks(ctx, &pb.ListBlocksRequest{
		Parent: parent,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetBlocks(), nil
}

// ListBlockHistory returns revision history for a block.
func (c *Client) ListBlockHistory(ctx context.Context, parent string, blockNumber int32) ([]*pb.Block, error) {
	resp, err := c.svc.ListBlockHistory(ctx, &pb.ListBlockHistoryRequest{
		Parent:      parent,
		BlockNumber: blockNumber,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetBlocks(), nil
}
