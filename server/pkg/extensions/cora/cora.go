// Package cora implements the Cora integration: Cora/Report references,
// one per Cora report, expanded from transient report payloads into a
// shared AIAgent/Message child carrying the verbatim report Markdown.
package cora

import (
	_ "embed"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
)

//go:embed schema.cue
var schemaCUE []byte

const (
	ReportTypeName = "Cora/Report"
	// AgentName is the attribution of the generated agent messages.
	AgentName = "Cora"
)

// Report is the inner content of a Cora/Report primary record: the
// reference to one Cora report. The report body is never stored here; it
// lives only in the generated AIAgent/Message child.
type Report struct {
	// ReportID is the authoritative Cora report identifier.
	ReportID string `json:"report_id"`
	// URL is the optional absolute HTTP(S) source link supplied by the
	// producer. It is omitted when unavailable; an empty value is never
	// valid.
	URL string `json:"url,omitempty"`
}

// Payload is the transient input of a Cora/Report expansion: the complete
// current state of one report. It is never persisted.
type Payload struct {
	// ReportID must match the referenced report.
	ReportID string `json:"report_id"`
	// Timestamp is the nonzero source time of the report; it becomes the
	// event time of both the reference and the generated message.
	Timestamp time.Time `json:"timestamp"`
	// Text is the verbatim report Markdown.
	Text string `json:"text"`
}

// ContentTypes returns the Cora content types. agentType is the registered
// AIAgent/Message implementation used for the emitted report message; it
// must be the instance registered in the host registry (it is not included
// in the returned types).
func ContentTypes(agentType *aiagent.MessageType) []content.ContentType {
	return []content.ContentType{NewReportType(agentType)}
}
