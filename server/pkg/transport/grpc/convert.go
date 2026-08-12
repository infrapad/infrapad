package grpc

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// ---------------------------------------------------------------------------
// model → proto
// ---------------------------------------------------------------------------

func docToProto(d model.Doc) *pb.Doc {
	out := &pb.Doc{
		Name:      "docs/" + string(d.Uid),
		Status:    string(d.Status),
		Title:     d.Title,
		Namespace: d.Namespace,
	}
	if !d.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(d.CreatedAt)
	}
	for _, b := range d.Blocks {
		out.Blocks = append(out.Blocks, blockToProto(b, d.Uid))
	}
	return out
}

func blockToProto(b model.Block, docUID model.DocUID) *pb.Block {
	out := &pb.Block{
		Name:           fmt.Sprintf("docs/%s/blocks/%d", string(docUID), b.BlockNumber),
		BlockNumber:    int32(b.BlockNumber),
		RevisionNumber: int32(b.RevisionNumber),
		AuthorId:       string(b.AuthorID),
		Type:           b.Type,
		Status:         string(b.Status),
	}
	if !b.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(b.CreatedAt)
	}

	// Convert model.BlockContent → structpb.Struct via SerializedContent.
	// The serialized JSON is the canonical representation that gets
	// exposed as the generic Struct in the API.
	if b.Content != nil {
		sc, err := b.Content.Serialize()
		if err == nil {
			var m map[string]any
			if err := json.Unmarshal(sc.Data, &m); err == nil {
				if s, err := structpb.NewStruct(m); err == nil {
					out.Content = s
				}
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// proto → model
// ---------------------------------------------------------------------------

func blockFromProto(b *pb.Block) model.Block {
	if b == nil {
		return model.Block{}
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

	// Convert structpb.Struct → model.SerializedContent, then deserialize
	// into the registered BlockContent type via the model's deserializer
	// registry.
	if b.Content != nil && b.Type != "" {
		data, err := json.Marshal(b.Content.AsMap())
		if err == nil {
			sc := model.SerializedContent{Type: b.Type, Data: data}
			content, err := model.DeserializeBlockContent(sc)
			if err == nil {
				out.Content = content
				out.SerializedContent = sc
			}
		}
	}
	return out
}
