// ABOUTME: Public API for declared pipeline inputs — introspect, validate, bind
// ABOUTME: at run start (#553). Pairs with dippin's `inputs` declaration (#190).
package tracker

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

// Input is a caller-supplied value for a declared workflow input. Use the
// constructors: StringInput for text/number/bool/enum, FileInput / FileInputBytes
// for a file input (staged into the run dir at <workDir>/.tracker/inputs/<name>).
type Input struct {
	Name      string
	String    string // text/number/bool/enum — canonical string form
	FilePath  string // file: path to read and stage
	FileBytes []byte // file: inline contents to stage (mutually exclusive with FilePath)
}

// StringInput builds a value input. Numbers and bools pass their canonical
// string form ("42", "true"); validation coerces and checks them.
func StringInput(name, value string) Input { return Input{Name: name, String: value} }

// FileInput builds a file input from a path; its contents are staged at run start.
func FileInput(name, path string) Input { return Input{Name: name, FilePath: path} }

// FileInputBytes builds a file input from inline contents (for values that
// arrive over the wire rather than as a local path).
func FileInputBytes(name string, contents []byte) Input {
	return Input{Name: name, FileBytes: contents}
}

// fileBytesMarker is a non-empty placeholder so a bytes-only file input reads as
// "present" during validation; staging overrides the seed with the staged path.
const fileBytesMarker = "\x00file-bytes"

// value returns the raw string a validator sees for this input.
func (in Input) value() string {
	if in.FilePath != "" {
		return in.FilePath
	}
	if len(in.FileBytes) > 0 {
		return fileBytesMarker
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
func bindInputs(graph *pipeline.Graph, cfg Config, workDir string) (Config, error) {
	if len(graph.Inputs) == 0 && len(cfg.Inputs) == 0 {
		return cfg, nil
	}
	seed, errs := pipeline.ValidateInputValues(graph.Inputs, inputValues(cfg.Inputs))
	if fatal := fatalInputErrors(errs); len(fatal) > 0 {
		return cfg, &InputValidationError{Errors: fatal}
	}
	staged, err := stageFileInputs(graph.Inputs, cfg.Inputs, workDir)
	if err != nil {
		return cfg, err
	}
	for name, rel := range staged {
		seed[name] = rel // the staged relative path is the ${inputs.<name>} value
	}
	if len(seed) == 0 {
		return cfg, nil
	}
	cfg.Context = mergeInputSeed(cfg.Context, seed)
	return cfg, nil
}

// mergeInputSeed returns ctx with the validated input seed added under the
// "inputs." prefix (the closed inputs.* expansion namespace reads it there).
func mergeInputSeed(ctx, seed map[string]string) map[string]string {
	merged := make(map[string]string, len(ctx)+len(seed))
	for k, v := range ctx {
		merged[k] = v
	}
	for name, v := range seed {
		merged["inputs."+name] = v
	}
	return merged
}

// stageFileInputs stages every supplied file input into the workdir and returns
// name→relative-staged-path. Only caller-supplied file inputs are staged; a
// file input's default path is not (the workflow's own logic resolves defaults).
func stageFileInputs(specs []pipeline.InputSpec, inputs []Input, workDir string) (map[string]string, error) {
	fileNames := fileInputNames(specs)
	if len(fileNames) == 0 {
		return nil, nil
	}
	out := make(map[string]string)
	for _, in := range inputs {
		if !fileNames[in.Name] {
			continue
		}
		rel, staged, err := stageOneFileInput(in, workDir)
		if err != nil {
			return nil, err
		}
		if staged {
			out[in.Name] = rel
		}
	}
	return out, nil
}

// stageOneFileInput stages a single file input. staged is false when the input
// carries no contents (present-but-empty is caught by validation).
func stageOneFileInput(in Input, workDir string) (rel string, staged bool, err error) {
	data, err := fileInputData(in)
	if err != nil {
		return "", false, inputFileError(in.Name, err)
	}
	if data == nil {
		return "", false, nil
	}
	rel, err = pipeline.StageInputFile(workDir, in.Name, data)
	if err != nil {
		return "", false, inputFileError(in.Name, err)
	}
	return rel, true, nil
}

// inputFileError wraps a file staging/read failure as a structured input error.
func inputFileError(name string, err error) error {
	return &InputValidationError{Errors: []pipeline.InputError{{Name: name, Kind: pipeline.ErrFile, Detail: err.Error()}}}
}

// fileInputNames returns the set of declared inputs whose kind is file.
func fileInputNames(specs []pipeline.InputSpec) map[string]bool {
	names := make(map[string]bool)
	for _, s := range specs {
		if s.Kind == pipeline.InputFile {
			names[s.Name] = true
		}
	}
	return names
}

// fileInputData resolves a file input's contents from inline bytes or a path
// (read size-capped). Returns nil when neither is supplied.
func fileInputData(in Input) ([]byte, error) {
	if len(in.FileBytes) > 0 {
		return in.FileBytes, nil
	}
	if in.FilePath == "" {
		return nil, nil
	}
	f, err := os.Open(in.FilePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, pipeline.MaxInputFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > pipeline.MaxInputFileBytes {
		return nil, fmt.Errorf("file %q exceeds the %d-byte cap", in.FilePath, pipeline.MaxInputFileBytes)
	}
	return data, nil
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
