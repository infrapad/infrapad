package markdown

import (
	"fmt"
	"strings"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"gopkg.in/yaml.v3"
)

// RemoteBlock holds the server-side representation of a single block,
// converted from the protobuf type into plain Go maps for easy comparison.
type RemoteBlock struct {
	BlockNumber    int
	RevisionNumber int
	Type           string
	AuthorID       string
	Content        map[string]any
}

// RemoteDoc is the server-side counterpart of ParsedDoc. It holds document
// metadata and the list of blocks fetched from the server, converted from
// protobuf types into plain Go values.
type RemoteDoc struct {
	DocID  string
	Blocks []RemoteBlock
}

// NewRemoteDoc builds a RemoteDoc from the protobuf blocks returned by the
// server. docID is the short document identifier (without the "documents/" prefix).
func NewRemoteDoc(docID string, blocks []*pb.Block) *RemoteDoc {
	rd := &RemoteDoc{DocID: docID}
	for _, b := range blocks {
		var content map[string]any
		if b.GetContent() != nil {
			content = b.GetContent().AsMap()
		}
		rd.Blocks = append(rd.Blocks, RemoteBlock{
			BlockNumber:    int(b.GetBlockNumber()),
			RevisionNumber: int(b.GetRevisionNumber()),
			Type:           b.GetType(),
			AuthorID:       b.GetAuthorId(),
			Content:        content,
		})
	}
	return rd
}

// blockByNumber returns the remote block with the given number, or nil.
func (rd *RemoteDoc) blockByNumber(n int) *RemoteBlock {
	for i := range rd.Blocks {
		if rd.Blocks[i].BlockNumber == n {
			return &rd.Blocks[i]
		}
	}
	return nil
}

// SyncAction describes a single block that needs to be pushed to the server.
type SyncAction struct {
	Block *ParsedBlock
	IsNew bool
}

// DiffBlocks compares a locally-parsed document against the remote server
// state and returns the list of blocks that need to be pushed (new blocks
// and existing blocks whose content has changed).
func DiffBlocks(local *ParsedDoc, remote *RemoteDoc) ([]SyncAction, error) {
	var actions []SyncAction

	for i := range local.Blocks {
		lb := &local.Blocks[i]

		if lb.Meta.IsNew {
			actions = append(actions, SyncAction{Block: lb, IsNew: true})
			continue
		}

		rb := remote.blockByNumber(lb.Meta.BlockNumber)
		if rb == nil {
			return nil, fmt.Errorf("block %d exists locally but not on server", lb.Meta.BlockNumber)
		}

		if blockContentChanged(lb, rb) {
			actions = append(actions, SyncAction{Block: lb, IsNew: false})
		}
	}

	return actions, nil
}

// blockContentChanged compares local parsed block content with the remote block.
func blockContentChanged(local *ParsedBlock, remote *RemoteBlock) bool {
	if local.Meta.Type == "markdown" {
		localText, _ := local.Content["text"].(string)
		serverText, _ := remote.Content["text"].(string)
		// Trim trailing newlines: blank lines between block directives
		// are parsed as part of the preceding block's content but are
		// not semantically significant.
		return strings.TrimRight(localText, "\n") != strings.TrimRight(serverText, "\n")
	}

	// For non-markdown blocks, compare by serializing to YAML to avoid
	// type mismatches (e.g. int vs float64 from protobuf structpb).
	localYAML, err1 := yaml.Marshal(local.Content)
	serverYAML, err2 := yaml.Marshal(remote.Content)
	if err1 != nil || err2 != nil {
		return true // can't compare, assume changed
	}
	return string(localYAML) != string(serverYAML)
}
