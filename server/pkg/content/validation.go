package content

import (
	"errors"
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	cuecontext "cuelang.org/go/cue/cuecontext"
	cuejson "cuelang.org/go/encoding/json"
)

// ErrUnknownContentType is wrapped by errors for unregistered type names.
var ErrUnknownContentType = errors.New("unknown content type")

// ValidationStage identifies which boundary rejected a value.
type ValidationStage string

const (
	// StagePayload is a transient ingestion payload checked against the
	// expander's payload schema.
	StagePayload ValidationStage = "payload"
	// StageContent is an inner content value checked against its type's
	// content schema.
	StageContent ValidationStage = "content"
	// StageRecord is host-owned record shape/metadata (type association,
	// timestamps, keys, collection envelopes, expansion identities).
	StageRecord ValidationStage = "record"
)

// LocationKind is the kind of a LocationStep.
type LocationKind string

const (
	// LocationBlock is an emitted block index within an expansion result.
	LocationBlock LocationKind = "block"
	// LocationMember is a member index (and key) within a collection record.
	LocationMember LocationKind = "member"
)

// LocationStep is one level of a Location.
type LocationStep struct {
	Kind  LocationKind `json:"kind"`
	Index int          `json:"index"`
	// Key is the member key (LocationMember only).
	Key string `json:"key,omitempty"`
}

func (s LocationStep) String() string {
	if s.Kind == LocationMember {
		return fmt.Sprintf("member %d (key %q)", s.Index, s.Key)
	}
	return fmt.Sprintf("%s %d", s.Kind, s.Index)
}

// Location is the position of the failing value, outermost step first, e.g.
// [block 1, block 0, member 1] for a member emitted by a nested expansion.
// An empty location is the validated record (or the returned primary of an
// expansion) itself.
type Location []LocationStep

func (l Location) String() string {
	steps := make([]string, len(l))
	for n, step := range l {
		steps[n] = step.String()
	}
	return strings.Join(steps, " > ")
}

// FieldError is one schema violation. Path is the dotted field path within
// the validated value ("" for the value itself); Message wording is not a
// stable contract.
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError is a transport-neutral structured validation failure.
// Inspect it with errors.As; it survives fmt.Errorf("...: %w") wrapping.
type ValidationError struct {
	ContentType string
	Stage       ValidationStage
	Location    Location
	// Fields lists schema violations (payload/content stages).
	Fields []FieldError
	// Err is the underlying cause (e.g. host record check, decode error).
	Err error
}

func (e *ValidationError) Error() string {
	var out strings.Builder
	out.WriteString(e.ContentType)
	if len(e.Location) > 0 {
		fmt.Fprintf(&out, ": %s", e.Location)
	}
	fmt.Fprintf(&out, ": invalid %s", e.Stage)
	if len(e.Fields) > 0 {
		fields := make([]string, len(e.Fields))
		for n, field := range e.Fields {
			if field.Path == "" {
				fields[n] = field.Message
			} else {
				fields[n] = field.Path + ": " + field.Message
			}
		}
		fmt.Fprintf(&out, ": %s", strings.Join(fields, "; "))
	}
	if e.Err != nil {
		fmt.Fprintf(&out, ": %v", e.Err)
	}
	return out.String()
}

func (e *ValidationError) Unwrap() error { return e.Err }

// recordError builds a host-record-stage validation error.
func recordError(ct ContentType, format string, args ...any) *ValidationError {
	return &ValidationError{ContentType: typeName(ct), Stage: StageRecord, Err: fmt.Errorf(format, args...)}
}

// locateError prepends an outer location step to the ValidationError within
// err; each layer adds its own step while unwinding. Other errors are
// returned unchanged.
func locateError(err error, step LocationStep) error {
	var validation *ValidationError
	if errors.As(err, &validation) {
		validation.Location = append(Location{step}, validation.Location...)
	}
	return err
}

func atBlock(n int) LocationStep { return LocationStep{Kind: LocationBlock, Index: n} }

func atMember(n int, key string) LocationStep {
	return LocationStep{Kind: LocationMember, Index: n, Key: key}
}

// validateJSON checks raw JSON against a compiled schema definition without
// applying defaults or otherwise rewriting the value. CUE values support
// concurrent method calls; each validation builds its input in a fresh context.
func validateJSON(schema cue.Value, raw []byte) []FieldError {
	expr, err := cuejson.Extract("value", raw)
	if err != nil {
		return []FieldError{{Message: fmt.Sprintf("invalid JSON: %v", err)}}
	}
	value := cuecontext.New().BuildExpr(expr)
	if err := value.Err(); err != nil {
		return []FieldError{{Message: err.Error()}}
	}
	return fieldErrors(schema, schema.Unify(value).Validate(cue.Concrete(true)))
}

// fieldErrors converts CUE errors into field violations relative to the
// validated value (the schema definition's own path prefix is removed).
func fieldErrors(schema cue.Value, err error) []FieldError {
	if err == nil {
		return nil
	}
	prefix := schema.Path().Selectors()
	seen := map[FieldError]bool{}
	var fields []FieldError
	for _, e := range cueerrors.Errors(err) {
		path := e.Path()
		if len(path) >= len(prefix) && matchesPrefix(path, prefix) {
			path = path[len(prefix):]
		}
		format, args := e.Msg()
		message := fmt.Sprintf(format, args...)
		if strings.HasPrefix(message, "incomplete value") {
			message = "missing required value (" + message + ")"
		}
		field := FieldError{Path: strings.Join(path, "."), Message: message}
		if !seen[field] {
			seen[field] = true
			fields = append(fields, field)
		}
	}
	return fields
}

func matchesPrefix(path []string, prefix []cue.Selector) bool {
	for n, selector := range prefix {
		if path[n] != selector.String() {
			return false
		}
	}
	return true
}
