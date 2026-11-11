// Companion constraints for the generated schema/*_go_gen.cue
// definitions, unified during schema assembly (see server/README.md).

package pagerduty

// _url is an absolute HTTP(S) URL with a host; the hostname is not fixed.
_url: string & =~"^(?i)https?://[^/\\s][^\\s]*$"

// _nonblank is a string with at least one non-space character.
_nonblank: string & =~"\\S"

#Incident: {
	id:  _nonblank
	url: _url
	// Omitted before the first successful payload submission; nonblank afterward.
	text?: _nonblank
}

#TimelineEntry: {
	author:      _nonblank
	kind:        #TimelineKindNote | #TimelineKindLogEntry
	entry_type?: _nonblank
	url:         _url
}

#SourceIncident: {
	id:  _nonblank
	url: _url
}

#SourceAlert: id: _nonblank
#SourceNote: id:  _nonblank
#SourceLogEntry: id: _nonblank
