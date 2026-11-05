package grpc

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// ---------------------------------------------------------------------------
// model → proto
// ---------------------------------------------------------------------------

func documentToProto(d model.Document) (*pb.Document, error) {
	out := &pb.Document{
		Name:      "documents/" + string(d.Uid),
		Status:    string(d.Status),
		Title:     d.Title,
		Namespace: d.Namespace,
	}
	if !d.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(d.CreatedAt)
	}
	for _, b := range d.Blocks {
		block, err := blockToProto(b, d.Uid)
		if err != nil {
			return nil, err
		}
		out.Blocks = append(out.Blocks, block)
	}
	return out, nil
}

func blockToProto(b model.Block, documentUID model.DocumentUID) (*pb.Block, error) {
	out := &pb.Block{
		Name:           fmt.Sprintf("documents/%s/blocks/%d", string(documentUID), b.BlockNumber),
		BlockNumber:    int32(b.BlockNumber),
		RevisionNumber: int32(b.RevisionNumber),
		AuthorId:       string(b.AuthorID),
		Type:           b.Type,
		Status:         string(b.Status),
	}
	if !b.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(b.CreatedAt)
	}
	if b.ParentBlockNumber != nil {
		out.ParentBlockNumber = int32Ptr(*b.ParentBlockNumber)
	}

	// The public record is the serialized host record envelope, verbatim:
	// standalone {timestamp, content[, key]} or grouped {items: [...]}.
	if b.Record != nil {
		data, err := content.EncodeRecord(b.Record)
		if err != nil {
			return nil, fmt.Errorf("block %d: encode record: %w", b.BlockNumber, err)
		}
		record, err := structFromJSON(data)
		if err != nil {
			return nil, fmt.Errorf("block %d: convert record: %w", b.BlockNumber, err)
		}
		out.Record = record
	}
	return out, nil
}

func structFromJSON(data []byte) (*structpb.Struct, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return structpb.NewStruct(m)
}

// ---------------------------------------------------------------------------
// proto → model
// ---------------------------------------------------------------------------

func blockFromProto(b *pb.Block) (model.Block, error) {
	if b == nil {
		return model.Block{}, nil
	}
	out := model.Block{
		BlockNumber:    model.BlockNumber(b.BlockNumber),
		RevisionNumber: model.RevisionNumber(b.RevisionNumber),
		AuthorID:       model.AuthorID(b.AuthorId),
		Type:           b.Type,
		Status:         model.BlockStatus(b.Status),
	}
	if b.CreatedAt != nil {
		out.CreatedAt = b.CreatedAt.AsTime()
	}
	if b.ParentBlockNumber != nil {
		out.ParentBlockNumber = blockNumberPtr(model.BlockNumber(*b.ParentBlockNumber))
	}
	// The public record envelope is passed through as raw JSON: the
	// controller's public write boundary decodes it (inner content plus an
	// optional timestamp echo) after the ownership checks, rejecting
	// generated keys and collection membership.
	if b.Record != nil {
		data, err := json.Marshal(b.Record.AsMap())
		if err != nil {
			return model.Block{}, fmt.Errorf("encode block record: %w", err)
		}
		out.PublicRecord = data
	}
	return out, nil
}

func blockNumberPtr(n model.BlockNumber) *model.BlockNumber {
	parent := n
	return &parent
}

func int32Ptr(n model.BlockNumber) *int32 {
	v := int32(n)
	return &v
}
