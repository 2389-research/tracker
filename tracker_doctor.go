// ABOUTME: Library API for preflight health checks.
// ABOUTME: Pure read-only — no network probes unless ProbeProviders: true.
package tracker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/google"
	"github.com/2389-research/tracker/llm/openai"
	"github.com/2389-research/tracker/llm/openaicompat"
	"github.com/2389-research/tracker/pipeline"
)

// PinnedDippinVersion is the dippin-lang version from go.mod.
// Keep in sync with the require line in go.mod.
const PinnedDippinVersion = "v0.20.0"

// DoctorConfig configures a Doctor() run.
type DoctorConfig struct {
	// WorkDir is the working directory to check. If empty, os.Getwd() is used.
	WorkDir string
	// Backend is the agent backend ("", "native", "claude-code"). When
	// "claude-code", a missing claude binary is a hard error.
	Backend string
	// ProbeProviders, when true, makes a minimal network call to each
	// configured provider to verify auth. Default false — key presence only.
	ProbeProviders bool
	// PipelineFile, when non-empty, adds a "Pipeline File" check that parses
	// and validates the given .dip / .dot file.
	PipelineFile string
	// TrackerVersion and TrackerCommit are surfaced in the "Version
	// Compatibility" check. They are populated by the CLI from build-time
	// ldflags; library callers may leave them empty.
	TrackerVersion string
	TrackerCommit  string
}

// DoctorReport is the structured result of a Doctor() call.
type DoctorReport struct {
	Checks   []CheckResult `json:"checks"`
	OK       bool          `json:"ok"`
	Warnings int           `json:"warnings"`
	Errors   int           `json:"errors"`
}

// CheckResult is one section of a DoctorReport.
type CheckResult struct {
	Name    string        `json:"name"`
	Status  string        `json:"status"` // "ok" | "warn" | "error" | "skip"
	Message string        `json:"message,omitempty"`
	Hint    string        `json:"hint,omitempty"`
	Details []CheckDetail `json:"details,omitempty"`
}

// CheckDetail is one sub-line within a CheckResult — used for per-item
// status lines (per-provider, per-binary, per-subdirectory).
type CheckDetail struct {
	Status  string `json:"status"` // "ok" | "warn" | "error" | "hint" — "hint" is used for informational sub-items (e.g. optional providers not configured)
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// Doctor runs a suite of preflight checks and returns a structured report.
//
// By default Doctor makes no network calls: provider configuration is
// detected via env-var presence and basic format validation. Set
// cfg.ProbeProviders = true to additionally make a 1-token API call per
// provider to verify auth. The CLI's "tracker doctor" command sets that
// flag; library callers should leave it false unless they specifically
// want live credential verification.
//
// Write side effects (gitignore fix-up, workdir creation prompts) are NOT
// performed by Doctor — callers inspect the report and apply any fixes
// themselves.
func Doctor(cfg DoctorConfig) (*DoctorReport, error) {
	if cfg.WorkDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cannot determine working directory: %w", err)
		}
		cfg.WorkDir = wd
	}

	r := &DoctorReport{}
	r.Checks = append(r.Checks,
		checkEnvWarningsLib(),
		checkProvidersLib(cfg.ProbeProviders),
		checkDippinLib(),
		checkVersionCompatLib(cfg.TrackerVersion, cfg.TrackerCommit),
		checkOtherBinariesLib(cfg.Backend),
		checkWorkdirLib(cfg.WorkDir),
		checkArtifactDirsLib(cfg.WorkDir),
		checkDiskSpaceLib(cfg.WorkDir),
	)
	if cfg.PipelineFile != "" {
		r.Checks = append(r.Checks, checkPipelineFileLib(cfg.PipelineFile))
	}

	r.OK = true
	for _, c := range r.Checks {
		switch c.Status {
		case "warn":
			r.Warnings++
		case "error":
			r.Errors++
			r.OK = false
		}
	}
	return r, nil
}

// checkEnvWarningsLib warns when opt-in security overrides are active.
func checkEnvWarningsLib() CheckResult {
	dangerousVars := map[string]string{
		"TRACKER_PASS_ENV":      "passes all env vars to tool subprocesses (security risk)",
		"TRACKER_PASS_API_KEYS": "passes API keys to tool subprocesses (security risk)",
	}
	var found []string
	for envVar, desc := range dangerousVars {
		if os.Getenv(envVar) != "" {
			found = append(found, fmt.Sprintf("%s (%s)", envVar, desc))
		}
	}
	if len(found) == 0 {
		return CheckResult{Name: "Environment Warnings", Status: "ok", Message: "no dangerous environment variables detected"}
	}
	return CheckResult{
		Name:    "Environment Warnings",
		Status:  "warn",
		Message: fmt.Sprintf("dangerous variables set: %s", strings.Join(found, "; ")),
		Hint:    "unset TRACKER_PASS_ENV and TRACKER_PASS_API_KEYS to restore default security posture",
	}
}

type providerDef struct {
	name         string
	envVars      []string
	defaultModel string
	buildAdapter func(key string) (llm.ProviderAdapter, error)
}

var knownProviders = []providerDef{
	{
		name:         "Anthropic",
		envVars:      []string{"ANTHROPIC_API_KEY"},
		defaultModel: "claude-haiku-4-5",
		buildAdapter: func(key string) (llm.ProviderAdapter, error) {
			var opts []anthropic.Option
			if base := ResolveProviderBaseURL("anthropic"); base != "" {
				opts = append(opts, anthropic.WithBaseURL(base))
			}
			return anthropic.New(key, opts...), nil
		},
	},
	{
		name:         "OpenAI",
		envVars:      []string{"OPENAI_API_KEY"},
		defaultModel: "gpt-4o-mini",
		buildAdapter: func(key string) (llm.ProviderAdapter, error) {
			var opts []openai.Option
			if base := ResolveProviderBaseURL("openai"); base != "" {
				opts = append(opts, openai.WithBaseURL(base))
			}
			return openai.New(key, opts...), nil
		},
	},
	{
		name:         "OpenAI-Compat",
		envVars:      []string{"OPENAI_COMPAT_API_KEY"},
		defaultModel: "gpt-4o-mini",
		buildAdapter: func(key string) (llm.ProviderAdapter, error) {
			var opts []openaicompat.Option
			if base := ResolveProviderBaseURL("openai-compat"); base != "" {
				opts = append(opts, openaicompat.WithBaseURL(base))
			}
			return openaicompat.New(key, opts...), nil
		},
	},
	{
		name:         "Gemini",
		envVars:      []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		defaultModel: "gemini-2.0-flash",
		buildAdapter: func(key string) (llm.ProviderAdapter, error) {
			var opts []google.Option
			if base := ResolveProviderBaseURL("gemini"); base != "" {
				opts = append(opts, google.WithBaseURL(base))
			}
			return google.New(key, opts...), nil
		},
	},
}

// checkProvidersLib reports on each configured LLM provider. When probe
// is true, a 1-token API call verifies auth for each configured provider.
func checkProvidersLib(probe bool) CheckResult {
	out := CheckResult{Name: "LLM Providers"}
	var configuredNames []string
	var missingNames []string
	hasProviderErrors := false

	for _, p := range knownProviders {
		key, envName := findProviderKey(p.envVars)
		if key == "" {
			missingNames = append(missingNames, p.name)
			continue
		}
		masked := maskKey(key)
		if !isValidAPIKey(p.name, key) {
			out.Details = append(out.Details, CheckDetail{
				Status:  "error",
				Message: fmt.Sprintf("%-15s %s=%s (invalid format)", p.name, envName, masked),
				Hint:    fmt.Sprintf("%s keys should match expected format — run `tracker setup`", p.name),
			})
			hasProviderErrors = true
			continue
		}
		if probe && p.buildAdapter != nil {
			authOk, authMsg := probeProvider(p, key)
			if !authOk {
				out.Details = append(out.Details, CheckDetail{
					Status:  "error",
					Message: fmt.Sprintf("%-15s %s=%s (auth failed: %s)", p.name, envName, masked, authMsg),
					Hint:    fmt.Sprintf("your %s key is invalid or expired — export a fresh key or run `tracker setup`", p.name),
				})
				hasProviderErrors = true
				continue
			}
			out.Details = append(out.Details, CheckDetail{
				Status:  "ok",
				Message: fmt.Sprintf("%-15s %s=%s (auth verified)", p.name, envName, masked),
			})
		} else {
			out.Details = append(out.Details, CheckDetail{
				Status:  "ok",
				Message: fmt.Sprintf("%-15s %s=%s", p.name, envName, masked),
			})
		}
		configuredNames = append(configuredNames, p.name)
	}

	if len(configuredNames) == 0 {
		for _, name := range missingNames {
			for _, pd := range knownProviders {
				if pd.name == name {
					out.Details = append(out.Details, CheckDetail{
						Status:  "error",
						Message: fmt.Sprintf("%-15s %s not set", pd.name, pd.envVars[0]),
					})
					break
				}
			}
		}
		out.Status = "error"
		out.Message = "no LLM providers configured"
		out.Hint = "run `tracker setup` or export ANTHROPIC_API_KEY / OPENAI_API_KEY / GEMINI_API_KEY"
		return out
	}

	if len(missingNames) > 0 {
		// "not configured" is informational when at least one provider works —
		// rendered as a hint line, not an error or warning, so Status=hint.
		out.Details = append(out.Details, CheckDetail{
			Status:  "hint",
			Message: fmt.Sprintf("not configured: %s (optional)", strings.Join(missingNames, ", ")),
		})
	}

	if hasProviderErrors {
		out.Status = "warn"
	} else {
		out.Status = "ok"
	}
	if probe {
		out.Message = fmt.Sprintf("%d provider(s) configured and auth verified", len(configuredNames))
	} else {
		out.Message = fmt.Sprintf("%d provider(s) configured: %s", len(configuredNames), strings.Join(configuredNames, ", "))
	}
	return out
}

func findProviderKey(envVars []string) (key, envName string) {
	for _, e := range envVars {
		if v := os.Getenv(e); v != "" {
			return v, e
		}
	}
	return "", ""
}

func probeProvider(p providerDef, key string) (bool, string) {
	adapter, err := p.buildAdapter(key)
	if err != nil {
		return false, fmt.Sprintf("build adapter: %v", err)
	}
	client, err := llm.NewClient(llm.WithProvider(adapter))
	if err != nil {
		return false, fmt.Sprintf("create client: %v", err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	maxTok := 1
	req := &llm.Request{
		Model:     p.defaultModel,
		Messages:  []llm.Message{llm.UserMessage("ping")},
		MaxTokens: &maxTok,
	}
	_, err = client.Complete(ctx, req)
	if err != nil {
		msg := err.Error()
		if isAuthError(msg) {
			return false, "invalid or expired API key"
		}
		return false, trimErrMsg(msg, 80)
	}
	return true, ""
}

func isAuthError(msg string) bool {
	lower := strings.ToLower(msg)
	for _, kw := range []string{"401", "403", "unauthorized", "authentication", "invalid api key", "api key", "forbidden"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func trimErrMsg(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	return msg[:maxLen] + "..."
}

// checkDippinLib verifies the dippin binary is installed. The full "dippin
// <ver> at <path>" string goes into the details so the CLI can print a
// per-item line; the composite summary carries the shorter "dippin <ver>"
// form. Historically the CLI emits both lines.
func checkDippinLib() CheckResult {
	path, err := exec.LookPath("dippin")
	if err != nil {
		return CheckResult{
			Name:    "Dippin Language",
			Status:  "error",
			Message: "dippin binary not found in PATH",
			Hint:    "install from https://github.com/2389-research/dippin-lang  (required for pipeline linting)",
		}
	}
	ver := getDippinVersion(path)
	return CheckResult{
		Name:   "Dippin Language",
		Status: "ok",
		Details: []CheckDetail{{
			Status:  "ok",
			Message: fmt.Sprintf("dippin %s at %s", ver, path),
		}},
		Message: fmt.Sprintf("dippin %s", ver),
	}
}

func getDippinVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		out, err = exec.CommandContext(ctx, path, "version").CombinedOutput()
		if err != nil {
			return "(version unknown)"
		}
	}
	ver := strings.TrimSpace(string(out))
	ver = strings.TrimPrefix(ver, "dippin ")
	ver = strings.TrimPrefix(ver, "version ")
	if ver == "" {
		return "(version unknown)"
	}
	return ver
}

// checkVersionCompatLib verifies the installed dippin version matches the
// go.mod pin (on major and minor). trackerVersion / trackerCommit, when
// non-empty, are surfaced as a detail line.
func checkVersionCompatLib(trackerVersion, trackerCommit string) CheckResult {
	out := CheckResult{Name: "Version Compatibility"}
	if trackerVersion != "" {
		msg := fmt.Sprintf("tracker   %s", trackerVersion)
		if trackerCommit != "" {
			msg = fmt.Sprintf("tracker   %s (commit %s)", trackerVersion, trackerCommit)
		}
		out.Details = append(out.Details, CheckDetail{Status: "ok", Message: msg})
	}
	dippinPath, err := exec.LookPath("dippin")
	if err != nil {
		out.Details = append(out.Details, CheckDetail{
			Status:  "warn",
			Message: "dippin not found — skipping version compatibility check",
		})
		out.Status = "warn"
		if trackerVersion != "" {
			out.Message = fmt.Sprintf("tracker %s / dippin not found", trackerVersion)
		} else {
			out.Message = "dippin not found"
		}
		return out
	}
	cliVer := getDippinVersion(dippinPath)
	out.Details = append(out.Details, CheckDetail{
		Status:  "ok",
		Message: fmt.Sprintf("dippin    %s (installed) / %s (go.mod pin)", cliVer, PinnedDippinVersion),
	})

	if mismatch, msg := checkDippinVersionMismatch(cliVer, PinnedDippinVersion); mismatch {
		out.Details = append(out.Details, CheckDetail{
			Status:  "warn",
			Message: fmt.Sprintf("dippin version mismatch: %s", msg),
		})
		out.Status = "warn"
		if trackerVersion != "" {
			out.Message = fmt.Sprintf("tracker %s / dippin %s (mismatched — expected %s)", trackerVersion, cliVer, PinnedDippinVersion)
		} else {
			out.Message = fmt.Sprintf("dippin %s (mismatched — expected %s)", cliVer, PinnedDippinVersion)
		}
		out.Hint = fmt.Sprintf("install dippin %s to match the go.mod pin", PinnedDippinVersion)
		return out
	}
	out.Status = "ok"
	if trackerVersion != "" {
		out.Message = fmt.Sprintf("tracker %s / dippin %s", trackerVersion, cliVer)
	} else {
		out.Message = fmt.Sprintf("dippin %s", cliVer)
	}
	return out
}

// checkDippinVersionMismatch returns (true, reason) if the installed CLI version
// diverges from the pinned version on major or minor components.
func checkDippinVersionMismatch(cliVer, pinned string) (bool, string) {
	cliMajor, cliMinor, ok1 := parseVersionMajorMinor(cliVer)
	pinMajor, pinMinor, ok2 := parseVersionMajorMinor(pinned)
	if !ok1 || !ok2 {
		return false, ""
	}
	if cliMajor != pinMajor {
		return true, fmt.Sprintf("installed major v%d != pinned major v%d", cliMajor, pinMajor)
	}
	if cliMinor != pinMinor {
		return true, fmt.Sprintf("installed v%d.%d != pinned v%d.%d", cliMajor, cliMinor, pinMajor, pinMinor)
	}
	return false, ""
}

var semverRe = regexp.MustCompile(`v?(\d+)\.(\d+)`)

func parseVersionMajorMinor(ver string) (major, minor int, ok bool) {
	m := semverRe.FindStringSubmatch(ver)
	if m == nil {
		return 0, 0, false
	}
	fmt.Sscanf(m[1], "%d", &major)
	fmt.Sscanf(m[2], "%d", &minor)
	return major, minor, true
}

// checkOtherBinariesLib checks for git (recommended) and claude (required
// when backend == "claude-code", optional otherwise).
func checkOtherBinariesLib(backend string) CheckResult {
	out := CheckResult{Name: "Optional Binaries"}
	hasErr := false
	hasWarn := false
	if _, err := exec.LookPath("git"); err == nil {
		out.Details = append(out.Details, CheckDetail{
			Status:  "ok",
			Message: "git found (recommended for pipeline versioning)",
		})
	} else {
		out.Details = append(out.Details, CheckDetail{
			Status:  "warn",
			Message: "git not found in PATH (recommended for pipeline versioning)",
		})
		hasWarn = true
	}
	claudePath, claudeErr := exec.LookPath("claude")
	if claudeErr == nil {
		claudeVer := getBinaryVersion(claudePath, "--version")
		out.Details = append(out.Details, CheckDetail{
			Status:  "ok",
			Message: fmt.Sprintf("claude %s (for --backend claude-code)", claudeVer),
		})
	} else if backend == "claude-code" {
		out.Details = append(out.Details, CheckDetail{
			Status:  "error",
			Message: "claude CLI not found in PATH (required for --backend claude-code)",
			Hint:    "install the Claude CLI from https://claude.ai/code",
		})
		hasErr = true
	} else {
		out.Details = append(out.Details, CheckDetail{
			Status:  "warn",
			Message: "claude not found in PATH (install for --backend claude-code support)",
		})
		hasWarn = true
	}
	switch {
	case hasErr:
		out.Status = "error"
		out.Message = "required binary missing for selected backend"
		out.Hint = "install the Claude CLI from https://claude.ai/code"
	case hasWarn:
		out.Status = "warn"
		out.Message = "some optional binaries missing"
		out.Hint = "install git and/or the Claude CLI to unlock all tracker features"
	default:
		out.Status = "ok"
		out.Message = "optional binaries available"
	}
	return out
}

func getBinaryVersion(path, flag string) string {
	out, err := exec.Command(path, flag).CombinedOutput()
	if err != nil {
		return "(version unknown)"
	}
	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	if len(lines) == 0 {
		return "(version unknown)"
	}
	return strings.TrimSpace(lines[0])
}

// checkWorkdirLib verifies the working directory exists and is writable.
// It also detects missing .gitignore entries but does NOT modify the file —
// the CLI applies any fix-up separately.
func checkWorkdirLib(workdir string) CheckResult {
	out := CheckResult{Name: "Working Directory"}
	info, err := os.Stat(workdir)
	if err != nil {
		out.Status = "error"
		out.Message = fmt.Sprintf("%s does not exist", workdir)
		out.Hint = fmt.Sprintf("create the directory: mkdir -p %s", workdir)
		return out
	}
	if !info.IsDir() {
		out.Status = "error"
		out.Message = fmt.Sprintf("%s is not a directory", workdir)
		out.Hint = "point --workdir at a directory, not a file"
		return out
	}
	f, err := os.CreateTemp(workdir, ".tracker_probe_*")
	if err != nil {
		out.Status = "error"
		out.Message = fmt.Sprintf("%s is not writable", workdir)
		out.Hint = fmt.Sprintf("check permissions: chmod u+w %s", workdir)
		return out
	}
	f.Close()
	os.Remove(f.Name())

	hasWarn := false
	home, _ := os.UserHomeDir()
	if workdir == home || workdir == "/" {
		out.Details = append(out.Details, CheckDetail{
			Status:  "warn",
			Message: fmt.Sprintf("%s (risk of accidental data loss — use a project subdirectory)", workdir),
		})
		hasWarn = true
	}

	// Detect missing .gitignore entries without modifying the file.
	if missing := missingGitignoreEntries(workdir); missing != "" {
		out.Details = append(out.Details, CheckDetail{
			Status:  "warn",
			Message: missing,
		})
		hasWarn = true
	}

	out.Details = append(out.Details, CheckDetail{
		Status:  "ok",
		Message: fmt.Sprintf("%s (writable)", workdir),
	})
	if hasWarn {
		out.Status = "warn"
		out.Message = fmt.Sprintf("%s is writable (with warnings)", workdir)
	} else {
		out.Status = "ok"
		out.Message = fmt.Sprintf("%s is writable", workdir)
	}
	return out
}

// missingGitignoreEntries returns a warning message if .gitignore is missing
// or lacks .tracker/, runs/, or .ai/ entries. Returns empty string if OK.
// Read-only — does not modify the file.
func missingGitignoreEntries(workdir string) string {
	gitignorePath := filepath.Join(workdir, ".gitignore")
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		return ".gitignore not found — add .tracker/, runs/, and .ai/ to prevent committing run artifacts"
	}
	entries := make(map[string]bool)
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries[strings.TrimRight(line, "/")] = true
	}
	want := []struct {
		bare    string
		display string
	}{
		{".tracker", ".tracker/"},
		{"runs", "runs/"},
		{".ai", ".ai/"},
	}
	var missing []string
	for _, w := range want {
		if !entries[w.bare] {
			missing = append(missing, w.display)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf(".gitignore missing entries: %s", strings.Join(missing, ", "))
	}
	return ""
}

// checkArtifactDirsLib verifies the .ai artifact directory is usable
// (either exists and is writable, or can be created).
func checkArtifactDirsLib(workdir string) CheckResult {
	out := CheckResult{Name: "Artifact Directories"}
	allOk := true
	aiDir := filepath.Join(workdir, ".ai")
	if info, err := os.Stat(aiDir); err == nil {
		switch {
		case !info.IsDir():
			out.Details = append(out.Details, CheckDetail{
				Status:  "error",
				Message: ".ai is not a directory",
			})
			allOk = false
		case !isDirWritable(aiDir):
			out.Details = append(out.Details, CheckDetail{
				Status:  "error",
				Message: fmt.Sprintf("%s exists but is not writable", aiDir),
				Hint:    fmt.Sprintf("check permissions: chmod u+w %s", aiDir),
			})
			allOk = false
		default:
			out.Details = append(out.Details, CheckDetail{
				Status:  "ok",
				Message: fmt.Sprintf("%s exists and is writable", aiDir),
			})
		}
	} else {
		if isDirWritable(workdir) {
			out.Details = append(out.Details, CheckDetail{
				Status:  "ok",
				Message: fmt.Sprintf("%s will be created on first run", aiDir),
			})
		} else {
			out.Details = append(out.Details, CheckDetail{
				Status:  "error",
				Message: fmt.Sprintf("%s cannot be created (parent not writable)", aiDir),
			})
			allOk = false
		}
	}
	if allOk {
		out.Status = "ok"
		out.Message = "artifact directories writable"
		return out
	}
	// Promote to "error" if any detail is an error (not just a warning).
	out.Status = "warn"
	for _, d := range out.Details {
		if d.Status == "error" {
			out.Status = "error"
			break
		}
	}
	out.Message = "some artifact directories have permission issues"
	out.Hint = "fix directory permissions: chmod u+w .ai"
	return out
}

func isDirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".tracker_probe_*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// checkDiskSpaceLib warns when available disk space under workdir is low.
// The implementation is platform-specific; see tracker_doctor_unix.go and
// tracker_doctor_windows.go.

// checkPipelineFileLib parses and validates a pipeline file.
func checkPipelineFileLib(pipelineFile string) CheckResult {
	out := CheckResult{Name: "Pipeline File"}
	if _, err := os.Stat(pipelineFile); err != nil {
		out.Status = "error"
		out.Message = fmt.Sprintf("%s does not exist", pipelineFile)
		out.Hint = fmt.Sprintf("check the file path: %s", pipelineFile)
		return out
	}
	if !strings.HasSuffix(pipelineFile, ".dip") && !strings.HasSuffix(pipelineFile, ".dot") {
		out.Details = append(out.Details, CheckDetail{
			Status:  "warn",
			Message: fmt.Sprintf("%s is not a .dip or .dot file — may not be a valid pipeline", pipelineFile),
		})
	}
	fileBytes, err := os.ReadFile(pipelineFile)
	if err != nil {
		out.Status = "error"
		out.Message = fmt.Sprintf("%s: read error: %v", pipelineFile, err)
		out.Hint = "check file permissions"
		return out
	}
	graph, err := parsePipelineSource(string(fileBytes), detectSourceFormat(string(fileBytes)))
	if err != nil {
		out.Status = "error"
		out.Message = fmt.Sprintf("%s: parse error: %v", pipelineFile, err)
		out.Hint = "run `tracker validate " + pipelineFile + "` for full details"
		return out
	}
	registry := buildDoctorValidationRegistry()
	ve := pipeline.ValidateAllWithLint(graph, registry)
	if ve != nil && len(ve.Errors) > 0 {
		for _, e := range ve.Errors {
			out.Details = append(out.Details, CheckDetail{
				Status:  "error",
				Message: fmt.Sprintf("error: %s", e),
			})
		}
		for _, w := range ve.Warnings {
			out.Details = append(out.Details, CheckDetail{
				Status:  "warn",
				Message: w,
			})
		}
		out.Status = "error"
		out.Message = fmt.Sprintf("%s failed validation (%d error(s))", pipelineFile, len(ve.Errors))
		out.Hint = "run `tracker validate " + pipelineFile + "` for full details"
		return out
	}
	if ve != nil && len(ve.Warnings) > 0 {
		for _, w := range ve.Warnings {
			out.Details = append(out.Details, CheckDetail{
				Status:  "warn",
				Message: w,
			})
		}
		out.Details = append(out.Details, CheckDetail{
			Status: "ok",
			Message: fmt.Sprintf("%s valid (%d nodes, %d edges, %d warning(s))",
				pipelineFile, len(graph.Nodes), len(graph.Edges), len(ve.Warnings)),
		})
		out.Status = "warn"
		out.Message = fmt.Sprintf("%s valid with %d warning(s)", pipelineFile, len(ve.Warnings))
		return out
	}
	out.Details = append(out.Details, CheckDetail{
		Status:  "ok",
		Message: fmt.Sprintf("%s valid (%d nodes, %d edges)", pipelineFile, len(graph.Nodes), len(graph.Edges)),
	})
	out.Status = "ok"
	out.Message = fmt.Sprintf("%s is valid", pipelineFile)
	return out
}

// buildDoctorValidationRegistry creates a handler registry stocked with
// every known handler name. Used for pipeline validation without actually
// executing any handlers.
func buildDoctorValidationRegistry() *pipeline.HandlerRegistry {
	registry := pipeline.NewHandlerRegistry()
	names := []string{
		"codergen", "tool", "subgraph", "spawn",
		"start", "exit", "conditional",
		"wait.human", "parallel", "parallel.fan_in", "manager_loop",
	}
	for _, name := range names {
		registry.Register(&doctorMockHandler{name: name})
	}
	return registry
}

type doctorMockHandler struct{ name string }

func (h *doctorMockHandler) Name() string { return h.name }

func (h *doctorMockHandler) Execute(_ context.Context, _ *pipeline.Node, _ *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func isValidAPIKey(provider string, key string) bool {
	if key == "" {
		return false
	}
	switch provider {
	case "Anthropic":
		return strings.HasPrefix(key, "sk-ant-") && len(key) > 10
	case "OpenAI", "OpenAI-Compat":
		return strings.HasPrefix(key, "sk-") && len(key) > 10
	case "Gemini":
		return len(key) > 10
	}
	return len(key) > 5
}
