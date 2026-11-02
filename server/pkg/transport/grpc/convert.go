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

	// The public API exposes the inner extension content value, not the
	// host record envelope: producers never see or supply host timestamps.
	if b.Record != nil {
		item, ok := b.Record.(content.ItemRecord)
		if !ok {
			// Grouped records: the public representation of grouped
			// records is deferred to a later task.
			return out, nil
		}
		data, err := json.Marshal(item.Content())
		if err != nil {
			return nil, fmt.Errorf("block %d: encode inner content: %w", b.BlockNumber, err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("block %d: decode inner content: %w", b.BlockNumber, err)
		}
		s, err := structpb.NewStruct(m)
		if err != nil {
			return nil, fmt.Errorf("block %d: convert content: %w", b.BlockNumber, err)
		}
		out.Content = s
	}
	return out, nil
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

	// The public API carries the inner extension content value. It is
	// passed through as raw JSON; validation, record construction, and
	// host timestamps are owned by the controller through the content
	// registry. Conversion failures are propagated, never discarded.
	if b.Content != nil {
		data, err := json.Marshal(b.Content.AsMap())
		if err != nil {
			return model.Block{}, fmt.Errorf("encode block content: %w", err)
		}
		out.Content = data
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
