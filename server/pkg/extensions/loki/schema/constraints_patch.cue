// Companion constraints for the generated schema/*_go_gen.cue
// definitions, unified during schema assembly (see server/README.md).
// Explicit (non-null) arrays/objects, time bounds, and the signed 64-bit
// timestamp range are semantic checks of Logs.Validate.

package loki

#LogsQuery: limit: >0

#Entry: timestamp: =~"^-?[0-9]+$"
