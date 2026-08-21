package markdown

import (
	"testing"
)

func TestDiffBlocks_NoChanges(t *testing.T) {
	local := &ParsedDoc{
		Meta: DocMeta{DocID: "test-doc"},
		Blocks: []ParsedBlock{
			{
				Meta:    BlockMeta{Type: "markdown", BlockNumber: 1},
				Content: map[string]any{"text": "Hello world\n"},
			},
			{
				Meta:    BlockMeta{Type: "markdown", BlockNumber: 2},
				Content: map[string]any{"text": "Second block\n"},
			},
		},
	}

	remote := &RemoteDoc{
		DocID: "test-doc",
		Blocks: []RemoteBlock{
			{BlockNumber: 1, Type: "markdown", Content: map[string]any{"text": "Hello world\n"}},
			{BlockNumber: 2, Type: "markdown", Content: map[string]any{"text": "Second block\n"}},
		},
	}

	actions, err := DiffBlocks(local, remote)
	if err != nil {
		t.Fatalf("DiffBlocks: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(actions))
	}
}

func TestDiffBlocks_ModifiedBlock(t *testing.T) {
	local := &ParsedDoc{
		Meta: DocMeta{DocID: "test-doc"},
		Blocks: []ParsedBlock{
			{
				Meta:    BlockMeta{Type: "markdown", BlockNumber: 1},
				Content: map[string]any{"text": "Hello world\n"},
			},
			{
				Meta:    BlockMeta{Type: "markdown", BlockNumber: 2},
				Content: map[string]any{"text": "Updated content\n"},
			},
		},
	}

	remote := &RemoteDoc{
		DocID: "test-doc",
		Blocks: []RemoteBlock{
			{BlockNumber: 1, Type: "markdown", Content: map[string]any{"text": "Hello world\n"}},
			{BlockNumber: 2, Type: "markdown", Content: map[string]any{"text": "Second block\n"}},
		},
	}

	actions, err := DiffBlocks(local, remote)
	if err != nil {
		t.Fatalf("DiffBlocks: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].IsNew {
		t.Error("expected existing block update, got new")
	}
	if actions[0].Block.Meta.BlockNumber != 2 {
		t.Errorf("expected block 2, got %d", actions[0].Block.Meta.BlockNumber)
	}
}

func TestDiffBlocks_NewBlock(t *testing.T) {
	local := &ParsedDoc{
		Meta: DocMeta{DocID: "test-doc"},
		Blocks: []ParsedBlock{
			{
				Meta:    BlockMeta{Type: "markdown", BlockNumber: 1},
				Content: map[string]any{"text": "Hello world\n"},
			},
			{
				Meta:    BlockMeta{Type: "markdown", IsNew: true},
				Content: map[string]any{"text": "Brand new block\n"},
			},
		},
	}

	remote := &RemoteDoc{
		DocID: "test-doc",
		Blocks: []RemoteBlock{
			{BlockNumber: 1, Type: "markdown", Content: map[string]any{"text": "Hello world\n"}},
		},
	}

	actions, err := DiffBlocks(local, remote)
	if err != nil {
		t.Fatalf("DiffBlocks: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if !actions[0].IsNew {
		t.Error("expected new block, got update")
	}
}

func TestDiffBlocks_TrailingNewlineIgnored(t *testing.T) {
	local := &ParsedDoc{
		Meta: DocMeta{DocID: "test-doc"},
		Blocks: []ParsedBlock{
			{
				Meta:    BlockMeta{Type: "markdown", BlockNumber: 1},
				Content: map[string]any{"text": "Hello world\n\n"},
			},
		},
	}

	remote := &RemoteDoc{
		DocID: "test-doc",
		Blocks: []RemoteBlock{
			{BlockNumber: 1, Type: "markdown", Content: map[string]any{"text": "Hello world\n"}},
		},
	}

	actions, err := DiffBlocks(local, remote)
	if err != nil {
		t.Fatalf("DiffBlocks: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("trailing newline difference should not trigger push, got %d actions", len(actions))
	}
}

func TestDiffBlocks_NonMarkdownChanged(t *testing.T) {
	local := &ParsedDoc{
		Meta: DocMeta{DocID: "test-doc"},
		Blocks: []ParsedBlock{
			{
				Meta: BlockMeta{Type: "alerts_matcher", BlockNumber: 1},
				Content: map[string]any{
					"LabelsMatchers": []any{
						map[string]any{"name": []any{"CrashLoopBackOff"}},
					},
				},
			},
		},
	}

	remote := &RemoteDoc{
		DocID: "test-doc",
		Blocks: []RemoteBlock{
			{
				BlockNumber: 1, Type: "alerts_matcher",
				Content: map[string]any{
					"LabelsMatchers": []any{
						map[string]any{"name": []any{"CrashLoopBackOff"}},
						map[string]any{"name": []any{"KubeNodeNotReady"}},
					},
				},
			},
		},
	}

	actions, err := DiffBlocks(local, remote)
	if err != nil {
		t.Fatalf("DiffBlocks: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Block.Meta.BlockNumber != 1 {
		t.Errorf("expected block 1, got %d", actions[0].Block.Meta.BlockNumber)
	}
}

func TestDiffBlocks_NonMarkdownUnchanged(t *testing.T) {
	content := map[string]any{
		"LabelsMatchers": []any{
			map[string]any{"name": []any{"CrashLoopBackOff"}},
		},
	}

	local := &ParsedDoc{
		Meta: DocMeta{DocID: "test-doc"},
		Blocks: []ParsedBlock{
			{
				Meta:    BlockMeta{Type: "alerts_matcher", BlockNumber: 1},
				Content: content,
			},
		},
	}

	remote := &RemoteDoc{
		DocID: "test-doc",
		Blocks: []RemoteBlock{
			{BlockNumber: 1, Type: "alerts_matcher", Content: content},
		},
	}

	actions, err := DiffBlocks(local, remote)
	if err != nil {
		t.Fatalf("DiffBlocks: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(actions))
	}
}

func TestDiffBlocks_MissingServerBlock(t *testing.T) {
	local := &ParsedDoc{
		Meta: DocMeta{DocID: "test-doc"},
		Blocks: []ParsedBlock{
			{
				Meta:    BlockMeta{Type: "markdown", BlockNumber: 99},
				Content: map[string]any{"text": "orphan\n"},
			},
		},
	}

	remote := &RemoteDoc{
		DocID:  "test-doc",
		Blocks: nil,
	}

	_, err := DiffBlocks(local, remote)
	if err == nil {
		t.Fatal("expected error for missing server block")
	}
}

func TestDiffBlocks_MixedActions(t *testing.T) {
	local := &ParsedDoc{
		Meta: DocMeta{DocID: "test-doc"},
		Blocks: []ParsedBlock{
			{
				Meta:    BlockMeta{Type: "markdown", BlockNumber: 1},
				Content: map[string]any{"text": "Unchanged\n"},
			},
			{
				Meta:    BlockMeta{Type: "markdown", BlockNumber: 2},
				Content: map[string]any{"text": "Modified\n"},
			},
			{
				Meta:    BlockMeta{Type: "markdown", IsNew: true},
				Content: map[string]any{"text": "New block\n"},
			},
		},
	}

	remote := &RemoteDoc{
		DocID: "test-doc",
		Blocks: []RemoteBlock{
			{BlockNumber: 1, Type: "markdown", Content: map[string]any{"text": "Unchanged\n"}},
			{BlockNumber: 2, Type: "markdown", Content: map[string]any{"text": "Original\n"}},
		},
	}

	actions, err := DiffBlocks(local, remote)
	if err != nil {
		t.Fatalf("DiffBlocks: %v", err)
	}
	if len(actions) != 2 {
		t.Fatalf("expected 2 actions (1 update + 1 new), got %d", len(actions))
	}

	// First action: update block 2
	if actions[0].IsNew || actions[0].Block.Meta.BlockNumber != 2 {
		t.Errorf("action[0]: expected update of block 2, got isNew=%v block=%d",
			actions[0].IsNew, actions[0].Block.Meta.BlockNumber)
	}

	// Second action: new block
	if !actions[1].IsNew {
		t.Error("action[1]: expected new block")
	}
}
