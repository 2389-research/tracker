// ABOUTME: Doctor Git Requires check — previews the run-time git preflight gate.
// ABOUTME: Split from tracker_doctor.go (#453) — behavior-preserving extraction.
package tracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

// checkGitRequires evaluates the workflow's `requires:` list against the
// current environment and the resolved --git= policy. Runs only when a
// pipeline file is provided. The result mirrors what would happen at
// `tracker run` time, so users can preview the gate without burning spend.
//
// Policy modeling matches pipeline.Preflight:
//   - off  → Skip (bypass)
//   - auto + workflow doesn't require git → OK
//   - require / init → forces the check regardless of workflow
//   - missing git → Error (downgraded to Warn under warn policy)
//   - missing repo + policy != init → Error (downgraded to Warn under warn)
//   - missing repo + policy == init: model the auto-init outcome:
//     safety latches would pass → OK with hint ("auto-init would
//     create .git here at run start")
//     safety latches would refuse → Error with the latch reason
//     This avoids the false-positive Error the previous implementation
//     reported when --git=init --allow-init would actually succeed.
func checkGitRequires(ctx context.Context, cfg DoctorConfig) CheckResult {
	out := CheckResult{Name: "Git Requires"}

	if cfg.PipelineFile == "" {
		out.Status = CheckStatusSkip
		out.Message = "no pipeline file provided"
		return out
	}

	graph, loadMsg, ok := loadGraphForGitRequires(ctx, cfg.PipelineFile)
	if !ok {
		out.Status = CheckStatusSkip
		out.Message = loadMsg
		return out
	}

	policy := cfg.gitCfg.policy
	requiresGit, hasUnknownDeps := scanGitRequiresDeps(&out, graph.RequiredDeps())

	// Off-bypass comes AFTER the dep scan so unrecognized-dep warnings
	// still surface under --git=off. Top-level Status escalates to Warn
	// when any details warn, so `tracker doctor`'s exit code reflects
	// the diagnostic (Doctor() counts CheckResult.Status, not Details).
	if policy == GitPreflightOff {
		return gitRequiresOffBypass(out, hasUnknownDeps)
	}

	if policy == GitPreflightRequire || policy == GitPreflightInit {
		requiresGit = true
	}
	if !requiresGit {
		return gitRequiresNotRequired(out, hasUnknownDeps)
	}
	return evaluateGitState(ctx, out, cfg, hasUnknownDeps)
}

// scanGitRequiresDeps classifies the workflow's declared deps, surfacing
// unrecognized entries as CheckDetail warnings so the doctor preview matches
// what runtime pipeline.Preflight emits on stderr. The off-bypass in the
// caller must not silence these — runtime preflight scans deps before its own
// off bypass for the same reason (forward-declared deps stay visible).
func scanGitRequiresDeps(out *CheckResult, deps []string) (requiresGit, hasUnknownDeps bool) {
	for _, d := range deps {
		switch strings.ToLower(strings.TrimSpace(d)) {
		case "":
			// empty entry; skip
		case "git":
			requiresGit = true
		default:
			hasUnknownDeps = true
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusWarn,
				Message: fmt.Sprintf("requires %q is not yet implemented; runtime will warn and continue", d),
			})
		}
	}
	return requiresGit, hasUnknownDeps
}

// gitRequiresOffBypass renders the --git=off result, still escalating to Warn
// when unrecognized deps were surfaced.
func gitRequiresOffBypass(out CheckResult, hasUnknownDeps bool) CheckResult {
	if hasUnknownDeps {
		out.Status = CheckStatusWarn
		out.Message = "--git=off; bypassing git check (unrecognized requires: entries surfaced as warnings)"
	} else {
		out.Status = CheckStatusSkip
		out.Message = "--git=off; bypassing"
	}
	return out
}

// gitRequiresNotRequired renders the result when git isn't required; unknown
// deps may still warrant a top-level Warn.
func gitRequiresNotRequired(out CheckResult, hasUnknownDeps bool) CheckResult {
	if hasUnknownDeps {
		out.Status = CheckStatusWarn
		out.Message = "workflow does not require git; unrecognized requires: entries surfaced as warnings"
	} else {
		out.Status = CheckStatusOK
		out.Message = "workflow does not require git"
	}
	return out
}

// evaluateGitState probes the working directory's git state and routes to the
// matching remediation: probe failure, git-not-installed, bare repo, no repo
// (auto-init preview), or born-HEAD verification.
func evaluateGitState(ctx context.Context, out CheckResult, cfg DoctorConfig, hasUnknownDeps bool) CheckResult {
	policy := cfg.gitCfg.policy
	installed, isRepo, isBare, probeErr := probeGitForDoctor(ctx, cfg.WorkDir)
	if probeErr != nil {
		// ctx cancellation or unexpected execution failure. Treat as Error
		// so the operator sees the actual cause rather than a misleading
		// "not a repo" preview.
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("git probe failed: %v", probeErr)
		out.Hint = "if the context was cancelled, retry; otherwise investigate the PATH/permissions"
		return out
	}
	if !installed {
		out.Status = doctorStatusForPolicy(policy, CheckStatusError)
		out.Message = "workflow requires git, but git is not in PATH"
		out.Hint = "install git (brew install git / apt install git / https://git-scm.com)"
		return out
	}
	if isBare {
		// Inside a bare repo (or .git dir): "git init" here is wrong; the
		// user needs to operate from a checkout/worktree. Distinct
		// remediation from the plain non-repo case below.
		out.Status = doctorStatusForPolicy(policy, CheckStatusError)
		out.Message = fmt.Sprintf("workflow requires a working tree; %s is a bare git repository (no work tree)", cfg.WorkDir)
		out.Hint = "cd into a checkout of this repo (clone or worktree) and run from there"
		return out
	}
	if !isRepo {
		return gitRequiresNoRepo(ctx, out, cfg, hasUnknownDeps)
	}
	return gitRequiresBornHEAD(ctx, out, cfg, hasUnknownDeps)
}

// gitRequiresNoRepo handles a workdir that is not inside a repo: model the
// auto-init outcome under --git=init --allow-init, else report the plain
// not-a-repo error.
func gitRequiresNoRepo(ctx context.Context, out CheckResult, cfg DoctorConfig, hasUnknownDeps bool) CheckResult {
	policy := cfg.gitCfg.policy
	if policy == GitPreflightInit && cfg.gitCfg.allowInit {
		return gitRequiresAutoInitPreview(ctx, out, cfg, hasUnknownDeps)
	}
	out.Status = doctorStatusForPolicy(policy, CheckStatusError)
	out.Message = fmt.Sprintf("workflow requires a git repository; %s is not inside one", cfg.WorkDir)
	// Offer both paths so a workdir with existing files doesn't end up with a
	// born-but-empty HEAD (Copilot:3261104615) — the `--allow-empty` path is
	// correct only for an empty workdir; a dir with source files almost always
	// wants `git add .` first.
	out.Hint = "run `git init && git add . && git commit -m initial` to capture existing files, OR `git init && git commit --allow-empty -m initial` for an empty baseline, OR `tracker <workflow> --git=init --allow-init` in an empty directory"
	return out
}

// gitRequiresAutoInitPreview models what runAutoInit would do under
// --git=init --allow-init: it gates on (a) safety latches and (b) the
// workdir-content latch (refuses if the dir has files outside `.git`), so
// `doctor --git=init --allow-init` doesn't say OK where the runtime refuses.
func gitRequiresAutoInitPreview(ctx context.Context, out CheckResult, cfg DoctorConfig, hasUnknownDeps bool) CheckResult {
	if latchErr := pipeline.SafetyLatches(ctx, cfg.WorkDir); latchErr != nil {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("auto-init would refuse: %v", latchErr)
		out.Hint = "cd into a project subdirectory, or run `git init` manually"
		return out
	}
	hasContent, contentErr := pipeline.WorkdirHasContent(cfg.WorkDir)
	if contentErr != nil {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("could not scan workdir for auto-init preview: %v", contentErr)
		out.Hint = "check filesystem permissions on the workdir"
		return out
	}
	if hasContent {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("auto-init would refuse: %s is not empty (auto-init makes an empty initial commit; user files would stay untracked)", cfg.WorkDir)
		out.Hint = "stage your own initial commit: `git init && git add . && git commit -m initial`"
		return out
	}
	// Auto-init preview is OK. Preserve unknown-dependency warn severity at
	// the check level (CodeRabbit:3260803551) — pre-fix this branch returned
	// CheckStatusOK unconditionally, suppressing the warning even though the
	// individual unrecognized-dep warnings had been emitted.
	if hasUnknownDeps {
		out.Status = CheckStatusWarn
		out.Message = fmt.Sprintf("workflow requires git; --git=init --allow-init would auto-init %s at run start (unrecognized requires: entries surfaced as warnings)", cfg.WorkDir)
	} else {
		out.Status = CheckStatusOK
		out.Message = fmt.Sprintf("workflow requires git; --git=init --allow-init would auto-init %s at run start", cfg.WorkDir)
	}
	out.Hint = ".git will be created here at run start, before the first node executes"
	return out
}

// gitRequiresBornHEAD handles a workdir that IS a repo: verify HEAD is born —
// the same probe Preflight uses, so the doctor preview agrees with the runtime
// check (Copilot:3260568737).
func gitRequiresBornHEAD(ctx context.Context, out CheckResult, cfg DoctorConfig, hasUnknownDeps bool) CheckResult {
	policy := cfg.gitCfg.policy
	born, headErr := pipeline.HasBornHEAD(ctx, cfg.WorkDir)
	if headErr != nil {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("could not verify HEAD: %v", headErr)
		out.Hint = "retry if ctx was cancelled; otherwise investigate the repo state"
		return out
	}
	if !born {
		out.Status = doctorStatusForPolicy(policy, CheckStatusError)
		out.Message = fmt.Sprintf("workflow requires a git repository with at least one commit; %s has no commits (unborn HEAD)", cfg.WorkDir)
		// Quote the workdir so paths containing spaces / special chars
		// produce copy/pasteable commands (Copilot:3260796999,
		// CodeRabbit:3260803559).
		out.Hint = fmt.Sprintf("create an initial commit: `git -C %q commit --allow-empty -m initial` (or `git -C %q add . && git -C %q commit -m initial` to capture existing files)", cfg.WorkDir, cfg.WorkDir, cfg.WorkDir)
		return out
	}
	if hasUnknownDeps {
		// Git satisfied but unknown deps still warrant a top-level Warn
		// so `tracker doctor`'s exit code reflects the diagnostic.
		out.Status = CheckStatusWarn
		out.Message = "workflow requires git; env satisfies it (unrecognized requires: entries surfaced as warnings)"
	} else {
		out.Status = CheckStatusOK
		out.Message = "workflow requires git; env satisfies it"
	}
	return out
}

// loadGraphForGitRequires loads the entry graph from a `.dip` source file
// OR a `.dipx` bundle, returning the same `*pipeline.Graph` shape either
// way. The doctor's Git Requires check needs `graph.RequiredDeps()` and
// nothing else — running tracker's full bundle validation would duplicate
// what checkPipelineBundle already covers. Returns (nil, "...", false)
// when the file can't be loaded; the caller maps that to CheckStatusSkip.
//
// .dipx bundles are routed through pipeline.LoadDipxBundle so a
// `tracker doctor <bundle.dipx>` invocation accurately previews what
// runtime preflight (which also goes through LoadDipxBundle internally
// in the loader path) would see. Pre-fix the .dipx branch fell through
// to parsePipelineSource which choked on ZIP bytes and silently Skip'd
// — bundle inputs got no Git Requires preview at all.
func loadGraphForGitRequires(ctx context.Context, pipelineFile string) (*pipeline.Graph, string, bool) {
	if strings.EqualFold(filepath.Ext(pipelineFile), ".dipx") {
		entry, _, _, _, err := pipeline.LoadDipxBundle(ctx, pipelineFile)
		if err != nil {
			return nil, fmt.Sprintf("cannot load bundle %s: %v", pipelineFile, err), false
		}
		return entry, "", true
	}
	fileBytes, err := os.ReadFile(pipelineFile)
	if err != nil {
		return nil, fmt.Sprintf("cannot read %s: %v", pipelineFile, err), false
	}
	graph, err := parsePipelineSource(string(fileBytes), detectSourceFormat(string(fileBytes)))
	if err != nil {
		return nil, fmt.Sprintf("cannot parse %s: %v", pipelineFile, err), false
	}
	return graph, "", true
}

// doctorStatusForPolicy maps preflight policy to a CheckStatus, downgrading
// to warn when policy == warn.
func doctorStatusForPolicy(policy GitPreflight, hardStatus CheckStatus) CheckStatus {
	if policy == GitPreflightWarn {
		return CheckStatusWarn
	}
	return hardStatus
}

// probeGitForDoctor performs the same two probes as pipeline.checkGit but
// without reaching into the pipeline-internal helper. Local copy keeps the
// doctor file's dependency surface clean.
//
// Uses `--is-inside-work-tree` (NOT `--git-dir`) so bare repositories don't
// count as "repo OK": workflows that declare `requires: git` need a work
// tree for `git commit`/`git merge`, both of which fail in a bare repo.
// Matches the pipeline.checkGit fix from the same review pass.
// probeGitForDoctor returns (installed, isRepo, isBare, err). Mirrors
// pipeline.checkGit's contract:
//   - installed=false means git missing from PATH (benign)
//   - isRepo=true means inside a real work tree
//   - isBare=true means inside a bare repo or .git directory (no work tree)
//   - all three false means outside any repo (plain dir)
//   - err is non-nil only on cancellation or unexpected execution failure;
//     "not a repo" stderr discrimination keeps dubious-ownership /
//     safe.directory failures from being mis-classified as benign
//
// Bare distinction is necessary so the doctor preview can emit the right
// remediation: bare-repo users need "cd into a checkout," not "git init."
func probeGitForDoctor(ctx context.Context, workDir string) (installed bool, isRepo bool, isBare bool, err error) {
	if _, lerr := exec.LookPath("git"); lerr != nil {
		if errors.Is(lerr, exec.ErrNotFound) {
			return false, false, false, nil
		}
		return false, false, false, fmt.Errorf("locate git in PATH: %w", lerr)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", workDir, "rev-parse", "--is-inside-work-tree")
	// Strip sensitive env AND force LANG/LC_ALL=C so the "not a git
	// repository" classifier below can rely on stable English stderr
	// regardless of operator locale. Pre-fix the doctor on a localized
	// install would have reported a plain non-repo as `git rev-parse
	// refused: <translated phrase>` instead of the documented
	// not-a-repo remediation.
	cmd.Env = pipeline.GitProbeEnv()
	out, runErr := cmd.Output()
	if runErr == nil {
		return classifyGitWorkTree(string(out))
	}
	return classifyGitProbeError(ctx, runErr)
}

// classifyGitWorkTree maps successful `rev-parse --is-inside-work-tree` stdout
// to the (installed, isRepo, isBare) probe contract.
func classifyGitWorkTree(out string) (installed bool, isRepo bool, isBare bool, err error) {
	stdout := strings.TrimSpace(out)
	switch stdout {
	case "true":
		return true, true, false, nil
	case "false":
		return true, false, true, nil
	default:
		return true, false, false, fmt.Errorf("git rev-parse --is-inside-work-tree: unexpected output %q", stdout)
	}
}

// classifyGitProbeError discriminates a failed git probe: context
// cancellation, benign "not a repo" stderr, dubious-ownership / safe.directory
// refusals, and unexpected execution failures.
func classifyGitProbeError(ctx context.Context, runErr error) (installed bool, isRepo bool, isBare bool, err error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return true, false, false, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		if strings.Contains(string(exitErr.Stderr), "not a git repository") {
			return true, false, false, nil // expected — outside any repo
		}
		return true, false, false, fmt.Errorf("git rev-parse refused: %s", strings.TrimSpace(string(exitErr.Stderr)))
	}
	return true, false, false, fmt.Errorf("git rev-parse --is-inside-work-tree: %w", runErr)
}
