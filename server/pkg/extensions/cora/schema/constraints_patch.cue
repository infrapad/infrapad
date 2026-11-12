// Companion constraints for the generated schema/*_go_gen.cue
// definitions, unified during schema assembly (see server/README.md).

package cora

// _url is an absolute HTTP(S) URL with a host; the hostname is not fixed.
_url: string & =~"^(?i)https?://[^/\\s][^\\s]*$"

// _nonblank is a string with at least one non-space character.
_nonblank: string & =~"\\S"

#Report: {
	report_id: _nonblank
	// Optional; an empty value is invalid, omit the field instead.
	url?: _url
}

#Payload: {
	report_id: _nonblank
	text:      _nonblank
}
