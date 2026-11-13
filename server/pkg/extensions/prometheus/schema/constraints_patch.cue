// Companion constraints for the generated schema/*_go_gen.cue
// definitions, unified during schema assembly (see server/README.md).
// Explicit (non-null) arrays/objects, time bounds, and sample ordering are
// semantic checks of QueryResults.Validate.

package prometheus

#QueryResults: step_seconds: >0

// Sample is encoded as exactly one [unix_seconds, value] pair of numbers.
#Sample: [number, number]
