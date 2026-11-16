package e2e

import (
	"context"
	"strings"
	"testing"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/infrapad/infrapad/server/internal/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Both human and mapped-agent messages must carry ordinary Markdown, not
// Slack mrkdwn requiring integration-specific rendering in the UI.
func TestSlackFormatting(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.NewClient(grpcAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := pb.NewInfrapadServiceClient(conn)
	created, err := client.CreateDocument(ctx, &pb.CreateDocumentRequest{Title: "Slack formatting regression"})
	if err != nil {
		t.Fatal(err)
	}
	docName := created.GetDocument().GetName()
	thread, err := client.AddBlock(ctx, &pb.AddBlockRequest{Parent: docName, Block: &pb.Block{
		Type:   "Slack/Thread",
		Record: contentRecord(t, map[string]any{"url": "https://slack.example/archives/C1/p" + slackID(0)}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	text := utils.Dedent(`
		*Two distinctions:*
		• Check ` + "`compliance-monkey`" + `.
		• Use *subnet pair and size*.

		First line
		Second line

		*Next evidence:*
		1. *Confirm scope:* Check the cluster.
		2. *Trace ` + "`serving-42`" + `:* Inspect labels.
	`)
	want := utils.Dedent(`
		**Two distinctions:**
		- Check ` + "`compliance-monkey`" + `.
		- Use **subnet pair and size**.

		First line` + "  " + `
		Second line

		**Next evidence:**
		1. **Confirm scope:** Check the cluster.
		2. **Trace ` + "`serving-42`" + `:** Inspect labels.
	`)
	// Raw text and producer-resolved readable_text take the same path.
	human := slackMessage(1, "U1", "Alice", text)
	human["previews"] = []any{map[string]any{
		"title": "Incident", "title_link": "https://example.com/incident", "product_name": "PagerDuty",
		"updated_at": "2026-11-16T12:22:42Z",
		"text":       "Preview metadata", "fields": []any{map[string]any{"label": "Status", "value": "Resolved"}},
	}}
	wantHuman := want + "\n\n> Attachment: **[Incident](https://example.com/incident)** · PagerDuty\n> Preview metadata\n> **Status:** Resolved\n> Updated: 2026-11-16 12:22:42 UTC"
	followup := slackMessage(2, "U1", "Alice", "A follow-up from Alice.")
	agent := slackMessage(3, "U3", "", "raw fallback")
	agent["readable_text"] = text
	submitted, err := client.SubmitBlockPayload(ctx, &pb.SubmitBlockPayloadRequest{
		Parent: docName, BlockNumber: thread.GetBlock().GetBlockNumber(), Payload: slackPayload(t, human, followup, agent),
	})
	if err != nil {
		t.Fatal(err)
	}
	blocks := generatedBlocks(submitted.GetDocument(), thread.GetBlock().GetBlockNumber())
	if len(blocks) != 2 {
		t.Fatalf("got %d generated blocks, want human and agent", len(blocks))
	}
	for _, block := range blocks {
		var got string
		if block.GetType() == "AIAgent/Message" {
			got, _ = recordContent(block)["text"].(string)
			if got != want || block.GetMarkdown() != want || presentationCards(t, block)[0].Markdown != want {
				t.Fatalf("agent Markdown not normalized: %v", block)
			}
		} else {
			items := block.GetRecord().AsMap()["items"].([]any)
			if len(items) != 2 || items[1].(map[string]any)["content"].(map[string]any)["text"] != "A follow-up from Alice." {
				t.Fatalf("presentation grouping changed stored messages: %v", items)
			}
			card := presentationCards(t, block)[0]
			if strings.Count(card.Markdown, "- **Alice**") != 1 ||
				!strings.Contains(card.Markdown, "\n\n  A follow-up from Alice.\n") || len(card.Fields) != 0 {
				t.Fatalf("consecutive Alice messages not grouped in presentation: %v", card)
			}
			if strings.Count(block.GetMarkdown(), "- **Alice**") != 2 {
				t.Fatalf("export lost individual attribution: %s", block.GetMarkdown())
			}
			got = items[0].(map[string]any)["content"].(map[string]any)["text"].(string)
			if got != wantHuman {
				t.Fatalf("human Markdown = %q, want %q", got, wantHuman)
			}
			if !strings.Contains(block.GetMarkdown(), "> **Status:** Resolved") {
				t.Fatalf("export lost attachment metadata: %s", block.GetMarkdown())
			}
			if !strings.Contains(card.Markdown, "  - [Incident](https://example.com/incident)") ||
				strings.Contains(card.Markdown, "Attachment:") || strings.Contains(card.Markdown, "Preview metadata") ||
				strings.Contains(card.Markdown, "**Status:**") || strings.Contains(card.Markdown, "Updated:") {
				t.Fatalf("attachment presentation is not links only: %s", card.Markdown)
			}
			if !strings.Contains(presentationCards(t, block)[0].Markdown, "  First line  \n  Second line") {
				t.Fatalf("conversation presentation lost hard break: %v", block)
			}
		}
	}
	t.Logf("Verified %s", docName)
}
