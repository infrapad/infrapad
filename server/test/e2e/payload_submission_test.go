package e2e

// One API lifecycle scenario for producer-driven payload submission with
// the Slack/AI-agent slice: initial, identical, and changed snapshots with
// grouped records, stable block identities, and revision/no-op behavior,
// followed by a failed refresh that leaves the cached document unchanged.
// Generic rejection cases live in invalid_data_test.go.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

func TestPayloadSubmissionLifecycle(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.NewClient(grpcAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer conn.Close()
	client := pb.NewInfrapadServiceClient(conn)

	created, err := client.CreateDocument(ctx, &pb.CreateDocumentRequest{
		Title:  "payload submission",
		Blocks: []*pb.Block{{Type: "Core/Markdown", Record: contentRecord(t, map[string]any{"text": "notes"})}},
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	docName := created.GetDocument().GetName()
	thread, err := client.AddBlock(ctx, &pb.AddBlockRequest{Parent: docName, Block: &pb.Block{
		Type:   "Slack/Thread",
		Record: contentRecord(t, map[string]any{"url": "https://slack.example/archives/C1/p" + slackID(0)}),
	}})
	if err != nil {
		t.Fatalf("AddBlock thread: %v", err)
	}
	threadNumber := thread.GetBlock().GetBlockNumber()

	submit := func(messages ...map[string]any) (*pb.Document, error) {
		t.Helper()
		resp, err := client.SubmitBlockPayload(ctx, &pb.SubmitBlockPayloadRequest{
			Parent: docName, BlockNumber: threadNumber, Payload: slackSnapshot(t, messages...),
		})
		return resp.GetDocument(), err
	}
	m1 := slackMessage(1, "U1", "Alice", "first")
	m2 := slackMessage(2, "U2", "Bob", "second")
	m3 := slackMessage(3, "U3", "", "agent analysis")
	m4 := slackMessage(4, "U1", "Alice", "fourth")

	// Initial snapshot: the agent message splits the conversation; the
	// response is the complete document in chronological order.
	doc, err := submit(m1, m2, m3, m4)
	if err != nil {
		t.Fatalf("SubmitBlockPayload: %v", err)
	}
	expectChronological(t, doc)
	generated := generatedBlocks(doc, threadNumber)
	if len(generated) != 3 {
		t.Fatalf("expected 3 generated blocks, got %d: %v", len(generated), doc.GetBlocks())
	}
	conv1, agent, conv2 := generated[0], generated[1], generated[2]
	expectItems(t, conv1, "Slack/Conversation", 1, 2)
	if agent.GetType() != "AIAgent/Message" || agent.GetRecord().AsMap()["key"] != "slack-message:"+slackTS(3) {
		t.Fatalf("unexpected agent block: %v", agent)
	}
	if recordContent(agent)["agent"] != "investigation-agent" {
		t.Fatalf("unexpected agent content: %v", recordContent(agent))
	}
	expectItems(t, conv2, "Slack/Conversation", 4)
	revisions := blockRevisions(doc)

	// An identical snapshot returns the unchanged document.
	doc, err = submit(m1, m2, m3, m4)
	if err != nil {
		t.Fatalf("identical SubmitBlockPayload: %v", err)
	}
	if got := blockRevisions(doc); !equalRevisions(got, revisions) {
		t.Fatalf("identical snapshot changed revisions: %v -> %v", revisions, got)
	}

	// A changed snapshot revises only changed groups and keeps identities.
	m2b := slackMessage(2, "U2", "Bob", "second (edited)")
	m5 := slackMessage(5, "U2", "Bob", "fifth")
	doc, err = submit(m1, m2b, m3, m4, m5)
	if err != nil {
		t.Fatalf("changed SubmitBlockPayload: %v", err)
	}
	expectChronological(t, doc)
	revisions = blockRevisions(doc)
	want := map[int32]int32{1: 1, threadNumber: 1, conv1.GetBlockNumber(): 2, agent.GetBlockNumber(): 1, conv2.GetBlockNumber(): 2}
	if !equalRevisions(revisions, want) {
		t.Fatalf("unexpected revisions after change: %v, want %v", revisions, want)
	}
	for _, blk := range doc.GetBlocks() {
		switch blk.GetBlockNumber() {
		case conv1.GetBlockNumber():
			expectItems(t, blk, "Slack/Conversation", 1, 2)
		case conv2.GetBlockNumber():
			expectItems(t, blk, "Slack/Conversation", 4, 5)
		}
	}
	history, err := client.ListBlockHistory(ctx, &pb.ListBlockHistoryRequest{Parent: docName, BlockNumber: conv1.GetBlockNumber()})
	if err != nil {
		t.Fatalf("ListBlockHistory: %v", err)
	}
	if n := len(history.GetBlocks()); n != 2 {
		t.Fatalf("expected 2 revisions of the conversation, got %d", n)
	}
	for _, rev := range history.GetBlocks() {
		expectItems(t, rev, "Slack/Conversation", 1, 2)
	}

	// A failed refresh must leave the previously cached document readable,
	// with its content and revisions unchanged.
	unchanged := func(label string) {
		t.Helper()
		resp, err := client.GetDocument(ctx, &pb.GetDocumentRequest{Name: docName})
		if err != nil {
			t.Fatalf("%s: GetDocument: %v", label, err)
		}
		if !proto.Equal(resp.GetDocument(), doc) {
			t.Fatalf("%s: document changed: got %v, want %v", label, resp.GetDocument(), doc)
		}
	}
	_, err = client.SubmitBlockPayload(ctx, &pb.SubmitBlockPayloadRequest{
		Parent: docName, BlockNumber: threadNumber,
		Payload: mustStruct(t, map[string]any{"messages": []any{map[string]any{"ts": "not-a-ts", "text": "t"}}}),
	})
	expectRejection(t, err, codes.InvalidArgument, "INVALID_PAYLOAD", "payload.messages.0.ts")
	unchanged("invalid payload semantics")

	// Identical HTTP submissions have the same no-op behavior as gRPC.
	url := fmt.Sprintf("%s/v1/%s/blocks/%d:submitPayload", httpBaseURL(), docName, threadNumber)
	var httpResp struct {
		Document struct {
			Blocks []struct {
				BlockNumber int32 `json:"blockNumber"`
			} `json:"blocks"`
		} `json:"document"`
	}
	httpDo(t, "POST", url, map[string]any{"payload": slackSnapshot(t, m1, m2b, m3, m4, m5).AsMap()}, &httpResp)
	if len(httpResp.Document.Blocks) != len(revisions) {
		t.Fatalf("HTTP submission returned %d blocks, want %d", len(httpResp.Document.Blocks), len(revisions))
	}
	unchanged("identical HTTP submission")
}

// generatedBlocks returns the live direct children of parent in document
// order.
func generatedBlocks(doc *pb.Document, parent int32) []*pb.Block {
	var out []*pb.Block
	for _, blk := range doc.GetBlocks() {
		if blk.ParentBlockNumber != nil && blk.GetParentBlockNumber() == parent && blk.GetStatus() != "deleted" {
			out = append(out, blk)
		}
	}
	return out
}

// expectItems checks a grouped record's type and member keys (by minute).
func expectItems(t *testing.T, blk *pb.Block, typ string, minutes ...int) {
	t.Helper()
	if blk.GetType() != typ {
		t.Fatalf("block %d: expected type %s, got %s", blk.GetBlockNumber(), typ, blk.GetType())
	}
	var got, want []string
	for _, item := range recordItems(blk) {
		key, _ := item["key"].(string)
		got = append(got, key)
	}
	for _, minute := range minutes {
		want = append(want, "slack-message:"+slackTS(minute))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("block %d: expected members %v, got %v", blk.GetBlockNumber(), want, got)
	}
}

func expectChronological(t *testing.T, doc *pb.Document) {
	t.Helper()
	blocks := doc.GetBlocks()
	ok := sort.SliceIsSorted(blocks, func(i, j int) bool {
		a, b := recordTimestamp(t, blocks[i]), recordTimestamp(t, blocks[j])
		if !a.Equal(b) {
			return a.Before(b)
		}
		return blocks[i].GetBlockNumber() < blocks[j].GetBlockNumber()
	})
	if !ok {
		t.Fatalf("document blocks are not in chronological order")
	}
}

func blockRevisions(doc *pb.Document) map[int32]int32 {
	out := map[int32]int32{}
	for _, blk := range doc.GetBlocks() {
		out[blk.GetBlockNumber()] = blk.GetRevisionNumber()
	}
	return out
}

func equalRevisions(a, b map[int32]int32) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
