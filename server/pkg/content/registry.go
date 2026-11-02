package content

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// typeNamePattern matches canonical namespaced names such as "Slack/Thread".
var typeNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*/[A-Za-z][A-Za-z0-9]*$`)

// Registry is the explicit set of registered content types. Construct it with
// NewRegistry and inject it where needed; there is no global registry.
type Registry struct {
	cue   *cue.Context
	types map[string]*registeredType
}

type registeredType struct {
	contentType ContentType
	// contentSchema and payloadSchema are the compiled CUE definitions
	// selected by the type's schemas. Strict operation-time validation
	// builds on them.
	contentSchema cue.Value
	payloadSchema cue.Value // zero Value for non-expanders
}

// NewRegistry registers the content types returned by extension constructors,
// e.g. NewRegistry(aiagent.ContentTypes(), slack.ContentTypes(agent)).
// It rejects invalid or duplicate names, incompatible capability combinations,
// and schema references that do not compile or select an existing definition.
func NewRegistry(extensions ...[]ContentType) (*Registry, error) {
	r := &Registry{cue: cuecontext.New(), types: map[string]*registeredType{}}
	for _, extension := range extensions {
		for _, ct := range extension {
			if err := r.register(ct); err != nil {
				return nil, err
			}
		}
	}
	return r, nil
}

func (r *Registry) register(ct ContentType) error {
	if ct == nil {
		return fmt.Errorf("cannot register a nil content type")
	}
	name := ct.Name()
	if !typeNamePattern.MatchString(name) {
		return fmt.Errorf("invalid namespaced content type name %q", name)
	}
	if _, exists := r.types[name]; exists {
		return fmt.Errorf("duplicate registration of content type %s", name)
	}
	expander, expands := ct.(Expander)
	if _, groupable := ct.(GroupableContentType); groupable && expands {
		return fmt.Errorf("%s: a content type cannot both group and expand", name)
	}
	entry := &registeredType{contentType: ct}
	var err error
	if entry.contentSchema, err = r.compileSchema(ct.ContentSchema()); err != nil {
		return fmt.Errorf("%s: content schema: %w", name, err)
	}
	if expands {
		if entry.payloadSchema, err = r.compileSchema(expander.PayloadSchema()); err != nil {
			return fmt.Errorf("%s: payload schema: %w", name, err)
		}
	}
	r.types[name] = entry
	return nil
}

func (r *Registry) compileSchema(schema Schema) (cue.Value, error) {
	if len(schema.Source) == 0 || schema.Definition == "" {
		return cue.Value{}, fmt.Errorf("incomplete schema reference")
	}
	source := r.cue.CompileBytes(schema.Source)
	if err := source.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("compile: %w", err)
	}
	path := cue.ParsePath(schema.Definition)
	if err := path.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("selector %q: %w", schema.Definition, err)
	}
	definition := source.LookupPath(path)
	if !definition.Exists() {
		return cue.Value{}, fmt.Errorf("definition %q not found", schema.Definition)
	}
	if err := definition.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("definition %q: %w", schema.Definition, err)
	}
	return definition, nil
}

// Lookup returns the registered content type; unknown types fail explicitly.
func (r *Registry) Lookup(name string) (ContentType, error) {
	entry, ok := r.types[name]
	if !ok {
		return nil, fmt.Errorf("unknown content type %q", name)
	}
	return entry.contentType, nil
}

// Names returns the registered type names in sorted order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.types))
	for name := range r.types {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// isRegistered reports whether ct is the exact registered implementation.
func (r *Registry) isRegistered(ct ContentType) bool {
	if ct == nil {
		return false
	}
	entry, ok := r.types[ct.Name()]
	return ok && entry.contentType == ct
}

// DecodeContent decodes an inner content value of the named type.
//
// TODO(extension-schema-validation): validate data against the compiled
// content schema before decoding.
func (r *Registry) DecodeContent(name string, data json.RawMessage) (Content, error) {
	ct, err := r.Lookup(name)
	if err != nil {
		return nil, err
	}
	value, err := ct.DecodeJSON(data)
	if err != nil {
		return nil, fmt.Errorf("%s: decode content: %w", name, err)
	}
	return value, nil
}

// CheckExpansion validates host-owned aspects of an expansion result for the
// existing primary: the primary keeps its type and (if keyed) its key; every
// emitted record uses a registered type, has valid host shape, and all
// emitted keys are unique within the primary; transient payloads are only
// attached to expandable references. It returns the emitted constituent
// records in emission order, for host grouping/reconciliation.
//
// Inner-content schema validation, recursion, and persistence are separate
// host responsibilities.
func (r *Registry) CheckExpansion(existing Record, result ExpansionResult) ([]KeyedRecord, error) {
	if existing == nil || !r.isRegistered(existing.ContentType()) {
		return nil, fmt.Errorf("expansion target must be a record of a registered type")
	}
	name := existing.ContentType().Name()
	if result.Record == nil || result.Record.ContentType() != existing.ContentType() {
		return nil, fmt.Errorf("%s: expansion changed or omitted the primary record type", name)
	}
	if err := CheckRecord(result.Record); err != nil {
		return nil, fmt.Errorf("%s: primary: %w", name, err)
	}
	if old, keyed := existing.(KeyedRecord); keyed {
		updated, ok := result.Record.(KeyedRecord)
		if !ok || updated.Key() != old.Key() {
			return nil, fmt.Errorf("%s: expansion changed the primary key %q", name, old.Key())
		}
	}
	var members []KeyedRecord
	seen := map[string]bool{}
	for n, block := range result.Blocks {
		if block.Record == nil {
			return nil, fmt.Errorf("%s: emitted block %d has no record", name, n)
		}
		ct := block.Record.ContentType()
		if !r.isRegistered(ct) {
			return nil, fmt.Errorf("%s: emitted block %d has unregistered content type %s", name, n, typeName(ct))
		}
		if err := CheckRecord(block.Record); err != nil {
			return nil, fmt.Errorf("%s: emitted block %d: %w", name, n, err)
		}
		if _, expands := ct.(Expander); !expands && len(block.ExpansionPayload) != 0 {
			return nil, fmt.Errorf("%s: emitted block %d: %s does not accept an expansion payload", name, n, ct.Name())
		}
		for _, member := range Members(block.Record) {
			if seen[member.Key()] {
				return nil, fmt.Errorf("%s: duplicate emitted key %q", name, member.Key())
			}
			seen[member.Key()] = true
			members = append(members, member)
		}
	}
	return members, nil
}
