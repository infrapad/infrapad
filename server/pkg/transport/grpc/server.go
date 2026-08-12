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
const DefaultPort = 50051

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

func (s *Server) CreateDoc(ctx context.Context, req *pb.CreateDocRequest) (*pb.CreateDocResponse, error) {
	doc := model.Doc{
		Title:     req.GetTitle(),
		Namespace: req.GetNamespace(),
	}
	for _, b := range req.GetBlocks() {
		doc.Blocks = append(doc.Blocks, blockFromProto(b))
	}

	created, err := s.ctrl.CreateDoc(ctx, doc)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create doc: %v", err)
	}
	return &pb.CreateDocResponse{Doc: docToProto(created)}, nil
}

// docUIDFromName extracts the doc UID from a resource name like "docs/{uid}".
func docUIDFromName(name string) model.DocUID {
	return model.DocUID(strings.TrimPrefix(name, "docs/"))
}

// docUIDFromParent extracts the doc UID from a parent resource name like "docs/{uid}".
func docUIDFromParent(parent string) model.DocUID {
	return docUIDFromName(parent)
}

func (s *Server) GetDoc(ctx context.Context, req *pb.GetDocRequest) (*pb.GetDocResponse, error) {
	doc, err := s.ctrl.GetDoc(ctx, docUIDFromName(req.GetName()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get doc: %v", err)
	}
	return &pb.GetDocResponse{Doc: docToProto(doc)}, nil
}

func (s *Server) ListDocs(ctx context.Context, req *pb.ListDocsRequest) (*pb.ListDocsResponse, error) {
	docs, err := s.ctrl.ListDocs(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list docs: %v", err)
	}
	resp := &pb.ListDocsResponse{}
	for _, d := range docs {
		resp.Docs = append(resp.Docs, docToProto(d))
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Block RPCs
// ---------------------------------------------------------------------------

func (s *Server) AddBlock(ctx context.Context, req *pb.AddBlockRequest) (*pb.AddBlockResponse, error) {
	docUID := docUIDFromParent(req.GetParent())
	blk := blockFromProto(req.GetBlock())
	created, err := s.ctrl.AddBlock(ctx, docUID, blk)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "add block: %v", err)
	}
	return &pb.AddBlockResponse{Block: blockToProto(created, docUID)}, nil
}

func (s *Server) UpdateBlock(ctx context.Context, req *pb.UpdateBlockRequest) (*pb.UpdateBlockResponse, error) {
	docUID := docUIDFromParent(req.GetParent())
	blk := blockFromProto(req.GetBlock())
	updated, err := s.ctrl.UpdateBlock(ctx, docUID, model.BlockNumber(req.GetBlockNumber()), blk)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "update block: %v", err)
	}
	return &pb.UpdateBlockResponse{Block: blockToProto(updated, docUID)}, nil
}

func (s *Server) GetBlock(ctx context.Context, req *pb.GetBlockRequest) (*pb.GetBlockResponse, error) {
	docUID := docUIDFromParent(req.GetParent())
	blk, err := s.ctrl.GetBlock(ctx, docUID, model.BlockNumber(req.GetBlockNumber()), model.RevisionNumber(req.GetRevisionNumber()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get block: %v", err)
	}
	return &pb.GetBlockResponse{Block: blockToProto(blk, docUID)}, nil
}

func (s *Server) ListBlocks(ctx context.Context, req *pb.ListBlocksRequest) (*pb.ListBlocksResponse, error) {
	docUID := docUIDFromParent(req.GetParent())
	blocks, err := s.ctrl.ListBlocks(ctx, docUID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list blocks: %v", err)
	}
	resp := &pb.ListBlocksResponse{}
	for _, b := range blocks {
		resp.Blocks = append(resp.Blocks, blockToProto(b, docUID))
	}
	return resp, nil
}

func (s *Server) ListBlockHistory(ctx context.Context, req *pb.ListBlockHistoryRequest) (*pb.ListBlockHistoryResponse, error) {
	docUID := docUIDFromParent(req.GetParent())
	revisions, err := s.ctrl.ListBlockHistory(ctx, docUID, model.BlockNumber(req.GetBlockNumber()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list block history: %v", err)
	}
	resp := &pb.ListBlockHistoryResponse{}
	for _, b := range revisions {
		resp.Blocks = append(resp.Blocks, blockToProto(b, docUID))
	}
	return resp, nil
}
