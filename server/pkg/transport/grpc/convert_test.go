package grpc

import (
	"errors"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// failingPresenter is Core/Markdown with a presenter that always fails.
type failingPresenter struct{ *core.MarkdownType }

func (failingPresenter) Present(content.Record) (content.Presentation, error) {
	return content.Presentation{}, errors.New("boom")
}

// A failing presenter falls back to Markdown: the block converts without
// error, with no presentation and the rendered Markdown body.
func TestBlockToProtoPresentationErrorFallsBackToMarkdown(t *testing.T) {
	ct := failingPresenter{core.NewMarkdownType()}
	record := content.NewItem(ct, time.Now(), core.Markdown{Text: "hello"})
	out, err := blockToProto(model.Block{BlockNumber: 1, Type: ct.Name(), Record: record}, "doc")
	if err != nil {
		t.Fatalf("blockToProto: %v", err)
	}
	if out.GetPresentation() != nil || out.GetMarkdown() != "hello" {
		t.Fatalf("expected markdown fallback, got %v / %q", out.GetPresentation(), out.GetMarkdown())
	}
}
