package e2e

// One API lifecycle scenario for producer-driven payload submission with
// the Slack/AI-agent slice: initial, identical, and changed payloads with
// grouped records, stable block identities, and revision/no-op behavior,
// followed by a failed refresh that leaves the cached document unchanged.
// The thread timestamp starts server-assigned and is then inferred from the
// earliest message of every non-empty payload (authoritative refreshes,
// empty payloads preserve it); explicit content timestamps are rejected.
// Generic rejection cases live in invalid_data_test.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/infrapad/infrapad/server/pkg/content"
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
	threadURL := "https://slack.example/archives/C1/p" + slackID(0)
	thread, err := client.AddBlock(ctx, &pb.AddBlockRequest{Parent: docName, Block: &pb.Block{
		Type:   "Slack/Thread",
		Record: contentRecord(t, map[string]any{"url": threadURL}),
	}})
	if err != nil {
		t.Fatalf("AddBlock thread: %v", err)
	}
	threadNumber := thread.GetBlock().GetBlockNumber()
	// Until a payload arrives, the reference keeps its creation time.
	if got, notes := recordTimestamp(t, thread.GetBlock()), recordTimestamp(t, created.GetDocument().GetBlocks()[0]); got.Before(notes) {
		t.Fatalf("thread timestamp %s, want the server-assigned creation time (after %s)", got, notes)
	}
	// threadAt asserts the thread timestamp and that it sorts first.
	threadAt := func(doc *pb.Document, minute int) {
		t.Helper()
		expectChronological(t, doc)
		first := doc.GetBlocks()[0]
		if want := slackOrigin.Add(time.Duration(minute) * time.Minute); first.GetBlockNumber() != threadNumber ||
			!recordTimestamp(t, first).Equal(want) {
			t.Fatalf("expected thread %d first at %s, got %v", threadNumber, want, first)
		}
	}

	submit := func(messages ...map[string]any) (*pb.Document, error) {
		t.Helper()
		resp, err := client.SubmitBlockPayload(ctx, &pb.SubmitBlockPayloadRequest{
			Parent: docName, BlockNumber: threadNumber, Payload: slackPayload(t, messages...),
		})
		return resp.GetDocument(), err
	}
	m1 := slackMessage(1, "U1", "Alice", "first")
	m2 := slackMessage(2, "U2", "Bob", "second")
	m3 := slackMessage(3, "U3", "", "agent analysis")
	m4 := slackMessage(4, "U1", "Alice", "fourth")

	// Initial payload: the agent message splits the conversation; the
	// response is the complete document in chronological order.
	doc, err := submit(m1, m2, m3, m4)
	if err != nil {
		t.Fatalf("SubmitBlockPayload: %v", err)
	}
	// The reference takes its earliest message time and sorts before the
	// tied first conversation (lower block number) and the newer notes.
	threadAt(doc, 1)
	if second := doc.GetBlocks()[1]; second.GetParentBlockNumber() != threadNumber ||
		!recordTimestamp(t, second).Equal(recordTimestamp(t, doc.GetBlocks()[0])) {
		t.Fatalf("expected the tied first conversation after the reference: %v", doc.GetBlocks())
	}
	if doc.GetBlocks()[0].GetRevisionNumber() != 2 {
		t.Fatalf("inferred timestamp should revise the primary: %v", doc.GetBlocks()[0])
	}
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
	expectPresentation(t, doc, threadNumber, conv1, agent)
	revisions := blockRevisions(doc)

	// An identical payload returns the unchanged document.
	doc, err = submit(m1, m2, m3, m4)
	if err != nil {
		t.Fatalf("identical SubmitBlockPayload: %v", err)
	}
	if got := blockRevisions(doc); !equalRevisions(got, revisions) {
		t.Fatalf("identical payload changed revisions: %v -> %v", revisions, got)
	}

	// A changed payload revises only changed groups and keeps identities.
	m2b := slackMessage(2, "U2", "Bob", "second (edited)")
	m5 := slackMessage(5, "U2", "Bob", "fifth")
	doc, err = submit(m1, m2b, m3, m4, m5)
	if err != nil {
		t.Fatalf("changed SubmitBlockPayload: %v", err)
	}
	threadAt(doc, 1)
	revisions = blockRevisions(doc)
	want := map[int32]int32{1: 1, threadNumber: 2, conv1.GetBlockNumber(): 2, agent.GetBlockNumber(): 1, conv2.GetBlockNumber(): 2}
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
	// Historical revisions render their own records, not the latest.
	if md := history.GetBlocks()[0].GetMarkdown(); !strings.Contains(md, "  second\n") || strings.Contains(md, "edited") {
		t.Fatalf("revision 1 markdown does not render the historical record:\n%s", md)
	}
	if md := history.GetBlocks()[1].GetMarkdown(); !strings.Contains(md, "  second (edited)\n") {
		t.Fatalf("revision 2 markdown does not render the latest record:\n%s", md)
	}
	if card := presentationCards(t, history.GetBlocks()[0])[0]; strings.Contains(card.Markdown, "edited") {
		t.Fatalf("revision 1 presentation renders the latest record: %v", card)
	}

	expectMarkdownExport(t, client, docName, threadNumber, conv1, agent, conv2)

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
	httpDo(t, "POST", url, map[string]any{"payload": slackPayload(t, m1, m2b, m3, m4, m5).AsMap()}, &httpResp)
	if len(httpResp.Document.Blocks) != len(revisions) {
		t.Fatalf("HTTP submission returned %d blocks, want %d", len(httpResp.Document.Blocks), len(revisions))
	}
	unchanged("identical HTTP submission")

	// Ordinary edits preserve the inferred timestamp; direct host-timestamp
	// changes and the removed content.timestamp field are rejected.
	edited, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
		Parent: docName, BlockNumber: threadNumber,
		Block: &pb.Block{Type: "Slack/Thread", Record: contentRecord(t, map[string]any{"url": threadURL})},
	})
	if err != nil {
		t.Fatalf("UpdateBlock thread: %v", err)
	}
	if got, want := recordTimestamp(t, edited.GetBlock()), slackOrigin.Add(time.Minute); !got.Equal(want) {
		t.Fatalf("ordinary edit changed the event time: %s, want %s", got, want)
	}
	_, err = client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
		Parent: docName, BlockNumber: threadNumber,
		Block: &pb.Block{Type: "Slack/Thread", Record: timestampedRecord(t, map[string]any{"url": threadURL}, slackOrigin)},
	})
	requireCode(t, err, codes.InvalidArgument, "direct host-timestamp change")
	_, err = client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
		Parent: docName, BlockNumber: threadNumber,
		Block: &pb.Block{Type: "Slack/Thread", Record: contentRecord(t, map[string]any{
			"url": threadURL, "timestamp": slackOrigin.Format(time.RFC3339Nano),
		})},
	})
	requireCode(t, err, codes.InvalidArgument, "removed content.timestamp")

	// The HTTP export route returns JSON containing the same Markdown.
	exported, err := client.ExportDocumentMarkdown(ctx, &pb.ExportDocumentMarkdownRequest{Name: docName})
	if err != nil {
		t.Fatalf("ExportDocumentMarkdown: %v", err)
	}
	var httpExport struct {
		Markdown string `json:"markdown"`
	}
	httpDo(t, "GET", fmt.Sprintf("%s/v1/%s/markdown", httpBaseURL(), docName), nil, &httpExport)
	if httpExport.Markdown != exported.GetMarkdown() {
		t.Fatalf("HTTP export differs from gRPC export:\n%s\nvs\n%s", httpExport.Markdown, exported.GetMarkdown())
	}

	expectMarkdownPush(t, client, docName, threadNumber, exported.GetMarkdown())
	expectOutputOnlyEchoIgnored(t, client, docName)

	// Refreshes are authoritative: without the parent the reference moves
	// to the earliest reply; an earlier message moves it back. An empty
	// payload removes the children and keeps the current timestamp.
	if doc, err = submit(m2b, m3, m4, m5); err != nil {
		t.Fatalf("SubmitBlockPayload without the parent: %v", err)
	}
	threadAt(doc, 2)
	if doc, err = submit(slackMessage(0, "U1", "Alice", "zeroth"), m2b, m3, m4, m5); err != nil {
		t.Fatalf("SubmitBlockPayload with an earlier message: %v", err)
	}
	threadAt(doc, 0)
	if doc, err = submit(); err != nil {
		t.Fatalf("empty SubmitBlockPayload: %v", err)
	}
	threadAt(doc, 0)
	if n := len(generatedBlocks(doc, threadNumber)); n != 0 {
		t.Fatalf("empty payload kept %d generated blocks", n)
	}
}

// expectPresentation checks the output-only presentation and body-only
// Markdown of the initial Slack/agent output: JSON cards from the
// registered presenters and Markdown fallback for Core/Markdown.
func expectPresentation(t *testing.T, doc *pb.Document, threadNumber int32, conv, agent *pb.Block) {
	t.Helper()
	blocks := map[int32]*pb.Block{}
	for _, blk := range doc.GetBlocks() {
		blocks[blk.GetBlockNumber()] = blk
	}
	notes := blocks[1]
	if notes.GetPresentation() != nil || notes.GetMarkdown() != "notes" {
		t.Fatalf("Core/Markdown: expected markdown fallback only, got %v / %q", notes.GetPresentation(), notes.GetMarkdown())
	}

	threadURL := "https://slack.example/archives/C1/p" + slackID(0)
	thread := blocks[threadNumber]
	want := mustStruct(t, map[string]any{"cards": []any{map[string]any{
		"title": "Slack thread",
		"links": []any{map[string]any{"label": "Open in Slack", "url": threadURL}},
	}}})
	if !proto.Equal(thread.GetPresentation(), want) {
		t.Fatalf("Slack/Thread presentation: got %v, want %v", thread.GetPresentation(), want)
	}
	if thread.GetMarkdown() != "[Slack thread]("+threadURL+")\n" {
		t.Fatalf("Slack/Thread markdown: %q", thread.GetMarkdown())
	}

	cards := presentationCards(t, conv)
	if len(cards) != 1 || cards[0].Title != "Slack conversation" ||
		len(cards[0].Fields) != 1 || cards[0].Fields[0].Label != "Messages" || cards[0].Fields[0].Value != "2" {
		t.Fatalf("Slack/Conversation presentation: %v", conv.GetPresentation())
	}
	md := conv.GetMarkdown()
	if !strings.HasPrefix(md, "- **Alice**") || !strings.Contains(md, "  first\n") || !strings.Contains(md, "  second\n") ||
		strings.Contains(md, "::infrapad_block") || strings.Contains(md, "```yaml") {
		t.Fatalf("Slack/Conversation markdown is not the grouped body only:\n%s", md)
	}
	if cards[0].Markdown != md {
		t.Fatalf("Slack/Conversation card markdown differs from body:\n%s\nvs\n%s", cards[0].Markdown, md)
	}

	cards = presentationCards(t, agent)
	if len(cards) != 1 || cards[0].Title != "investigation-agent" || cards[0].Markdown != "agent analysis" ||
		len(cards[0].Links) != 1 || cards[0].Links[0].Label != "Open source" {
		t.Fatalf("AIAgent/Message presentation: %v", agent.GetPresentation())
	}
	if agent.GetMarkdown() != "agent analysis" {
		t.Fatalf("AIAgent/Message markdown: %q", agent.GetMarkdown())
	}
}

// presentationCards decodes the opaque transport payload using the backend's
// presentation contract, rather than transport-specific card messages.
func presentationCards(t *testing.T, block *pb.Block) []content.Card {
	t.Helper()
	data, err := block.GetPresentation().MarshalJSON()
	if err != nil {
		t.Fatalf("marshal presentation: %v", err)
	}
	var presentation content.Presentation
	if err := json.Unmarshal(data, &presentation); err != nil {
		t.Fatalf("decode presentation: %v", err)
	}
	if len(presentation.Cards) == 0 {
		t.Fatalf("block %d: expected presentation cards", block.GetBlockNumber())
	}
	return presentation.Cards
}

// expectOutputOnlyEchoIgnored echoes a retrieved block with bogus
// output-only presentation/Markdown: the write is accepted and the stored
// content and rendering stay derived from the record.
func expectOutputOnlyEchoIgnored(t *testing.T, client pb.InfrapadServiceClient, docName string) {
	t.Helper()
	ctx := context.Background()
	got, err := client.GetBlock(ctx, &pb.GetBlockRequest{Parent: docName, BlockNumber: 1})
	if err != nil {
		t.Fatalf("GetBlock: %v", err)
	}
	echo := proto.Clone(got.GetBlock()).(*pb.Block)
	echo.Markdown = "bogus"
	echo.Presentation = mustStruct(t, map[string]any{"arbitrary": "ignored", "cards": "not even an array"})
	updated, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{Parent: docName, BlockNumber: 1, Block: echo})
	if err != nil {
		t.Fatalf("UpdateBlock echo: %v", err)
	}
	blk := updated.GetBlock()
	if blk.GetMarkdown() != got.GetBlock().GetMarkdown() || blk.GetPresentation() != nil ||
		!proto.Equal(blk.GetRecord(), got.GetBlock().GetRecord()) {
		t.Fatalf("echoed output-only fields changed the block: %v -> %v", got.GetBlock(), blk)
	}
}

// expectMarkdownPush round-trips the exported Markdown through the push
// API: a no-op push over HTTP, then an ordinary body edit, a header-only
// edit of the Slack thread reference (keeping its inferred timestamp), and a
// new AI-agent message block. A content.timestamp header is rejected.
func expectMarkdownPush(t *testing.T, client pb.InfrapadServiceClient, docName string, threadNumber int32, exported string) {
	t.Helper()
	url := fmt.Sprintf("%s/v1/%s/markdown", httpBaseURL(), docName)
	var noop struct {
		Markdown string `json:"markdown"`
		Changes  []any  `json:"changes"`
	}
	httpDo(t, "POST", url, map[string]any{"markdown": exported}, &noop)
	if noop.Markdown != exported || len(noop.Changes) != 0 {
		t.Fatalf("expected a no-op push, got changes %v:\n%s", noop.Changes, noop.Markdown)
	}

	oldURL := "https://slack.example/archives/C1/p" + slackID(0)
	newURL := "https://slack.example/archives/C1/p" + slackID(9)
	threadTime := slackOrigin.Add(time.Minute).Format(time.RFC3339Nano)
	_, err := client.PushDocumentMarkdown(context.Background(), &pb.PushDocumentMarkdownRequest{Name: docName,
		Markdown: exported + "::infrapad_block{block=new type=Slack/Thread}\n```yaml infrapad\ncontent:\n  url: " + oldURL +
			"\n  timestamp: \"" + threadTime + "\"\n```\n"})
	requireCode(t, err, codes.InvalidArgument, "Markdown content.timestamp")
	edited := strings.Replace(exported, "}\nnotes\n", "}\nnotes (edited)\n", 1)
	edited = strings.Replace(edited, "  url: "+oldURL+"\n", "  url: "+newURL+"\n", 1)
	edited += "::infrapad_block{block=new type=AIAgent/Message}\n```yaml infrapad\ncontent:\n  agent: reviewer\n```\nLooks good.\n"
	resp, err := client.PushDocumentMarkdown(context.Background(), &pb.PushDocumentMarkdownRequest{Name: docName, Markdown: edited})
	if err != nil {
		t.Fatalf("PushDocumentMarkdown: %v", err)
	}
	var changes []string
	var added int32
	for _, change := range resp.GetChanges() {
		changes = append(changes, fmt.Sprintf("%d:%s", change.GetBlockNumber(), change.GetAction()))
		if change.GetAction() == "added" {
			added = change.GetBlockNumber()
		}
	}
	// The reference sorts first by its inferred source time.
	if want := fmt.Sprintf("%d:updated,1:updated,%d:added", threadNumber, added); strings.Join(changes, ",") != want {
		t.Fatalf("unexpected changes %v, want %s", changes, want)
	}
	md := resp.GetMarkdown()
	for _, want := range []string{
		"block=1 rev=2 type=Core/Markdown", "}\nnotes (edited)\n",
		// Header-only edit: the body is regenerated from the new URL.
		fmt.Sprintf("block=%d rev=4 type=Slack/Thread timestamp=%s", threadNumber, threadTime), "[Slack thread](" + newURL + ")",
		fmt.Sprintf("block=%d rev=1 type=AIAgent/Message timestamp=", added), "  agent: reviewer\n", "Looks good.\n",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected %q in refreshed Markdown:\n%s", want, md)
		}
	}
	// Editing generated content is forbidden.
	forbidden := strings.Replace(md, "agent analysis", "agent analysis (edited)", 1)
	_, err = client.PushDocumentMarkdown(context.Background(), &pb.PushDocumentMarkdownRequest{Name: docName, Markdown: forbidden})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for a generated edit, got %v", err)
	}
}

// expectMarkdownExport checks the server-rendered Markdown of the Slack
// lifecycle document: chronological generated output with provenance and
// event times, structured headers for the thread and agent message, and
// conversation bodies without YAML.
func expectMarkdownExport(t *testing.T, client pb.InfrapadServiceClient, docName string, threadNumber int32, conv1, agent, conv2 *pb.Block) {
	t.Helper()
	resp, err := client.ExportDocumentMarkdown(context.Background(), &pb.ExportDocumentMarkdownRequest{Name: docName})
	if err != nil {
		t.Fatalf("ExportDocumentMarkdown: %v", err)
	}
	md := resp.GetMarkdown()
	if !strings.HasPrefix(md, "---\ndocument: "+strings.TrimPrefix(docName, "documents/")+"\ntitle: payload submission\n") {
		t.Fatalf("unexpected frontmatter:\n%s", md)
	}
	// Sections in document order, keyed by the directive's block number.
	sections := map[int32]string{}
	var order []int32
	for _, section := range strings.Split(md, "::infrapad_block{")[1:] {
		var number int32
		if _, err := fmt.Sscanf(section, "block=%d ", &number); err != nil {
			t.Fatalf("unexpected directive %q: %v", section, err)
		}
		sections[number] = section
		order = append(order, number)
	}
	if len(order) != 5 {
		t.Fatalf("expected 5 exported blocks, got %v:\n%s", order, md)
	}
	index := func(n int32) int {
		for i, o := range order {
			if o == n {
				return i
			}
		}
		return -1
	}
	if !(index(conv1.GetBlockNumber()) < index(agent.GetBlockNumber()) && index(agent.GetBlockNumber()) < index(conv2.GetBlockNumber())) {
		t.Fatalf("generated blocks not in chronological order: %v", order)
	}
	at := func(minute int) string {
		return slackOrigin.Add(time.Duration(minute) * time.Minute).Format(time.RFC3339Nano)
	}
	header := "}\n```yaml infrapad\ncontent:\n"
	expectSection := func(n int32, parts ...string) {
		t.Helper()
		for _, part := range parts {
			if !strings.Contains(sections[n], part) {
				t.Fatalf("block %d: expected %q in:\n%s", n, part, sections[n])
			}
		}
	}
	threadURL := "https://slack.example/archives/C1/p" + slackID(0)
	expectSection(threadNumber, "rev=2 type=Slack/Thread timestamp="+at(1)+header+"  url: "+threadURL+"\n```\n[Slack thread]("+threadURL+")\n")
	parent := fmt.Sprintf(" parent=%d timestamp=", threadNumber)
	expectSection(agent.GetBlockNumber(), "type=AIAgent/Message"+parent+at(3)+header,
		"  agent: investigation-agent\n", "  url: ", "```\nagent analysis\n")
	expectSection(conv1.GetBlockNumber(), "rev=2 type=Slack/Conversation"+parent+at(1)+"}\n- **Alice**", "second (edited)")
	expectSection(conv2.GetBlockNumber(), "type=Slack/Conversation"+parent+at(4)+"}\n- **Alice**", "fifth")
	if strings.Contains(sections[conv1.GetBlockNumber()], "```yaml") || strings.Contains(md, "status=") {
		t.Fatalf("unexpected conversation YAML or block status:\n%s", md)
	}
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
