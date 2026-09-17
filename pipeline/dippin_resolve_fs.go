// ABOUTME: fs.FS-backed resolver for dippin *_file directives (command_file, prompt_file, ...).
// ABOUTME: Lets embedded built-in workflows load their sidecars from the binary instead of disk.
package pipeline

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/2389-research/dippin-lang/ir"
)

// ResolveFileDirectivesFS loads the file contents referenced by every *_file
// directive in w — a tool node's command_file into Command, an agent node's
// prompt_file / system_prompt_file / prompt_include into Prompt /
// SystemPrompt, and the defaults-block prompt cascade (prompt_prefix_file,
// prompt_suffix_file, system_prompt_file) — reading each path from fsys
// relative to baseDir instead of from disk. It mirrors the semantics of
// dippin's parser.ResolveFileDirectives exactly (the #175 prefix/suffix
// cascade with the `prompt_prefix: none` / `prompt_suffix: none` opt-outs, the
// #72 shared system-prompt fallback, and the #248 body-less passthrough skip),
// composing prompts through the exported ir.ComposePrompt so the join
// separator cannot drift.
//
// STOPGAP: this duplicates dippin's resolver because dippin only ships a
// disk-backed variant, and tracker's embedded built-ins (tracker_workflows.go)
// live in an embed.FS where a disk resolver cannot find their sidecars. It
// should be deleted in favor of the upstream fs.FS variant once it lands —
// see the "dippin-lang ResolveFileDirectivesFS follow-up" issue. The parity
// test in dippin_resolve_fs_test.go pins this copy to dippin's behavior.
//
// Path safety: dippin's disk checks (symlink containment, size cap, TOCTOU
// hardening) do not apply — an fs.FS such as embed.FS is immutable and
// validated at compile time — but a directive path that is absolute or
// contains a `..` segment is still rejected so the containment contract is
// preserved for any fsys.
func ResolveFileDirectivesFS(w *ir.Workflow, fsys fs.FS, baseDir string) error {
	if w == nil {
		return fmt.Errorf("resolve file directives: nil workflow")
	}
	if fsys == nil {
		return fmt.Errorf("resolve file directives: nil fs")
	}
	r := fsDirectiveResolver{fsys: fsys, baseDir: baseDir}
	cascade, err := r.loadCascade(&w.Defaults)
	if err != nil {
		return err
	}
	for _, n := range w.Nodes {
		if err := r.resolveNode(n, cascade); err != nil {
			return err
		}
	}
	return nil
}

// fsPromptCascade holds the resolved defaults prompt prefix/suffix and the
// shared system-prompt fallback, loaded once per workflow.
type fsPromptCascade struct {
	prefix, suffix string
	systemPrompt   string
}

type fsDirectiveResolver struct {
	fsys    fs.FS
	baseDir string
}

func (r fsDirectiveResolver) loadCascade(d *ir.WorkflowDefaults) (fsPromptCascade, error) {
	c := fsPromptCascade{prefix: d.PromptPrefix, suffix: d.PromptSuffix}
	if err := r.loadInto(&c.prefix, d.PromptPrefixFile, "defaults", "prompt_prefix_file"); err != nil {
		return c, err
	}
	if err := r.loadInto(&c.suffix, d.PromptSuffixFile, "defaults", "prompt_suffix_file"); err != nil {
		return c, err
	}
	if err := r.loadInto(&c.systemPrompt, d.SystemPromptFile, "defaults", "system_prompt_file"); err != nil {
		return c, err
	}
	return c, nil
}

func (r fsDirectiveResolver) resolveNode(n *ir.Node, cascade fsPromptCascade) error {
	switch cfg := n.Config.(type) {
	case ir.ToolConfig:
		if err := r.loadInto(&cfg.Command, cfg.CommandFile, n.ID, "command_file"); err != nil {
			return err
		}
		n.Config = cfg
	case ir.AgentConfig:
		if err := r.resolveAgent(&cfg, n.ID, cascade); err != nil {
			return err
		}
		n.Config = cfg
	}
	return nil
}

func (r fsDirectiveResolver) resolveAgent(cfg *ir.AgentConfig, nodeID string, cascade fsPromptCascade) error {
	if err := r.loadInto(&cfg.Prompt, cfg.PromptFile, nodeID, "prompt_file"); err != nil {
		return err
	}
	if err := r.loadInto(&cfg.SystemPrompt, cfg.SystemPromptFile, nodeID, "system_prompt_file"); err != nil {
		return err
	}
	// #72: an agent that set no system prompt of its own inherits the shared
	// defaults system_prompt_file; its own (inline or file) always wins.
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = cascade.systemPrompt
	}
	include := ""
	if err := r.loadInto(&include, cfg.PromptInclude, nodeID, "prompt_include"); err != nil {
		return err
	}
	applyPromptCascadeFS(cfg, include, cascade)
	return nil
}

// applyPromptCascadeFS wraps the body with the defaults prefix/suffix (#175),
// honouring the node-level `prompt_prefix: none` / `prompt_suffix: none`
// opt-outs. #248: a body-less passthrough agent (no own prompt, no include —
// e.g. a declared start:/exit: node) stays body-less; the cascade wraps a
// prompt, it never synthesizes one.
func applyPromptCascadeFS(cfg *ir.AgentConfig, include string, cascade fsPromptCascade) {
	if cfg.Prompt == "" && include == "" {
		return
	}
	prefix, suffix := cascade.prefix, cascade.suffix
	if cfg.PromptPrefix == "none" {
		prefix = ""
	}
	if cfg.PromptSuffix == "none" {
		suffix = ""
	}
	cfg.Prompt = ir.ComposePrompt(prefix, cfg.Prompt, include, suffix)
}

// loadInto reads p (relative to baseDir) from fsys into *dst; no-op when p is
// empty. As in dippin, *dst is empty whenever p is set — the parser rejects a
// node that declares both the inline value and its *_file twin.
func (r fsDirectiveResolver) loadInto(dst *string, p, nodeID, directive string) error {
	if p == "" {
		return nil
	}
	contents, err := r.readDirectiveFile(p)
	if err != nil {
		return fmt.Errorf("node %q %s: %w", nodeID, directive, err)
	}
	*dst = string(contents)
	return nil
}

// readDirectiveFile rejects absolute and parent-escaping paths, then reads
// baseDir/p from fsys. Errors name only the user-written path.
func (r fsDirectiveResolver) readDirectiveFile(p string) ([]byte, error) {
	if path.IsAbs(p) || filepath.IsAbs(p) {
		return nil, fmt.Errorf("absolute paths not allowed: %q", p)
	}
	for _, seg := range strings.FieldsFunc(p, func(c rune) bool { return c == '/' || c == '\\' }) {
		if seg == ".." {
			return nil, fmt.Errorf("path %q resolves outside source directory", p)
		}
	}
	full := path.Join(r.baseDir, p)
	contents, err := fs.ReadFile(r.fsys, full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("file %q not found", p)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read file %q: %w", p, err)
	}
	return contents, nil
}
