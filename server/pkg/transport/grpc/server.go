package grpc

import (
	"context"
	"fmt"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// DefaultPort is the default gRPC listen port.
const DefaultPort = 50061

// Server wraps the gRPC server and the controller.
type Server struct {
	pb.UnimplementedInfrapadServiceServer
	ctrl *controller.Controller
	gs   *grpc.Server
}

// New creates a new gRPC server backed by the given controller.
func New(ctrl *controller.Controller) *Server {
	s := &Server{ctrl: ctrl}
	s.gs = grpc.NewServer()
	pb.RegisterInfrapadServiceServer(s.gs, s)
	return s
}

// Serve starts listening on the given address.
func (s *Server) Serve(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	return s.gs.Serve(lis)
}

// GracefulStop stops the gRPC server gracefully.
func (s *Server) GracefulStop() {
	s.gs.GracefulStop()
}

// GRPCServer returns the underlying grpc.Server (useful for testing).
func (s *Server) GRPCServer() *grpc.Server {
	return s.gs
}

// ---------------------------------------------------------------------------
// Document RPCs
// ---------------------------------------------------------------------------

func (s *Server) CreateDocument(ctx context.Context, req *pb.CreateDocumentRequest) (*pb.CreateDocumentResponse, error) {
	doc := model.Document{
		Title:     req.GetTitle(),
		Namespace: req.GetNamespace(),
	}
	for _, b := range req.GetBlocks() {
		doc.Blocks = append(doc.Blocks, blockFromProto(b))
	}

	created, err := s.ctrl.CreateDocument(ctx, doc)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create document: %v", err)
	}
	return &pb.CreateDocumentResponse{Document: documentToProto(created)}, nil
}

// documentUIDFromName extracts the document UID from a resource name like "documents/{uid}".
func documentUIDFromName(name string) model.DocumentUID {
	return model.DocumentUID(strings.TrimPrefix(name, "documents/"))
}

// documentUIDFromParent extracts the document UID from a parent resource name like "documents/{uid}".
func documentUIDFromParent(parent string) model.DocumentUID {
	return documentUIDFromName(parent)
}

func (s *Server) GetDocument(ctx context.Context, req *pb.GetDocumentRequest) (*pb.GetDocumentResponse, error) {
	doc, err := s.ctrl.GetDocument(ctx, documentUIDFromName(req.GetName()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get document: %v", err)
	}
	return &pb.GetDocumentResponse{Document: documentToProto(doc)}, nil
}

func (s *Server) ListDocuments(ctx context.Context, req *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
	documents, err := s.ctrl.ListDocuments(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list documents: %v", err)
	}
	resp := &pb.ListDocumentsResponse{}
	for _, d := range documents {
		resp.Documents = append(resp.Documents, documentToProto(d))
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Block RPCs
// ---------------------------------------------------------------------------

func (s *Server) AddBlock(ctx context.Context, req *pb.AddBlockRequest) (*pb.AddBlockResponse, error) {
	documentUID := documentUIDFromParent(req.GetParent())
	blk := blockFromProto(req.GetBlock())
	created, err := s.ctrl.AddBlock(ctx, documentUID, blk)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "add block: %v", err)
	}
	return &pb.AddBlockResponse{Block: blockToProto(created, documentUID)}, nil
}

func (s *Server) UpdateBlock(ctx context.Context, req *pb.UpdateBlockRequest) (*pb.UpdateBlockResponse, error) {
	documentUID := documentUIDFromParent(req.GetParent())
	blk := blockFromProto(req.GetBlock())
	updated, err := s.ctrl.UpdateBlock(ctx, documentUID, model.BlockNumber(req.GetBlockNumber()), blk)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "update block: %v", err)
	}
	return &pb.UpdateBlockResponse{Block: blockToProto(updated, documentUID)}, nil
}

func (s *Server) GetBlock(ctx context.Context, req *pb.GetBlockRequest) (*pb.GetBlockResponse, error) {
	documentUID := documentUIDFromParent(req.GetParent())
	blk, err := s.ctrl.GetBlock(ctx, documentUID, model.BlockNumber(req.GetBlockNumber()), model.RevisionNumber(req.GetRevisionNumber()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get block: %v", err)
	}
	return &pb.GetBlockResponse{Block: blockToProto(blk, documentUID)}, nil
}

func (s *Server) ListBlocks(ctx context.Context, req *pb.ListBlocksRequest) (*pb.ListBlocksResponse, error) {
	documentUID := documentUIDFromParent(req.GetParent())
	blocks, err := s.ctrl.ListBlocks(ctx, documentUID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list blocks: %v", err)
	}
	resp := &pb.ListBlocksResponse{}
	for _, b := range blocks {
		resp.Blocks = append(resp.Blocks, blockToProto(b, documentUID))
	}
	return resp, nil
}

func (s *Server) ListBlockHistory(ctx context.Context, req *pb.ListBlockHistoryRequest) (*pb.ListBlockHistoryResponse, error) {
	documentUID := documentUIDFromParent(req.GetParent())
	revisions, err := s.ctrl.ListBlockHistory(ctx, documentUID, model.BlockNumber(req.GetBlockNumber()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list block history: %v", err)
	}
	resp := &pb.ListBlockHistoryResponse{}
	for _, b := range revisions {
		resp.Blocks = append(resp.Blocks, blockToProto(b, documentUID))
	}
	return resp, nil
}
