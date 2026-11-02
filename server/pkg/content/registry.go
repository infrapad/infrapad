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
//
// Besides type resolution, the registry provides the validation boundaries
// used by host processing: ValidatePayload and ValidateRecord for inputs,
// ValidateExpansion for expansion output, and DecodeContent/DecodeRecord for
// stored or submitted JSON. Failures are structured *ValidationError values.
type Registry struct {
	cue   *cue.Context
	types map[string]*registeredType
}

type registeredType struct {
	contentType ContentType
	// contentSchema and payloadSchema are the compiled CUE definitions
	// selected by the type's schemas, used for strict runtime validation.
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
	entry, err := r.lookupEntry(name)
	if err != nil {
		return nil, err
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

// DecodeContent validates the raw inner content JSON against the type's
// content schema and then decodes it, so unknown or missing fields cannot
// disappear through Go decoding.
func (r *Registry) DecodeContent(name string, data json.RawMessage) (Content, error) {
	entry, err := r.lookupEntry(name)
	if err != nil {
		return nil, err
	}
	if err := r.validate(entry, entry.contentSchema, StageContent, data); err != nil {
		return nil, err
	}
	value, err := entry.contentType.DecodeJSON(data)
	if err != nil {
		return nil, &ValidationError{ContentType: name, Stage: StageContent, Err: fmt.Errorf("decode content: %w", err)}
	}
	return value, nil
}

// ValidatePayload checks a transient ingestion payload against the payload
// schema of the named expandable type. The payload is not modified.
func (r *Registry) ValidatePayload(name string, payload json.RawMessage) error {
	entry, err := r.lookupEntry(name)
	if err != nil {
		return err
	}
	if _, expands := entry.contentType.(Expander); !expands {
		return &ValidationError{ContentType: name, Stage: StagePayload, Err: fmt.Errorf("type does not accept an expansion payload")}
	}
	return r.validate(entry, entry.payloadSchema, StagePayload, payload)
}

// ValidateRecord checks a (typically Go-constructed) record: the exact
// registered type instance and host record shape/metadata (checkShape),
// then the JSON representation of each inner value (each collection member
// individually) against the type's content schema. Collection envelopes,
// keys, and timestamps are not part of extension schemas.
func (r *Registry) ValidateRecord(record Record) error {
	if err := checkShape(record); err != nil {
		return err
	}
	ct := record.ContentType()
	if !r.isRegistered(ct) {
		return recordError(ct, "content type instance is not registered")
	}
	entry := r.types[ct.Name()]
	if collection, ok := record.(GroupableRecord); ok {
		for n, member := range collection.KeyedItems() {
			if err := r.validateValue(entry, member.(ItemRecord).Content()); err != nil {
				return locateError(err, atMember(n, member.Key()))
			}
		}
		return nil
	}
	return r.validateValue(entry, record.(ItemRecord).Content())
}

// ValidateExpansion validates an expansion result for the existing primary:
// the primary keeps its type and (if keyed) its key; the returned primary
// and every emitted record pass ValidateRecord (registered type instance,
// host shape, inner content schema, including cross-extension output); all
// emitted keys are unique within the primary; transient payloads are only
// attached to expandable references. Errors in emitted output carry a
// leading block step in their Location; errors without one concern the
// returned primary. Use ExpansionResult.Members for the emitted constituents
// afterwards.
//
// Host sequence: ValidateRecord(existing) and ValidatePayload → Expand →
// ValidateExpansion. Downstream payloads are validated (ValidatePayload)
// immediately before their own expansion; recursion and persistence are
// separate host responsibilities.
func (r *Registry) ValidateExpansion(existing Record, result ExpansionResult) error {
	if existing == nil || !r.isRegistered(existing.ContentType()) {
		return fmt.Errorf("expansion target must be a record of a registered type")
	}
	ct := existing.ContentType()
	if result.Record == nil || result.Record.ContentType() != ct {
		return recordError(ct, "expansion changed or omitted the primary record type")
	}
	if err := r.ValidateRecord(result.Record); err != nil {
		return err
	}
	if old, keyed := existing.(KeyedRecord); keyed {
		updated, ok := result.Record.(KeyedRecord)
		if !ok || updated.Key() != old.Key() {
			return recordError(ct, "expansion changed the primary key %q", old.Key())
		}
	}
	seen := map[string]bool{}
	for n, block := range result.Blocks {
		if block.Record == nil {
			return locateError(recordError(nil, "emitted block has no record"), atBlock(n))
		}
		if err := r.ValidateRecord(block.Record); err != nil {
			return locateError(err, atBlock(n))
		}
		emitted := block.Record.ContentType()
		if _, expands := emitted.(Expander); !expands && len(block.ExpansionPayload) != 0 {
			return locateError(recordError(emitted, "type does not accept an expansion payload"), atBlock(n))
		}
		for _, member := range Members(block.Record) {
			if seen[member.Key()] {
				return locateError(recordError(emitted, "duplicate emitted key %q", member.Key()), atBlock(n))
			}
			seen[member.Key()] = true
		}
	}
	return nil
}

func (r *Registry) lookupEntry(name string) (*registeredType, error) {
	entry, ok := r.types[name]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownContentType, name)
	}
	return entry, nil
}

// validateValue validates the JSON representation of an inner Go value.
func (r *Registry) validateValue(entry *registeredType, value Content) error {
	data, err := json.Marshal(value)
	if err != nil {
		return &ValidationError{ContentType: entry.contentType.Name(), Stage: StageContent, Err: fmt.Errorf("encode content: %w", err)}
	}
	return r.validate(entry, entry.contentSchema, StageContent, data)
}

func (r *Registry) validate(entry *registeredType, schema cue.Value, stage ValidationStage, data []byte) error {
	fields := validateJSON(schema, data)
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{ContentType: entry.contentType.Name(), Stage: stage, Fields: fields}
}
