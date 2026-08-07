// ABOUTME: Declared pipeline inputs — the caller-supplied run signature (#553).
// ABOUTME: InputSpec schema + value validation; injection/namespace live in expand.go.
package pipeline

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/2389-research/dippin-lang/ir"
)

// inputsFromIR maps dippin's declared inputs (ir.Input) to tracker's InputSpec.
// It is the adapter seam for the `inputs` block; an unrecognized Type is carried
// verbatim so a .dip authored against a newer dippin still introspects and
// round-trips (value-validation errors on it later).
func inputsFromIR(inputs []*ir.Input) []InputSpec {
	if len(inputs) == 0 {
		return nil
	}
	specs := make([]InputSpec, 0, len(inputs))
	for _, in := range inputs {
		if in == nil {
			continue
		}
		specs = append(specs, InputSpec{
			Name:        in.Name,
			Kind:        InputKind(in.Type),
			Required:    in.Required,
			Default:     in.Default,
			HasDefault:  in.HasDefault,
			Prompt:      in.Prompt,
			Description: in.Description,
			Multiline:   in.Multiline,
			Options:     in.Options,
			Pattern:     in.Pattern,
			Min:         in.Min,
			Max:         in.Max,
			MaxLength:   in.MaxLength,
		})
	}
	return specs
}

// InputKind is the declared type of a pipeline input. The v1 set mirrors
// dippin-lang #190; an unrecognized kind is preserved verbatim (so a .dip
// authored against a newer dippin still introspects and round-trips) and
// errors only when a value is validated against it.
type InputKind string

const (
	InputText   InputKind = "text"
	InputNumber InputKind = "number"
	InputBool   InputKind = "bool"
	InputEnum   InputKind = "enum"
	InputFile   InputKind = "file"
	InputSecret InputKind = "secret"
)

// InputSpec is one declared input. Fields mirror dippin's ir.Input; Min/Max are
// raw source text (parsed at validation) to stay faithful to the declaration.
type InputSpec struct {
	Name        string
	Kind        InputKind
	Required    bool     // host must obtain a value even when Default is set
	Default     string   // form prefill; applied only to omitted non-required inputs
	HasDefault  bool     // distinguishes an absent default from an empty-string default
	Prompt      string   // what a host asks the caller
	Description string   // help text
	Multiline   bool     // text: host renders a textarea
	Options     []string // enum choices
	Pattern     string   // text: regex
	Min, Max    string   // number: inclusive bounds, raw text ("" = unbounded)
	MaxLength   int      // text: character cap (0 = none)
}

// IsSecret reports whether the input carries redaction obligations.
func (s InputSpec) IsSecret() bool { return s.Kind == InputSecret }

// InputErrorKind classifies a validation failure so a host can re-prompt precisely.
type InputErrorKind string

const (
	ErrMissingRequired InputErrorKind = "missing_required"
	ErrTypeMismatch    InputErrorKind = "type_mismatch"
	ErrPattern         InputErrorKind = "pattern"
	ErrRange           InputErrorKind = "range"
	ErrLength          InputErrorKind = "length"
	ErrEnum            InputErrorKind = "enum"
	ErrUnknownKind     InputErrorKind = "unknown_kind"
	ErrUnsupportedKind InputErrorKind = "unsupported_kind" // declared but not yet handled (e.g. secret)
	ErrUnknownInput    InputErrorKind = "unknown_input"
)

// InputError is a single, machine-readable validation failure.
type InputError struct {
	Name   string
	Kind   InputErrorKind
	Detail string
}

func (e InputError) Error() string {
	return fmt.Sprintf("input %q: %s (%s)", e.Name, e.Detail, e.Kind)
}

// ValidateInputValues checks caller-supplied values against specs and returns
// the canonical seed (name → coerced string) plus any structured errors. It is
// filesystem-free and side-effect-free: file inputs are validated for presence
// only (staging is a higher layer). A nil/empty specs list accepts anything and
// returns the values unchanged — a pipeline with no declared inputs is
// unaffected. Defaults are applied to omitted non-required inputs.
func ValidateInputValues(specs []InputSpec, values map[string]string) (map[string]string, []InputError) {
	seed := make(map[string]string, len(specs))
	var errs []InputError

	declared := make(map[string]bool, len(specs))
	for i := range specs {
		spec := specs[i]
		declared[spec.Name] = true
		raw, present := values[spec.Name]
		canonical, ierr := resolveInputValue(spec, raw, present)
		if ierr != nil {
			errs = append(errs, *ierr)
			continue
		}
		if canonical != nil {
			seed[spec.Name] = *canonical
		}
	}
	errs = append(errs, unknownInputErrors(declared, values)...)
	return seed, errs
}

// resolveInputValue resolves one input to its canonical value, applying the
// default and required rules. A nil return with a nil error means "omitted
// optional with no default" — nothing to seed.
func resolveInputValue(spec InputSpec, raw string, present bool) (*string, *InputError) {
	if !present {
		if spec.Required {
			return nil, &InputError{spec.Name, ErrMissingRequired, "required input not supplied"}
		}
		if spec.HasDefault {
			return validateByKind(spec, spec.Default)
		}
		return nil, nil
	}
	// A required input satisfied by an empty/whitespace value is the "run
	// proceeds with nothing" failure this feature exists to prevent — reject it
	// with the same signal as an omitted required input.
	if spec.Required && strings.TrimSpace(raw) == "" {
		return nil, &InputError{spec.Name, ErrMissingRequired, "required input supplied empty"}
	}
	return validateByKind(spec, raw)
}

// unknownInputErrors flags supplied keys that were not declared (typo signal).
func unknownInputErrors(declared map[string]bool, values map[string]string) []InputError {
	var errs []InputError
	for name := range values {
		if !declared[name] {
			errs = append(errs, InputError{name, ErrUnknownInput, "supplied input is not declared by the workflow"})
		}
	}
	return errs
}

// validateByKind validates and canonicalizes a supplied value for its kind.
func validateByKind(spec InputSpec, v string) (*string, *InputError) {
	switch spec.Kind {
	case InputNumber:
		return validateNumber(spec, v)
	case InputBool:
		return validateBool(spec, v)
	case InputEnum:
		return validateEnum(spec, v)
	case InputText:
		return validateText(spec, v)
	case InputSecret:
		// A secret value cannot be accepted until redaction exists: it would be
		// seeded into the context and persisted in the checkpoint snapshot in
		// cleartext, contradicting the author's `secret` declaration. Refuse
		// loudly rather than silently leak (redaction is the Phase 3 follow-up).
		return nil, &InputError{spec.Name, ErrUnsupportedKind, "secret inputs are not yet supported (value redaction pending)"}
	case InputFile:
		return okInput(v) // Phase 1+2: path passthrough; staging lands in Phase 3.
	default:
		return nil, &InputError{spec.Name, ErrUnknownKind, fmt.Sprintf("unknown input type %q", spec.Kind)}
	}
}

// okInput wraps a validated value as the canonical result.
func okInput(v string) (*string, *InputError) { return &v, nil }

func validateNumber(spec InputSpec, v string) (*string, *InputError) {
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return nil, &InputError{spec.Name, ErrTypeMismatch, fmt.Sprintf("%q is not a number", v)}
	}
	if ierr := checkNumberBounds(spec, n); ierr != nil {
		return nil, ierr
	}
	return okInput(strings.TrimSpace(v))
}

// checkNumberBounds enforces the inclusive Min/Max bounds when present.
func checkNumberBounds(spec InputSpec, n float64) *InputError {
	if spec.Min != "" {
		if lo, err := strconv.ParseFloat(spec.Min, 64); err == nil && n < lo {
			return &InputError{spec.Name, ErrRange, fmt.Sprintf("%v is below minimum %s", n, spec.Min)}
		}
	}
	if spec.Max != "" {
		if hi, err := strconv.ParseFloat(spec.Max, 64); err == nil && n > hi {
			return &InputError{spec.Name, ErrRange, fmt.Sprintf("%v is above maximum %s", n, spec.Max)}
		}
	}
	return nil
}

func validateBool(spec InputSpec, v string) (*string, *InputError) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true":
		return okInput("true")
	case "false":
		return okInput("false")
	default:
		return nil, &InputError{spec.Name, ErrTypeMismatch, fmt.Sprintf("%q is not a bool (want true/false)", v)}
	}
}

func validateEnum(spec InputSpec, v string) (*string, *InputError) {
	for _, opt := range spec.Options {
		if v == opt {
			return okInput(v)
		}
	}
	return nil, &InputError{spec.Name, ErrEnum, fmt.Sprintf("%q is not one of [%s]", v, strings.Join(spec.Options, ", "))}
}

func validateText(spec InputSpec, v string) (*string, *InputError) {
	if spec.MaxLength > 0 && len([]rune(v)) > spec.MaxLength {
		return nil, &InputError{spec.Name, ErrLength, fmt.Sprintf("length %d exceeds max_length %d", len([]rune(v)), spec.MaxLength)}
	}
	if spec.Pattern != "" {
		re, err := regexp.Compile(spec.Pattern)
		if err != nil {
			return nil, &InputError{spec.Name, ErrPattern, fmt.Sprintf("malformed pattern %q: %v", spec.Pattern, err)}
		}
		if !re.MatchString(v) {
			return nil, &InputError{spec.Name, ErrPattern, fmt.Sprintf("value does not match pattern %q", spec.Pattern)}
		}
	}
	return okInput(v)
}
