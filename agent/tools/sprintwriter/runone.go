// ABOUTME: RunOne authors + audits + writes one sprint file. Decomposed into
// ABOUTME: small helpers (path validation, author pass, audit pass, verdict apply) per #452.
package sprintwriter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/llm"
)

// RunOne authors + audits + writes one sprint file. Caller supplies the contract
// (already loaded) and a fully-resolved output directory. Returns a structured
// result so callers (Execute, dispatch_sprints) can format their own summaries.
func (t *WriteEnrichedSprintTool) RunOne(ctx context.Context, contract, path, description, outputDir string) (*SprintRunResult, error) {
	if err := validateRunOneArgs(path, description); err != nil {
		return nil, err
	}
	cleanedPath, err := sprintPathClean(path)
	if err != nil {
		return nil, err
	}
	if err := sprintPathWithin(cleanedPath, outputDir, path); err != nil {
		return nil, err
	}

	authorResp, err := t.authorPass(ctx, contract, path, description)
	if err != nil {
		return nil, err
	}
	draft := trimEnclosingMarkdownFence(authorResp.Text())

	auditResp, err := t.auditPass(ctx, contract, path, description, draft)
	if err != nil {
		return nil, err
	}

	verdict, final, patchesApplied, auditIn, auditOut := resolveAuditOutcome(path, draft, auditResp)

	fullPath := filepath.Join(outputDir, path)
	if err := t.writeSprintFile(ctx, fullPath, final); err != nil {
		return nil, err
	}

	return &SprintRunResult{
		Path:           path,
		Bytes:          len(final),
		Verdict:        verdict,
		PatchesApplied: patchesApplied,
		Model:          t.model,
		AuthorIn:       authorResp.Usage.InputTokens,
		AuthorOut:      authorResp.Usage.OutputTokens,
		AuditIn:        auditIn,
		AuditOut:       auditOut,
	}, nil
}

// validateRunOneArgs rejects empty path/description before any work.
func validateRunOneArgs(path, description string) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if description == "" {
		return fmt.Errorf("description is required")
	}
	return nil
}

// sprintPathClean rejects absolute paths and parent-escapes, returning the
// cleaned relative path. dispatch_sprints' readDispatchPlan already enforces a
// stricter ^SPRINT-NNN\.md$ regex, but Execute() (callable directly by an
// agent with arbitrary path input) has only this guard.
func sprintPathClean(path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be relative to output_dir, got absolute %q", path)
	}
	cleaned := filepath.Clean(path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q resolves outside output_dir", path)
	}
	return cleaned, nil
}

// sprintPathWithin validates that filepath.Join(outputDir, cleaned) stays
// inside outputDir. origPath is used only for error messages so they name the
// caller's original argument.
func sprintPathWithin(cleaned, outputDir, origPath string) error {
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("resolve output_dir: %w", err)
	}
	absResolved, err := filepath.Abs(filepath.Join(outputDir, cleaned))
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	rel, err := filepath.Rel(absOutput, absResolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes output_dir %q", origPath, outputDir)
	}
	return nil
}

// authorPass is PASS 1 — the author produces the draft.
//
// MaxTokens 16384 accommodates foundation sprints with full data contracts
// (e.g., NIFB SPRINT-001's 18 ORM models + 25 schemas + verbatim conftest +
// per-route algorithm prose hits ~15K output tokens). The provider default
// (~4-8K depending on model) is too small for these and silently truncates.
func (t *WriteEnrichedSprintTool) authorPass(ctx context.Context, contract, path, description string) (*llm.Response, error) {
	authorSystemPrompt := sprintSystemPromptHeader + enrichedSprintExample
	authorUserPrompt := fmt.Sprintf(
		"CONTRACT (project-wide architectural map shared across all sprints):\n\n%s\n\n"+
			"SPRINT TO WRITE: %s\n\n"+
			"Per-sprint description from the architect:\n%s\n\n"+
			"Write the complete enriched sprint markdown for the file above. "+
			"Output ONLY the raw markdown — first line must be the `# Sprint NNN — Title (enriched spec)` heading.",
		contract, path, description,
	)
	authorMaxTokens := 16384
	resp, err := t.client.Complete(ctx, &llm.Request{
		Model:     t.model,
		Provider:  t.provider,
		MaxTokens: &authorMaxTokens,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: []llm.ContentPart{{Kind: llm.KindText, Text: authorSystemPrompt}}},
			llm.UserMessage(authorUserPrompt),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("author pass failed for %s: %w", path, err)
	}
	return resp, nil
}

// auditPass is PASS 2 — the auditor reviews the draft against the within-sprint
// patterns and either returns the draft verbatim (AUDIT-VERDICT: PASS) or a
// patched version (AUDIT-VERDICT: PATCHED). Different system prompt narrows the
// auditor's role; lower temperature makes the audit deterministic.
//
// Provider errors hard-fail per CLAUDE.md — silently shipping an unaudited
// draft would be indistinguishable from a real auditor PASS and could mask
// quota/auth problems for hours. The author-pass already returns on error;
// mirror that here.
//
// MaxTokens 16384 — for PATCHED verdicts the audit may emit several SR blocks;
// for PASS it's a single line. Headroom accommodates large patch sets without
// truncating mid-block.
func (t *WriteEnrichedSprintTool) auditPass(ctx context.Context, contract, path, description, draft string) (*llm.Response, error) {
	auditTemperature := 0.2
	auditMaxTokens := 16384
	auditUserPrompt := fmt.Sprintf(
		"CONTRACT (project-wide architectural map shared across all sprints):\n\n%s\n\n"+
			"SPRINT BEING AUDITED: %s\n\n"+
			"Per-sprint description from the architect:\n%s\n\n"+
			"DRAFT (the author's first attempt) — audit this against the within-sprint patterns:\n\n"+
			"---BEGIN DRAFT---\n%s\n---END DRAFT---\n\n"+
			"Output exactly per the system prompt: `AUDIT-VERDICT: PASS` alone (single line, nothing else) OR `AUDIT-VERDICT: PATCHED` followed by SEARCH/REPLACE blocks. No sprint heading, no commentary, no summary.",
		contract, path, description, draft,
	)
	resp, err := t.client.Complete(ctx, &llm.Request{
		Model:       t.model,
		Provider:    t.provider,
		Temperature: &auditTemperature,
		MaxTokens:   &auditMaxTokens,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: []llm.ContentPart{{Kind: llm.KindText, Text: auditSystemPrompt}}},
			llm.UserMessage(auditUserPrompt),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("write_enriched_sprint: audit pass failed for %s: %w", path, err)
	}
	return resp, nil
}

// resolveAuditOutcome interprets the auditor's response into the final content,
// a verdict string, the patch count, and the audit token usage. A malformed
// audit response falls back to the unaudited draft (PASS-FALLBACK-MALFORMED)
// rather than failing the sprint.
func resolveAuditOutcome(path, draft string, auditResp *llm.Response) (verdict, final string, patchesApplied, auditIn, auditOut int) {
	verdict, final = "PASS", draft
	if auditResp == nil {
		return verdict, final, 0, 0, 0
	}
	auditText := trimEnclosingMarkdownFence(auditResp.Text())
	v, blocks, parseErr := parseAuditResponse(auditText)
	if parseErr != nil {
		fmt.Fprintf(os.Stderr, "write_enriched_sprint: audit response malformed for %s (using draft as-is): %v\n", path, parseErr)
		verdict = "PASS-FALLBACK-MALFORMED"
	} else {
		verdict = v
		if v == "PATCHED" && len(blocks) > 0 {
			final, verdict, patchesApplied = applyAuditPatches(draft, blocks, path)
		}
	}
	return verdict, final, patchesApplied, auditResp.Usage.InputTokens, auditResp.Usage.OutputTokens
}

// applyAuditPatches applies the audit's SR blocks with partial-apply semantics:
// each block is independent, some may fail (e.g., a later block's SEARCH was
// modified by an earlier block's REPLACE); whatever applied ships. Only when
// ZERO blocks apply does it fall back to the unaudited draft.
func applyAuditPatches(draft string, blocks []srBlock, path string) (final, verdict string, applied int) {
	patched, n, skipped := applySRBlocks(draft, blocks)
	for _, s := range skipped {
		fmt.Fprintf(os.Stderr, "write_enriched_sprint: %s for %s (skipped, partial apply continues)\n", s, path)
	}
	if n == 0 {
		return draft, "PASS-FALLBACK-NOMATCH", 0
	}
	verdict = "PATCHED"
	if n < len(blocks) {
		verdict = "PATCHED-PARTIAL"
	}
	return patched, verdict, n
}
