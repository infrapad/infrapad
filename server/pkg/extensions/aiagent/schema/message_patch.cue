// Companion constraints for #Message, unified with the generated
// schema/aiagent_go_gen.cue during schema assembly (see server/README.md).

package aiagent

#Message: {
	// Required display name containing a non-whitespace character.
	agent: string & =~ "\\S"

	// Required Markdown body containing a non-whitespace character.
	text: string & =~ "\\S"

	// Optional absolute HTTP(S) source URL with a host. An empty value is
	// invalid; omit the field when no URL is available.
	url?: string & =~ "^(?i)https?://[^/\\s][^\\s]*$"
}
