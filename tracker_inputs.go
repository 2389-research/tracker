// ABOUTME: Public API for declared pipeline inputs — introspect, validate, bind
// ABOUTME: at run start (#553). Pairs with dippin's `inputs` declaration (#190).
package tracker

import (
	"fmt"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

// Input is a caller-supplied value for a declared workflow input. Use the
// constructors: StringInput for text/number/bool/enum/secret, FileInput for a
// file input (a path; staging into the run dir arrives in a later phase).
type Input struct {
	Name     string
	String   string // text/number/bool/enum/secret — canonical string form
	FilePath string // file: path to the input file
}

// StringInput builds a value input. Numbers and bools pass their canonical
// string form ("42", "true"); validation coerces and checks them.
func StringInput(name, value string) Input { return Input{Name: name, String: value} }

// FileInput builds a file input from a path.
func FileInput(name, path string) Input { return Input{Name: name, FilePath: path} }

// value returns the raw string a validator sees for this input.
func (in Input) value() string {
	if in.FilePath != "" {
		return in.FilePath
	}
	return in.String
}

// DescribeInputs parses source and returns its declared input schema WITHOUT
// running — the read-only introspection a host uses to render a form or ask
// conversationally before it has any values. Returns nil for a pipeline that
// declares no inputs. format follows Config.Format ("dip" default).
func DescribeInputs(source, format string) ([]pipeline.InputSpec, error) {
	graph, err := parsePipelineSource(source, format)
	if err != nil {
		return nil, err
	}
	return graph.Inputs, nil
}

// ValidateInputs checks caller-supplied values against a schema and returns
// structured, per-input errors (empty when valid). Standalone — call it to
// gate a request before Run. Includes unknown_input (a supplied key the
// workflow does not declare) so a host can surface a typo; Run treats
// unknown_input as non-fatal.
func ValidateInputs(specs []pipeline.InputSpec, values []Input) []pipeline.InputError {
	_, errs := pipeline.ValidateInputValues(specs, inputValues(values))
	return errs
}

// InputValidationError is returned by Run/NewEngineFromGraph when supplied
// inputs fail validation. It carries the structured per-input failures so a
// host can re-prompt precisely.
type InputValidationError struct {
	Errors []pipeline.InputError
}

func (e *InputValidationError) Error() string {
	parts := make([]string, 0, len(e.Errors))
	for _, ie := range e.Errors {
		parts = append(parts, ie.Error())
	}
	return "invalid workflow inputs: " + strings.Join(parts, "; ")
}

// inputValues projects []Input to the name→raw-value map the validator consumes.
func inputValues(values []Input) map[string]string {
	m := make(map[string]string, len(values))
	for _, in := range values {
		m[in.Name] = in.value()
	}
	return m
}

// bindInputs validates cfg.Inputs against the graph's declared signature and,
// on success, seeds the canonical values into cfg.Context under the "inputs."
// prefix (the closed inputs.* expansion namespace reads them there, and they
// ride the checkpoint snapshot for resume). A missing required input or a
// type/constraint violation fails closed before any node runs; unknown_input
// is non-fatal (a host that wants to reject typos calls ValidateInputs first).
func bindInputs(graph *pipeline.Graph, cfg Config) (Config, error) {
	if len(graph.Inputs) == 0 && len(cfg.Inputs) == 0 {
		return cfg, nil
	}
	seed, errs := pipeline.ValidateInputValues(graph.Inputs, inputValues(cfg.Inputs))
	if fatal := fatalInputErrors(errs); len(fatal) > 0 {
		return cfg, &InputValidationError{Errors: fatal}
	}
	if len(seed) == 0 {
		return cfg, nil
	}
	merged := make(map[string]string, len(cfg.Context)+len(seed))
	for k, v := range cfg.Context {
		merged[k] = v
	}
	for name, v := range seed {
		merged[fmt.Sprintf("inputs.%s", name)] = v
	}
	cfg.Context = merged
	return cfg, nil
}

// fatalInputErrors drops the non-fatal unknown_input class, leaving the errors
// that must fail the run (missing required, type/constraint/enum violations).
func fatalInputErrors(errs []pipeline.InputError) []pipeline.InputError {
	var fatal []pipeline.InputError
	for _, e := range errs {
		if e.Kind != pipeline.ErrUnknownInput {
			fatal = append(fatal, e)
		}
	}
	return fatal
}
