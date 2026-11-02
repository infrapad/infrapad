// Handwritten placeholder for the generated inner-content schema of the
// AIAgent extension. Host record metadata (key, timestamp) is not part of it.
// TODO(cue-schema-generation): generate from the Go definitions.
package aiagent

#Message: close({
	agent:   string
	text:    string
	extras?: {[string]: _}
})
