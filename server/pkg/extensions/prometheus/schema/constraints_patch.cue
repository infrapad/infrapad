// Companion constraints for the generated schema/*_go_gen.cue
// definitions, unified during schema assembly (see server/README.md).
// Explicit (non-null) arrays/objects, time bounds, and sample ordering are
// semantic checks of QueryResults.Validate.

package prometheus

#QueryResults: step_seconds: >0

// Sample is encoded as exactly one [unix_seconds, value] pair: a numeric
// timestamp and a numeric or null value.
#Sample: [number, number | null]

#TimeSeriesQuery: step_seconds: >0
