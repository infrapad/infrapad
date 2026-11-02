// Handwritten placeholder for the generated schemas of the Slack extension:
// inner content values (#ThreadReference, #Message) and the transient
// ingestion payload (#Snapshot). Host record metadata is not part of them.
// TODO(cue-schema-generation): generate from the Go definitions.
package slack

#ThreadReference: close({
	url: string
})

#Message: close({
	author: string
	text:   string
})

#SourceMessage: close({
	id:        string
	timestamp: string
	author:    string
	text:      string
	agent?:    bool
})

#Snapshot: close({
	messages: [...#SourceMessage]
})
