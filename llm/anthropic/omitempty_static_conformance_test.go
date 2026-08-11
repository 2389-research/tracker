// ABOUTME: Static structural guard for the omitempty-on-required-field bug class (#567/#568).
// ABOUTME: Reflects over request structs to assert schema-required-even-if-empty fields are not serialization-droppable.
package anthropic

import (
	"reflect"
	"strings"
	"testing"
)

// This is the STATIC complement to the dynamic replay-fidelity test in
// roundtrip_conformance_test.go. Instead of driving a value through
// translateRequest and inspecting the wire, it inspects the struct DEFINITION
// itself, catching the bug class at compile-of-the-schema time — before any
// model returns the empty-but-present block that triggers it in production.
//
// The bug class: a request struct field for a content-block key the provider
// treats as REQUIRED-EVEN-WHEN-EMPTY, tagged `omitempty` on a VALUE type. Go's
// encoding/json drops the zero value, so a present-but-empty field silently
// vanishes from the wire and the provider 400s on replay.
//   - #567 (FIXED): anthropicContent.Thinking — Sonnet 5 / Opus 5 / Fable 5
//     return a signature-only thinking block with "thinking":"". Making the
//     field *string (presence-tracked) keeps the empty value on the wire.
//   - #568 (OPEN): anthropicContent.Input — a no-argument tool_use's valid
//     shape is "input":{}, but the stream accumulator can leave Arguments=""
//     (empty json.RawMessage); `omitempty` then drops "input" on replay.
//
// The rule enforced here: a field named in the required-present table must be
// presence-tracked (a pointer, whose omitempty only drops a NIL, never a
// present-but-empty value) OR carry no `omitempty` at all. A value type WITH
// `omitempty` is serialization-droppable-when-empty == the bug.

// requiredPresentField names one struct field that the Anthropic Messages API
// requires present on the replay of a content block EVEN WHEN its value is the
// empty/zero value. Kept deliberately CONSERVATIVE: only fields the provider
// genuinely accepts (and requires) in an empty-but-present form. Fields that are
// required but never legitimately empty (e.g. thinking.signature, the opaque
// redacted_thinking.data) are intentionally excluded — asserting on them would
// risk false-positive CI failures without guarding a real replay case.
type requiredPresentField struct {
	provider  string
	structVal any    // zero value of the request struct
	jsonKey   string // the wire key that must survive when empty
	rationale string // why the provider requires it present-when-empty
	// knownBug, if set, converts a CURRENTLY-DROPPABLE field from a failure into
	// a t.Skip(knownBug). Use only for a genuinely open, tracked defect so the
	// suite stays green; the case activates automatically once the field is
	// made presence-tracked.
	knownBug string
}

func requiredPresentFields() []requiredPresentField {
	return []requiredPresentField{
		{
			provider:  "anthropic",
			structVal: anthropicContent{},
			jsonKey:   "thinking",
			rationale: "#567: signature-only thinking block replays with \"thinking\":\"\"; " +
				"Anthropic 400s \"thinking.thinking: Field required\" if the empty value is dropped. " +
				"Guards the shipped *string fix.",
			// No knownBug: this MUST pass on current main (field is *string).
		},
		// NOTE: anthropicContent.Input (tool_use) is deliberately NOT listed here.
		// It is a DIFFERENT remediation class from #567: an empty tool_use input
		// is never a legitimate value (the minimum valid shape is "{}"), so #568
		// is fixed by value-normalization at the source (StreamAccumulator emits
		// "{}" for a no-arg call), not by making the struct field presence-tracked.
		// With the value guaranteed non-empty, `input,omitempty` on a value type
		// is safe. The dynamic round-trip test (roundtrip_conformance_test.go,
		// tool_use_no_args_stream_path) is the enforcing gate for input. This
		// static guard covers only the "empty is a LEGITIMATE value that must
		// survive" class (thinking:"").
	}
}

// fieldByJSONKey finds the struct field whose json tag name matches key.
func fieldByJSONKey(t reflect.Type, key string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := tag
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			name = tag[:comma]
		}
		if name == "" {
			name = f.Name // json falls back to the Go field name
		}
		if name == key {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

func tagHasOmitempty(tag string) bool {
	for _, opt := range strings.Split(tag, ",")[1:] {
		if opt == "omitempty" {
			return true
		}
	}
	return false
}

// isPresenceTracked reports whether a present-but-empty value of this field
// survives json marshaling under omitempty. Only a pointer (nil is dropped, a
// non-nil pointer to the zero value is kept) qualifies. Every other kind —
// string, slice ([]byte / json.RawMessage), map, interface, numeric, bool —
// has its zero value dropped by omitempty, which is precisely the bug.
func isPresenceTracked(k reflect.Kind) bool {
	return k == reflect.Ptr
}

func TestOmitemptyStaticConformance(t *testing.T) {
	for _, rf := range requiredPresentFields() {
		name := rf.provider + "/" + reflect.TypeOf(rf.structVal).Name() + "." + rf.jsonKey
		t.Run(name, func(t *testing.T) {
			st := reflect.TypeOf(rf.structVal)
			if st.Kind() != reflect.Struct {
				t.Fatalf("structVal for %s is %s, want a struct", name, st.Kind())
			}
			field, ok := fieldByJSONKey(st, rf.jsonKey)
			if !ok {
				t.Fatalf("no field with json key %q found on %s; the required-present table is stale "+
					"(field renamed/removed?)", rf.jsonKey, st.Name())
			}

			tag := field.Tag.Get("json")
			droppable := tagHasOmitempty(tag) && !isPresenceTracked(field.Type.Kind())
			if !droppable {
				return // safe: pointer, or no omitempty — a present-but-empty value survives.
			}

			// Droppable-when-empty: this is the #567/#568 bug class.
			msg := "field %s.%s (json:%q, Go type %s, kind %s) is serialization-droppable when empty: " +
				"it has `omitempty` on a value type, so a present-but-empty value is dropped from the wire. " +
				"%s Make it a pointer (presence-tracked) or remove omitempty."
			if rf.knownBug != "" {
				t.Skipf("known-open bug: %s\n(rationale: %s)", rf.knownBug, rf.rationale)
			}
			t.Errorf(msg, st.Name(), field.Name, rf.jsonKey, field.Type, field.Type.Kind(), rf.rationale)
		})
	}
}
