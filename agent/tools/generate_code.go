// ABOUTME: Tool that generates code via a cheap/fast LLM from a structured contract.
// ABOUTME: Implements the hybrid architect/executor pattern — strong model writes contract, this tool calls cheap model.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/llm"
)

// GenerateCodeTool calls a cheap LLM to generate code from a structured contract.
type GenerateCodeTool struct {
	client   Completer
	model    string
	provider string
	workDir  string
}

// GenerateCodeOption configures the GenerateCodeTool.
type GenerateCodeOption func(*GenerateCodeTool)

// WithGenerateModel sets the model used for code generation.
func WithGenerateModel(model string) GenerateCodeOption {
	return func(t *GenerateCodeTool) { t.model = model }
}

// WithGenerateProvider sets the provider used for code generation.
func WithGenerateProvider(provider string) GenerateCodeOption {
	return func(t *GenerateCodeTool) { t.provider = provider }
}

// WithGenerateWorkDir sets the base directory for writing generated files.
func WithGenerateWorkDir(dir string) GenerateCodeOption {
	return func(t *GenerateCodeTool) { t.workDir = dir }
}

// NewGenerateCodeTool creates a tool that generates code via a cheap model.
// Callers should override the model with WithGenerateModel — there is no
// universally-correct default. The env-gated registration in
// pipeline/handlers/backend_native.go always supplies one, so this default
// only matters for direct construction.
func NewGenerateCodeTool(client Completer, opts ...GenerateCodeOption) *GenerateCodeTool {
	t := &GenerateCodeTool{
		client:   client,
		model:    "gpt-4o-mini",
		provider: "openai",
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *GenerateCodeTool) Name() string { return "generate_code" }

func (t *GenerateCodeTool) Description() string {
	return "Generate files from a structured contract using a fast, cheap model. " +
		"Pass a contract specifying structure, content, and rules. Works for any output type: " +
		"code, markdown, configuration, prose. Optionally pass a 'files' array to generate " +
		"each file separately (better for smaller models). " +
		"Returns a summary of files written."
}

func (t *GenerateCodeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"contract": {
				"type": "string",
				"description": "Structured contract with DATA CONTRACT, API CONTRACT, FILE STRUCTURE, ALGORITHM, and RULES sections"
			},
			"output_dir": {
				"type": "string",
				"description": "Directory to write generated files. Defaults to working directory."
			},
			"files": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"path": {"type": "string", "description": "File path relative to output_dir"},
						"description": {"type": "string", "description": "What this file should contain — classes, functions, imports"}
					},
					"required": ["path", "description"]
				},
				"description": "Optional: generate each file separately. Each entry gets its own LLM call with the contract as context. Better for smaller/local models."
			}
		},
		"required": ["contract"]
	}`)
}

func (t *GenerateCodeTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var args struct {
		Contract  string `json:"contract"`
		OutputDir string `json:"output_dir"`
		Files     []struct {
			Path        string `json:"path"`
			Description string `json:"description"`
		} `json:"files"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if args.OutputDir == "" || args.OutputDir == "." {
		args.OutputDir = t.workDir
	}
	if args.OutputDir == "" {
		args.OutputDir = "."
	}
	if !filepath.IsAbs(args.OutputDir) && t.workDir != "" {
		args.OutputDir = filepath.Join(t.workDir, args.OutputDir)
	}

	// Sequential mode: one LLM call per file
	if len(args.Files) > 0 {
		return t.generateSequential(ctx, args.Contract, args.OutputDir, args.Files)
	}

	// Single-call mode: all files in one output
	return t.generateSingleCall(ctx, args.Contract, args.OutputDir)
}

func (t *GenerateCodeTool) generateSequential(ctx context.Context, contract, outputDir string, files []struct {
	Path        string `json:"path"`
	Description string `json:"description"`
}) (string, error) {
	var summary []string
	totalIn, totalOut := 0, 0

	systemPrompt := "You are an executor generating a single file from a contract. " +
		"Output ONLY the raw file content — no commentary, no explanations, no markdown fences. " +
		"Follow the contract exactly."

	for _, f := range files {
		prompt := fmt.Sprintf("CONTRACT (full context — you are writing ONE file from this):\n%s\n\n"+
			"GENERATE THIS FILE: %s\n"+
			"This file should contain: %s\n\n"+
			"Output ONLY the raw file content. Nothing else.",
			contract, f.Path, f.Description)

		resp, err := t.client.Complete(ctx, &llm.Request{
			Model:    t.model,
			Provider: t.provider,
			Messages: []llm.Message{
				{Role: llm.RoleSystem, Content: []llm.ContentPart{{Kind: llm.KindText, Text: systemPrompt}}},
				llm.UserMessage(prompt),
			},
		})
		if err != nil {
			return "", fmt.Errorf("generate_code: failed generating %s: %w", f.Path, err)
		}

		code := resp.Text()
		// Strip markdown fences if the model wrapped them
		code = stripMarkdownFences(code)

		// Validate the LLM-supplied path stays under outputDir — `..`
		// segments or absolute paths that escape would otherwise be honored.
		path, err := resolveUnderRoot(outputDir, f.Path)
		if err != nil {
			return "", fmt.Errorf("generate_code: file path %q: %w", f.Path, err)
		}
		if err := writeFile(path, code); err != nil {
			return "", err
		}

		totalIn += resp.Usage.InputTokens
		totalOut += resp.Usage.OutputTokens
		summary = append(summary, fmt.Sprintf("  %s (%d bytes)", f.Path, len(code)))
	}

	return fmt.Sprintf("Generated %d files (sequential):\n%s\nModel: %s\nTokens: %d in / %d out",
		len(files), strings.Join(summary, "\n"), t.model, totalIn, totalOut), nil
}

func (t *GenerateCodeTool) generateSingleCall(ctx context.Context, contract, outputDir string) (string, error) {
	prompt := "You are an executor generating files from a contract. The contract specifies " +
		"structure, content, and rules. Follow the contract exactly. Do not make decisions " +
		"not already specified in the contract.\n\n" +
		"CONTRACT:\n" + contract + "\n\n" +
		"Output ALL files with `# === FILE: path/to/filename ===` separators.\n" +
		"No commentary, no explanations. Output ONLY file content."

	resp, err := t.client.Complete(ctx, &llm.Request{
		Model:    t.model,
		Provider: t.provider,
		Messages: []llm.Message{llm.UserMessage(prompt)},
	})
	if err != nil {
		return "", fmt.Errorf("generate_code: llm call failed: %w", err)
	}

	code := resp.Text()
	files := parseFiles(code)

	if len(files) == 0 {
		// "generated.py" is a static name with no traversal risk; routed
		// through the same helper for consistency with the multi-file branch.
		path, err := resolveUnderRoot(outputDir, "generated.py")
		if err != nil {
			return "", err
		}
		if err := writeFile(path, code); err != nil {
			return "", err
		}
		return fmt.Sprintf("Generated 1 file (%d bytes): %s\nModel: %s\nTokens: %d in / %d out",
			len(code), path, t.model, resp.Usage.InputTokens, resp.Usage.OutputTokens), nil
	}

	var summary []string
	for _, f := range files {
		// File names come from the model's `# === FILE: name ===` headers in
		// the LLM output. Validate before writing — `..` segments or absolute
		// paths that escape outputDir would otherwise be honored.
		path, err := resolveUnderRoot(outputDir, f.name)
		if err != nil {
			return "", fmt.Errorf("generate_code: file %q: %w", f.name, err)
		}
		if err := writeFile(path, f.content); err != nil {
			return "", err
		}
		summary = append(summary, fmt.Sprintf("  %s (%d bytes)", f.name, len(f.content)))
	}

	return fmt.Sprintf("Generated %d files:\n%s\nModel: %s\nTokens: %d in / %d out",
		len(files), strings.Join(summary, "\n"), t.model,
		resp.Usage.InputTokens, resp.Usage.OutputTokens), nil
}

type parsedFile struct {
	name    string
	content string
}

func parseFiles(code string) []parsedFile {
	var files []parsedFile
	lines := strings.Split(code, "\n")
	var current *parsedFile

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# === FILE:") && strings.HasSuffix(trimmed, "===") {
			if current != nil {
				current.content = strings.TrimSpace(current.content)
				files = append(files, *current)
			}
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "# === FILE:"), "==="))
			current = &parsedFile{name: name}
		} else if current != nil {
			current.content += line + "\n"
		}
	}
	if current != nil {
		current.content = strings.TrimSpace(current.content)
		files = append(files, *current)
	}
	return files
}

func stripMarkdownFences(code string) string {
	lines := strings.Split(code, "\n")
	if len(lines) < 2 {
		return code
	}
	first := strings.TrimSpace(lines[0])
	if strings.HasPrefix(first, "```") {
		lines = lines[1:]
		// Remove trailing fence
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.TrimSpace(lines[i]) == "```" {
				lines = lines[:i]
				break
			}
		}
		return strings.Join(lines, "\n")
	}
	return code
}

func writeFile(path string, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
