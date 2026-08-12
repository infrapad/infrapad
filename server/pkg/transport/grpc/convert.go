package grpc

import (
	"fmt"
	"time"

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

	if b.Content != nil {
		switch c := b.Content.(type) {
		case model.MarkdownBC:
			out.Content = &pb.Block_Markdown{
				Markdown: &pb.MarkdownContent{Text: c.Render()},
			}
		case model.AlertsMatcherBC:
			out.Content = &pb.Block_AlertsMatcher{
				AlertsMatcher: alertsMatcherToProto(c),
			}
		}
	}
	return out
}

func alertsMatcherToProto(am model.AlertsMatcherBC) *pb.AlertsMatcherContent {
	out := &pb.AlertsMatcherContent{}
	for _, m := range am.LabelsMatchers {
		lm := &pb.LabelsMatcher{Labels: make(map[string]*pb.LabelValues)}
		for k, vals := range m {
			lm.Labels[k] = &pb.LabelValues{Values: vals}
		}
		out.LabelsMatchers = append(out.LabelsMatchers, lm)
	}
	if !am.Since.IsZero() {
		out.Since = timestamppb.New(am.Since)
	}
	if !am.Until.IsZero() {
		out.Until = timestamppb.New(am.Until)
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

	switch c := b.Content.(type) {
	case *pb.Block_Markdown:
		out.Type = "markdown"
		out.Content = model.NewMarkdownBC(c.Markdown.GetText())
	case *pb.Block_AlertsMatcher:
		out.Type = "alerts_matcher"
		out.Content = alertsMatcherFromProto(c.AlertsMatcher)
	}
	return out
}

func alertsMatcherFromProto(am *pb.AlertsMatcherContent) model.AlertsMatcherBC {
	if am == nil {
		return model.AlertsMatcherBC{}
	}
	out := model.AlertsMatcherBC{}
	for _, lm := range am.LabelsMatchers {
		m := make(map[string][]string)
		for k, lv := range lm.Labels {
			m[k] = lv.Values
		}
		out.LabelsMatchers = append(out.LabelsMatchers, m)
	}
	if am.Since != nil {
		out.Since = am.Since.AsTime()
	}
	if am.Until != nil {
		out.Until = am.Until.AsTime()
	} else {
		out.Until = time.Time{}
	}
	return out
}
