// Companion constraints for the generated schema/slack_go_gen.cue
// definitions, unified during schema assembly (see server/README.md).

package slack

#ThreadReference: {
	// Absolute HTTP(S) Slack thread permalink:
	// https://<workspace>/archives/<channelID>/p<16-digit timestamp>.
	// The workspace hostname is not fixed.
	url: string & =~ "^https?://[^/\\s]+/archives/[A-Z0-9]+/p[0-9]{16}$"
}

#Message: {
	// Optional absolute HTTP(S) source permalink with a host. An empty
	// value is invalid; omit the field when no URL is available.
	url?: string & =~ "^(?i)https?://[^/\\s][^\\s]*$"
}

#SourceMessage: {
	// The Slack message timestamp is the stable source identity and must
	// be nonblank; well-formedness and uniqueness are semantic checks of
	// the expander/converter.
	ts: string & =~ "\\S"
}

#Snapshot: {
	// Optional per-snapshot Slack user-ID → agent-name mapping; keys and
	// values must be nonblank.
	agents?: {[=~"^\\S+$"]: string & =~ "^\\S+$"}
}
