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
	agent := slackMessage(2, "U3", "", "raw fallback")
	agent["readable_text"] = text
	submitted, err := client.SubmitBlockPayload(ctx, &pb.SubmitBlockPayloadRequest{
		Parent: docName, BlockNumber: thread.GetBlock().GetBlockNumber(), Payload: slackPayload(t, human, agent),
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
			got = items[0].(map[string]any)["content"].(map[string]any)["text"].(string)
			if got != want {
				t.Fatalf("human Markdown = %q, want %q", got, want)
			}
			if !strings.Contains(presentationCards(t, block)[0].Markdown, "  First line  \n  Second line") {
				t.Fatalf("conversation presentation lost hard break: %v", block)
			}
		}
	}
	t.Logf("Verified %s", docName)
}
