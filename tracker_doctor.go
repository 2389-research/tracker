// ABOUTME: Library API for preflight health checks.
// ABOUTME: Pure read-only — no network probes unless ProbeProviders: true.
package tracker

import (
	"context"
	"errors"
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

// PinnedDippinVersion is the dippin-lang version from go.mod. Kept in sync
// with go.mod by TestPinnedDippinVersionMatchesGoMod.
const PinnedDippinVersion = "v0.38.0"

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
	// versionInfo is populated by WithVersionInfo. Unexported so callers
	// use the functional option rather than setting CLI-specific fields.
	versionInfo versionInfo
	// gitCfg is populated by WithGitConfig. Drives the Git Requires check.
	// Unexported; zero value (set=false) implies GitPreflightAuto.
	gitCfg doctorGitConfig
}

// doctorGitConfig carries the resolved --git/--allow-init values into the
// Git Requires check.
type doctorGitConfig struct {
	policy    GitPreflight
	allowInit bool
	set       bool
}

// versionInfo carries CLI-provided build metadata into a Doctor run.
type versionInfo struct {
	version string
	commit  string
}

// DoctorOption configures a Doctor run via a functional option.
type DoctorOption func(*DoctorConfig)

// WithVersionInfo attaches a tracker version and commit hash for display in
// the "Version Compatibility" check. CLI callers populate these from
// build-time ldflags; library callers typically do not need this.
func WithVersionInfo(version, commit string) DoctorOption {
	return func(c *DoctorConfig) {
		c.versionInfo = versionInfo{version: version, commit: commit}
	}
}

// WithGitConfig sets the git preflight policy considered by the Git Requires
// check. Library callers that don't set this get GitPreflightAuto behavior
// (respect workflow `requires:`). CLI doctor mode passes --git/--allow-init
// through this option.
func WithGitConfig(policy GitPreflight, allowInit bool) DoctorOption {
	return func(c *DoctorConfig) {
		c.gitCfg = doctorGitConfig{policy: policy, allowInit: allowInit, set: true}
	}
}

// DoctorReport is the structured result of a Doctor() call.
type DoctorReport struct {
	Checks   []CheckResult `json:"checks"`
	OK       bool          `json:"ok"`
	Warnings int           `json:"warnings"`
	Errors   int           `json:"errors"`
}

// CheckStatus is the status of a CheckResult or CheckDetail. Enum-like
// typed string so consumers can switch-exhaust. "hint" is only valid on
// CheckDetail.Status (informational sub-items such as optional providers
// not configured).
type CheckStatus string

// CheckStatus values.
const (
	CheckStatusOK    CheckStatus = "ok"
	CheckStatusWarn  CheckStatus = "warn"
	CheckStatusError CheckStatus = "error"
	CheckStatusSkip  CheckStatus = "skip"
	CheckStatusHint  CheckStatus = "hint"
)

// CheckResult is one section of a DoctorReport.
type CheckResult struct {
	Name    string        `json:"name"`
	Status  CheckStatus   `json:"status"` // "ok" | "warn" | "error" | "skip"
	Message string        `json:"message,omitempty"`
	Hint    string        `json:"hint,omitempty"`
	Details []CheckDetail `json:"details,omitempty"`
}

// CheckDetail is one sub-line within a CheckResult — used for per-item
// status lines (per-provider, per-binary, per-subdirectory).
type CheckDetail struct {
	Status  CheckStatus `json:"status"` // "ok" | "warn" | "error" | "hint"
	Message string      `json:"message"`
	Hint    string      `json:"hint,omitempty"`
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
// Provider probes and binary version lookups honor ctx: cancelling the
// context aborts in-flight checks. A nil context is treated as
// context.Background().
//
// Write side effects (gitignore fix-up, workdir creation prompts) are NOT
// performed by Doctor — callers inspect the report and apply any fixes
// themselves.
func Doctor(ctx context.Context, cfg DoctorConfig, opts ...DoctorOption) (*DoctorReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&cfg)
	}
	if cfg.WorkDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cannot determine working directory: %w", err)
		}
		cfg.WorkDir = wd
	}

	r := &DoctorReport{}
	r.Checks = append(r.Checks,
		checkEnvWarnings(),
		checkProviders(ctx, cfg.ProbeProviders),
		checkDippin(ctx),
		checkVersionCompat(ctx, cfg.versionInfo.version, cfg.versionInfo.commit),
		checkOtherBinaries(ctx, cfg.Backend),
		checkWorkdir(cfg.WorkDir),
		checkArtifactDirs(cfg.WorkDir),
		checkDiskSpace(cfg.WorkDir),
	)
	// Gateway routing caveats only matter when a gateway is actually
	// configured (#277). Appending unconditionally would add noise to the
	// common no-gateway run, so gate on the env presence here.
	if os.Getenv("TRACKER_GATEWAY_URL") != "" || os.Getenv("TRACKER_GATEWAY_KIND") != "" {
		r.Checks = append(r.Checks, checkGatewayRouting())
	}
	if cfg.PipelineFile != "" {
		r.Checks = append(r.Checks,
			checkPipelineFile(cfg.PipelineFile),
			checkGitRequires(ctx, cfg),
		)
	}

	r.OK = true
	for _, c := range r.Checks {
		switch c.Status {
		case CheckStatusWarn:
			r.Warnings++
		case CheckStatusError:
			r.Errors++
			r.OK = false
		}
	}
	return r, nil
}

// checkEnvWarnings warns when opt-in security overrides are active.
func checkEnvWarnings() CheckResult {
	dangerousVars := map[string]string{
		"TRACKER_PASS_ENV":      "passes all env vars to tool subprocesses (security risk)",
		"TRACKER_PASS_API_KEYS": "passes API keys to tool subprocesses (security risk)",
	}
	var found []string
	for envVar, desc := range dangerousVars {
		if os.Getenv(envVar) == "1" {
			found = append(found, fmt.Sprintf("%s (%s)", envVar, desc))
		}
	}
	if len(found) == 0 {
		return CheckResult{Name: "Environment Warnings", Status: CheckStatusOK, Message: "no dangerous environment variables detected"}
	}
	return CheckResult{
		Name:    "Environment Warnings",
		Status:  CheckStatusWarn,
		Message: fmt.Sprintf("dangerous variables set: %s", strings.Join(found, "; ")),
		Hint:    "unset TRACKER_PASS_ENV and TRACKER_PASS_API_KEYS to restore default security posture",
	}
}

// gatewayBaseURLEnvVars maps a provider label to its per-provider *_BASE_URL
// override env var. The Gateway Routing check uses this to report which
// overrides win over TRACKER_GATEWAY_URL. Order mirrors knownProviders.
var gatewayBaseURLEnvVars = []struct {
	provider string
	envVar   string
}{
	{"anthropic", "ANTHROPIC_BASE_URL"},
	{"openai", "OPENAI_BASE_URL"},
	{"openai-compat", "OPENAI_COMPAT_BASE_URL"},
	{"gemini", "GEMINI_BASE_URL"},
}

// providerBaseURLEnvVar returns the canonical *_BASE_URL env var name for a
// provider label via gatewayBaseURLEnvVars — naive ToUpper(name) renders
// invalid names for dashed labels (OPENAI-COMPAT_BASE_URL).
func providerBaseURLEnvVar(name string) string {
	for _, e := range gatewayBaseURLEnvVars {
		if e.provider == name {
			return e.envVar
		}
	}
	return strings.ReplaceAll(strings.ToUpper(name), "-", "_") + "_BASE_URL"
}

// checkGatewayRouting surfaces non-fatal gateway routing caveats (#277). It
// runs only when TRACKER_GATEWAY_URL or TRACKER_GATEWAY_KIND is set (see
// Doctor) and emits informational notes — never warnings or errors, since
// every condition it reports is an intentional configuration:
//
//   - B.1 bedrock masquerade: when OpenAI traffic actually routes through the
//     bedrock gateway (KIND=bedrock + a gateway URL + OPENAI_API_KEY, with no
//     OPENAI_BASE_URL override), gpt-* / o*-* model strings route to Claude
//     today because AWS Bedrock has no OpenAI models yet. Surfaced once at
//     setup time rather than as a per-session runtime warning.
//   - B.2 per-provider precedence: a *_BASE_URL override silently wins over
//     TRACKER_GATEWAY_URL for that provider.
func checkGatewayRouting() CheckResult {
	out := CheckResult{Name: "Gateway Routing", Status: CheckStatusOK}

	gatewayURL := strings.TrimRight(os.Getenv("TRACKER_GATEWAY_URL"), "/")
	kind := os.Getenv("TRACKER_GATEWAY_KIND")
	kindLabel := kind
	if kindLabel == "" {
		kindLabel = string(GatewayKindCFAIG) + " (default)"
	}

	// Context line: describe what routing is in effect.
	if gatewayURL != "" {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusOK,
			Message: fmt.Sprintf("TRACKER_GATEWAY_URL=%s (kind=%s)", gatewayURL, kindLabel),
		})
	} else {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusOK,
			Message: fmt.Sprintf("TRACKER_GATEWAY_KIND=%s (no TRACKER_GATEWAY_URL — per-provider *_BASE_URL or SDK defaults in effect)", kindLabel),
		})
	}

	notes := 0

	// B.1 — OpenAI→Claude masquerade under the bedrock gateway. The note fires
	// only when OpenAI traffic actually traverses the bedrock gateway:
	//   - kind must be bedrock;
	//   - a gateway URL must be configured — without one, openai resolves to
	//     the SDK default (api.openai.com), so there is no gateway and no
	//     masquerade;
	//   - OPENAI_BASE_URL must be unset — it wins over the gateway in the
	//     resolver, so when set, openai bypasses the gateway. The B.2
	//     precedence note covers that case instead.
	//
	// Residual gap: an OPENAI_BASE_URL pointed explicitly at the bedrock
	// gateway (e.g. <gateway>/v1) would still masquerade, but an arbitrary
	// URL can't be reliably recognized as a gateway endpoint, so we defer to
	// the B.2 note rather than guess.
	if kind == string(GatewayKindBedrock) && gatewayURL != "" && os.Getenv("OPENAI_BASE_URL") == "" {
		if key, _ := findProviderKey([]string{"OPENAI_API_KEY"}); key != "" {
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusHint,
				Message: "TRACKER_GATEWAY_KIND=bedrock: gpt-* / o*-* model strings route to Claude Sonnet 4.6 today (the bedrock gateway translates because AWS hasn't added OpenAI to Bedrock yet). When AWS adds it, the gateway updates its mapping without tracker changes.",
			})
			notes++
		}
	}

	// B.2 — per-provider *_BASE_URL overrides win over the gateway URL.
	if gatewayURL != "" {
		var overridden []string
		for _, p := range gatewayBaseURLEnvVars {
			if os.Getenv(p.envVar) != "" {
				overridden = append(overridden, fmt.Sprintf("%s (%s)", p.provider, p.envVar))
			}
		}
		if len(overridden) > 0 {
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusHint,
				Message: fmt.Sprintf("per-provider overrides win over TRACKER_GATEWAY_URL: %s", strings.Join(overridden, ", ")),
			})
			notes++
		}
	}

	switch {
	case gatewayURL == "":
		// Only the kind is set; without a URL the resolver never routes via a
		// gateway, so don't imply one is in use.
		out.Message = fmt.Sprintf("TRACKER_GATEWAY_KIND=%s set without TRACKER_GATEWAY_URL — no gateway routing in effect", kindLabel)
	case notes > 0:
		out.Message = fmt.Sprintf("gateway configured (%d routing note(s))", notes)
	default:
		out.Message = "gateway configured (no routing caveats)"
	}
	return out
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
			// Strict resolver so doctor reports the same gateway-route
			// refusal that `tracker run` would hard-fail on.
			base, err := ResolveProviderBaseURLStrict("anthropic")
			if err != nil {
				return nil, err
			}
			var opts []anthropic.Option
			if base != "" {
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
			base, err := ResolveProviderBaseURLStrict("openai")
			if err != nil {
				return nil, err
			}
			var opts []openai.Option
			if base != "" {
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
			base, err := ResolveProviderBaseURLStrict("openai-compat")
			if err != nil {
				return nil, err
			}
			var opts []openaicompat.Option
			if base != "" {
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
			base, err := ResolveProviderBaseURLStrict("gemini")
			if err != nil {
				return nil, err
			}
			var opts []google.Option
			if base != "" {
				opts = append(opts, google.WithBaseURL(base))
			}
			return google.New(key, opts...), nil
		},
	},
}

var probeProviderFn = probeProvider

// checkProviders reports on each configured LLM provider. When probe
// is true, a 1-token API call verifies auth for each configured provider.
func checkProviders(ctx context.Context, probe bool) CheckResult {
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
				Status:  CheckStatusError,
				Message: fmt.Sprintf("%-15s %s=%s (invalid format)", p.name, envName, masked),
				Hint:    fmt.Sprintf("%s keys should match expected format — run `tracker setup`", p.name),
			})
			hasProviderErrors = true
			continue
		}
		if probe && p.buildAdapter != nil {
			ok, probeMsg, isAuth := probeProviderFn(ctx, p, key)
			if !ok {
				detail := CheckDetail{Status: CheckStatusError}
				if isAuth {
					detail.Message = fmt.Sprintf("%-15s %s=%s (auth failed: %s)", p.name, envName, masked, probeMsg)
					detail.Hint = fmt.Sprintf("your %s key is invalid or expired — export a fresh key or run `tracker setup`", p.name)
				} else {
					// DNS, timeout, transport, context cancel, or other non-auth failure.
					// Do NOT tell the user to rotate a working key.
					detail.Message = fmt.Sprintf("%-15s %s=%s (probe failed: %s)", p.name, envName, masked, probeMsg)
					detail.Hint = fmt.Sprintf("probe for %s failed on network/transport — verify connectivity and %s before rotating keys", p.name, providerBaseURLEnvVar(p.name))
				}
				out.Details = append(out.Details, detail)
				hasProviderErrors = true
				continue
			}
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusOK,
				Message: fmt.Sprintf("%-15s %s=%s (auth verified)", p.name, envName, masked),
			})
		} else {
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusOK,
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
						Status:  CheckStatusError,
						Message: fmt.Sprintf("%-15s %s not set", pd.name, pd.envVars[0]),
					})
					break
				}
			}
		}
		out.Status = CheckStatusError
		out.Message = "no LLM providers configured"
		out.Hint = "run `tracker setup` or export ANTHROPIC_API_KEY / OPENAI_API_KEY / GEMINI_API_KEY"
		return out
	}

	if len(missingNames) > 0 {
		// "not configured" is informational when at least one provider works —
		// rendered as a hint line, not an error or warning, so Status=hint.
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusHint,
			Message: fmt.Sprintf("not configured: %s (optional)", strings.Join(missingNames, ", ")),
		})
	}

	if hasProviderErrors {
		out.Status = CheckStatusWarn
	} else {
		out.Status = CheckStatusOK
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

// probeProvider returns (ok, msg, isAuthFailure). The third return lets the
// caller distinguish an actual auth failure (rotate-the-key guidance) from
// a network/transport/timeout failure (don't rotate good keys).
func probeProvider(ctx context.Context, p providerDef, key string) (bool, string, bool) {
	adapter, err := p.buildAdapter(key)
	if err != nil {
		return false, fmt.Sprintf("build adapter: %v", err), false
	}
	client, err := llm.NewClient(llm.WithProvider(adapter))
	if err != nil {
		return false, fmt.Sprintf("create client: %v", err), false
	}
	defer client.Close()
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	maxTok := 16
	req := &llm.Request{
		Model:     p.defaultModel,
		Messages:  []llm.Message{llm.UserMessage("ping")},
		MaxTokens: &maxTok,
	}
	_, err = client.Complete(probeCtx, req)
	if err != nil {
		msg := err.Error()
		if isAuthError(msg) {
			return false, "invalid or expired API key", true
		}
		// Sanitize FIRST, then trim. If we trim first, a key that
		// straddles the 80-char boundary gets cut into a shorter prefix
		// that no longer matches the regex, leaking the prefix. Sanitize
		// the full message, then trim whatever's left.
		return false, trimErrMsg(sanitizeProviderError(msg), 80), false
	}
	return true, "", false
}

// sanitizeProviderError strips API keys and bearer tokens from provider error
// text so they never land in CheckDetail.Message (which library consumers may
// log or forward to webhooks).
var (
	apiKeyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{6,}`),
		regexp.MustCompile(`sk-[A-Za-z0-9_\-]{10,}`),
		regexp.MustCompile(`AIza[0-9A-Za-z_\-]{20,}`),
	}
	bearerPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]{6,}`)
)

func sanitizeProviderError(msg string) string {
	for _, re := range apiKeyPatterns {
		msg = re.ReplaceAllString(msg, "[redacted-key]")
	}
	msg = bearerPattern.ReplaceAllString(msg, "Bearer [redacted]")
	return msg
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

// checkDippin verifies the dippin binary is installed. The full "dippin
// <ver> at <path>" string goes into the details so the CLI can print a
// per-item line; the composite summary carries the shorter "dippin <ver>"
// form. Historically the CLI emits both lines.
func checkDippin(ctx context.Context) CheckResult {
	path, err := exec.LookPath("dippin")
	if err != nil {
		return CheckResult{
			Name:    "Dippin Language",
			Status:  CheckStatusError,
			Message: "dippin binary not found in PATH",
			Hint:    "install from https://github.com/2389-research/dippin-lang  (required for pipeline linting)",
		}
	}
	ver := getDippinVersion(ctx, path)
	return CheckResult{
		Name:   "Dippin Language",
		Status: CheckStatusOK,
		Details: []CheckDetail{{
			Status:  CheckStatusOK,
			Message: fmt.Sprintf("dippin %s at %s", ver, path),
		}},
		Message: fmt.Sprintf("dippin %s", ver),
	}
}

func getDippinVersion(ctx context.Context, path string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, path, "--version").CombinedOutput()
	if err != nil {
		out, err = exec.CommandContext(probeCtx, path, "version").CombinedOutput()
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

// checkVersionCompat verifies the installed dippin version matches the
// go.mod pin (on major and minor). trackerVersion / trackerCommit, when
// non-empty, are surfaced as a detail line.
func checkVersionCompat(ctx context.Context, trackerVersion, trackerCommit string) CheckResult {
	out := CheckResult{Name: "Version Compatibility"}
	if trackerVersion != "" {
		msg := fmt.Sprintf("tracker   %s", trackerVersion)
		if trackerCommit != "" {
			msg = fmt.Sprintf("tracker   %s (commit %s)", trackerVersion, trackerCommit)
		}
		out.Details = append(out.Details, CheckDetail{Status: CheckStatusOK, Message: msg})
	}
	dippinPath, err := exec.LookPath("dippin")
	if err != nil {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: "dippin not found — skipping version compatibility check",
		})
		out.Status = CheckStatusWarn
		if trackerVersion != "" {
			out.Message = fmt.Sprintf("tracker %s / dippin not found", trackerVersion)
		} else {
			out.Message = "dippin not found"
		}
		return out
	}
	cliVer := getDippinVersion(ctx, dippinPath)
	out.Details = append(out.Details, CheckDetail{
		Status:  CheckStatusOK,
		Message: fmt.Sprintf("dippin    %s (installed) / %s (go.mod pin)", cliVer, PinnedDippinVersion),
	})

	if mismatch, msg := checkDippinVersionMismatch(cliVer, PinnedDippinVersion); mismatch {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: fmt.Sprintf("dippin version mismatch: %s", msg),
		})
		out.Status = CheckStatusWarn
		if trackerVersion != "" {
			out.Message = fmt.Sprintf("tracker %s / dippin %s (mismatched — expected %s)", trackerVersion, cliVer, PinnedDippinVersion)
		} else {
			out.Message = fmt.Sprintf("dippin %s (mismatched — expected %s)", cliVer, PinnedDippinVersion)
		}
		out.Hint = fmt.Sprintf("install dippin %s to match the go.mod pin", PinnedDippinVersion)
		return out
	}
	out.Status = CheckStatusOK
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

// checkOtherBinaries checks for git (recommended) and claude (required
// when backend == "claude-code", optional otherwise).
func checkOtherBinaries(ctx context.Context, backend string) CheckResult {
	out := CheckResult{Name: "Optional Binaries"}
	hasErr := false
	hasWarn := false
	if _, err := exec.LookPath("git"); err == nil {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusOK,
			Message: "git found (recommended for pipeline versioning)",
		})
	} else {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: "git not found in PATH (recommended for pipeline versioning)",
		})
		hasWarn = true
	}
	claudePath, claudeErr := exec.LookPath("claude")
	if claudeErr == nil {
		claudeVer := getBinaryVersion(ctx, claudePath, "--version")
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusOK,
			Message: fmt.Sprintf("claude %s (for --backend claude-code)", claudeVer),
		})
	} else if backend == "claude-code" {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusError,
			Message: "claude CLI not found in PATH (required for --backend claude-code)",
			Hint:    "install the Claude CLI from https://claude.ai/code",
		})
		hasErr = true
	} else {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: "claude not found in PATH (install for --backend claude-code support)",
		})
		hasWarn = true
	}
	switch {
	case hasErr:
		out.Status = CheckStatusError
		out.Message = "required binary missing for selected backend"
		out.Hint = "install the Claude CLI from https://claude.ai/code"
	case hasWarn:
		out.Status = CheckStatusWarn
		out.Message = "some optional binaries missing"
		out.Hint = "install git and/or the Claude CLI to unlock all tracker features"
	default:
		out.Status = CheckStatusOK
		out.Message = "optional binaries available"
	}
	return out
}

func getBinaryVersion(ctx context.Context, path, flag string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, path, flag).CombinedOutput()
	if err != nil {
		return "(version unknown)"
	}
	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	if len(lines) == 0 {
		return "(version unknown)"
	}
	return strings.TrimSpace(lines[0])
}

// checkWorkdir verifies the working directory exists and is writable.
// It also detects missing .gitignore entries but does NOT modify the file —
// the CLI applies any fix-up separately.
func checkWorkdir(workdir string) CheckResult {
	out := CheckResult{Name: "Working Directory"}
	info, err := os.Stat(workdir)
	if err != nil {
		out.Status = CheckStatusError
		switch {
		case os.IsNotExist(err):
			out.Message = fmt.Sprintf("%s does not exist", workdir)
			out.Hint = fmt.Sprintf("create the directory: mkdir -p %s", workdir)
		case os.IsPermission(err):
			out.Message = fmt.Sprintf("permission denied accessing %s", workdir)
			out.Hint = fmt.Sprintf("check permissions on %s or a parent directory", workdir)
		default:
			out.Message = fmt.Sprintf("cannot stat %s: %v", workdir, err)
			out.Hint = "check the path and its parent directories"
		}
		return out
	}
	if !info.IsDir() {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("%s is not a directory", workdir)
		out.Hint = "point --workdir at a directory, not a file"
		return out
	}
	f, err := os.CreateTemp(workdir, ".tracker_probe_*")
	if err != nil {
		out.Status = CheckStatusError
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
			Status:  CheckStatusWarn,
			Message: fmt.Sprintf("%s (risk of accidental data loss — use a project subdirectory)", workdir),
		})
		hasWarn = true
	}

	// Detect missing .gitignore entries without modifying the file.
	if missing := missingGitignoreEntries(workdir); missing != "" {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: missing,
		})
		hasWarn = true
	}

	out.Details = append(out.Details, CheckDetail{
		Status:  CheckStatusOK,
		Message: fmt.Sprintf("%s (writable)", workdir),
	})
	if hasWarn {
		out.Status = CheckStatusWarn
		out.Message = fmt.Sprintf("%s is writable (with warnings)", workdir)
	} else {
		out.Status = CheckStatusOK
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

// checkArtifactDirs verifies the .ai artifact directory is usable
// (either exists and is writable, or can be created).
func checkArtifactDirs(workdir string) CheckResult {
	out := CheckResult{Name: "Artifact Directories"}
	allOk := true
	aiDir := filepath.Join(workdir, ".ai")
	info, err := os.Stat(aiDir)
	switch {
	case err == nil:
		switch {
		case !info.IsDir():
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusError,
				Message: ".ai is not a directory",
			})
			allOk = false
		case !isDirWritable(aiDir):
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusError,
				Message: fmt.Sprintf("%s exists but is not writable", aiDir),
				Hint:    fmt.Sprintf("check permissions: chmod u+w %s", aiDir),
			})
			allOk = false
		default:
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusOK,
				Message: fmt.Sprintf("%s exists and is writable", aiDir),
			})
		}
	case os.IsNotExist(err):
		if isDirWritable(workdir) {
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusOK,
				Message: fmt.Sprintf("%s will be created on first run", aiDir),
			})
		} else {
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusError,
				Message: fmt.Sprintf("%s cannot be created (parent not writable)", aiDir),
			})
			allOk = false
		}
	default:
		// Non-ENOENT stat failure — permission denied, I/O error, etc.
		// Report the real failure instead of pretending .ai is missing.
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusError,
			Message: fmt.Sprintf("cannot inspect %s: %v", aiDir, err),
			Hint:    fmt.Sprintf("check permissions on %s and its parents", aiDir),
		})
		allOk = false
	}
	if allOk {
		out.Status = CheckStatusOK
		out.Message = "artifact directories writable"
		return out
	}
	// Promote to "error" if any detail is an error (not just a warning).
	out.Status = CheckStatusWarn
	for _, d := range out.Details {
		if d.Status == CheckStatusError {
			out.Status = CheckStatusError
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

// checkDiskSpace warns when available disk space under workdir is low.
// The implementation is platform-specific; see tracker_doctor_unix.go and
// tracker_doctor_windows.go.

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

	deps := graph.RequiredDeps()
	policy := cfg.gitCfg.policy

	// Walk declared deps FIRST: classify each and surface unrecognized deps
	// as CheckDetail warnings so the doctor preview matches what runtime
	// pipeline.Preflight will emit on stderr. The off-bypass below must
	// not silence these — runtime preflight scans deps before its own off
	// bypass for the same reason (forward-declared deps stay visible).
	requiresGit := false
	hasUnknownDeps := false
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

	// Off-bypass comes AFTER the dep scan so unrecognized-dep warnings
	// still surface under --git=off. Top-level Status escalates to Warn
	// when any details warn, so `tracker doctor`'s exit code reflects
	// the diagnostic (Doctor() counts CheckResult.Status, not Details).
	if policy == GitPreflightOff {
		if hasUnknownDeps {
			out.Status = CheckStatusWarn
			out.Message = "--git=off; bypassing git check (unrecognized requires: entries surfaced as warnings)"
		} else {
			out.Status = CheckStatusSkip
			out.Message = "--git=off; bypassing"
		}
		return out
	}

	if policy == GitPreflightRequire || policy == GitPreflightInit {
		requiresGit = true
	}
	if !requiresGit {
		// No git required, but unknown deps may still warrant a top-level Warn.
		if hasUnknownDeps {
			out.Status = CheckStatusWarn
			out.Message = "workflow does not require git; unrecognized requires: entries surfaced as warnings"
		} else {
			out.Status = CheckStatusOK
			out.Message = "workflow does not require git"
		}
		return out
	}

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
		// Auto-init preview: under --git=init, runtime would call
		// runAutoInit which gates on (a) --allow-init or interactive
		// confirmation, (b) safety latches, and (c) the workdir-content
		// latch (refuses if the dir has files outside `.git`). Doctor
		// doesn't have interactivity, so a non-interactive run
		// effectively requires allowInit=true. Model both latches so
		// `doctor --git=init --allow-init` doesn't say OK in a workdir
		// where the runtime would refuse.
		if policy == GitPreflightInit && cfg.gitCfg.allowInit {
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
			// Auto-init preview is OK. Preserve unknown-dependency warn
			// severity at the check level (CodeRabbit:3260803551) — the
			// parallel born-HEAD-success branch below already does this;
			// pre-fix the auto-init branch returned CheckStatusOK
			// unconditionally, suppressing the warning even though the
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
		out.Status = doctorStatusForPolicy(policy, CheckStatusError)
		out.Message = fmt.Sprintf("workflow requires a git repository; %s is not inside one", cfg.WorkDir)
		// Offer both paths so a workdir with existing files doesn't end
		// up with a born-but-empty HEAD (Copilot:3261104615) — the
		// `--allow-empty` path is correct only for an empty workdir; a
		// dir with source files almost always wants `git add .` first.
		out.Hint = "run `git init && git add . && git commit -m initial` to capture existing files, OR `git init && git commit --allow-empty -m initial` for an empty baseline, OR `tracker <workflow> --git=init --allow-init` in an empty directory"
		return out
	}
	// Workdir IS a repo. Verify HEAD is born — same probe Preflight uses,
	// so the doctor preview agrees with the runtime check. Pre-fix the
	// doctor reported "OK" for unborn-HEAD repos while the actual run
	// failed in preflight (Copilot:3260568737).
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
		stdout := strings.TrimSpace(string(out))
		switch stdout {
		case "true":
			return true, true, false, nil
		case "false":
			return true, false, true, nil
		default:
			return true, false, false, fmt.Errorf("git rev-parse --is-inside-work-tree: unexpected output %q", stdout)
		}
	}
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

// checkPipelineFile parses and validates a pipeline file.
func checkPipelineFile(pipelineFile string) CheckResult {
	out := CheckResult{Name: "Pipeline File"}
	if _, err := os.Stat(pipelineFile); err != nil {
		out.Status = CheckStatusError
		switch {
		case os.IsNotExist(err):
			out.Message = fmt.Sprintf("%s does not exist", pipelineFile)
			out.Hint = fmt.Sprintf("check the file path: %s", pipelineFile)
		case os.IsPermission(err):
			out.Message = fmt.Sprintf("permission denied reading %s", pipelineFile)
			out.Hint = fmt.Sprintf("check permissions: chmod +r %s", pipelineFile)
		default:
			out.Message = fmt.Sprintf("cannot stat %s: %v", pipelineFile, err)
			out.Hint = "check the file path and permissions"
		}
		return out
	}
	// .dipx bundles are ZIP archives produced by `dippin pack`, not text source.
	// dispatch through LoadDipxBundle so dipx.Open can verify the manifest and
	// every embedded workflow before we report success. parsePipelineSource
	// below would choke on the ZIP bytes if we fell through.
	if strings.EqualFold(filepath.Ext(pipelineFile), ".dipx") {
		return checkPipelineBundle(pipelineFile)
	}
	hasWarn := false
	if !strings.HasSuffix(pipelineFile, ".dip") && !strings.HasSuffix(pipelineFile, ".dot") {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: fmt.Sprintf("%s is not a .dip, .dot, or .dipx file — may not be a valid pipeline", pipelineFile),
		})
		hasWarn = true
	}
	fileBytes, err := os.ReadFile(pipelineFile)
	if err != nil {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("%s: read error: %v", pipelineFile, err)
		out.Hint = "check file permissions"
		return out
	}
	graph, err := parsePipelineSource(string(fileBytes), detectSourceFormat(string(fileBytes)))
	if err != nil {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("%s: parse error: %v", pipelineFile, err)
		out.Hint = "run `tracker validate " + pipelineFile + "` for full details"
		return out
	}
	registry := buildDoctorValidationRegistry()
	ve := pipeline.ValidateAllWithLint(graph, registry)
	if ve != nil && len(ve.Errors) > 0 {
		for _, e := range ve.Errors {
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusError,
				Message: fmt.Sprintf("error: %s", e),
			})
		}
		for _, w := range ve.Warnings {
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusWarn,
				Message: w,
			})
		}
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("%s failed validation (%d error(s))", pipelineFile, len(ve.Errors))
		out.Hint = "run `tracker validate " + pipelineFile + "` for full details"
		return out
	}
	if ve != nil && len(ve.Warnings) > 0 {
		for _, w := range ve.Warnings {
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusWarn,
				Message: w,
			})
		}
		out.Details = append(out.Details, CheckDetail{
			Status: CheckStatusOK,
			Message: fmt.Sprintf("%s valid (%d nodes, %d edges, %d warning(s))",
				pipelineFile, len(graph.Nodes), len(graph.Edges), len(ve.Warnings)),
		})
		out.Status = CheckStatusWarn
		out.Message = fmt.Sprintf("%s valid with %d warning(s)", pipelineFile, len(ve.Warnings))
		return out
	}
	out.Details = append(out.Details, CheckDetail{
		Status:  CheckStatusOK,
		Message: fmt.Sprintf("%s valid (%d nodes, %d edges)", pipelineFile, len(graph.Nodes), len(graph.Edges)),
	})
	if hasWarn {
		out.Status = CheckStatusWarn
		out.Message = fmt.Sprintf("%s is valid but has warnings", pipelineFile)
	} else {
		out.Status = CheckStatusOK
		out.Message = fmt.Sprintf("%s is valid", pipelineFile)
	}
	return out
}

// checkPipelineBundle handles the .dipx branch of checkPipelineFile. It loads
// the bundle via pipeline.LoadDipxBundle, which verifies SHA-256 hashes and
// converts every embedded workflow. A successful load is sufficient evidence
// that the bundle parses and has a valid shape — doctor does not need to run
// the pipeline.
func checkPipelineBundle(bundlePath string) CheckResult {
	out := CheckResult{Name: "Pipeline File"}
	entry, subgraphs, info, diags, err := pipeline.LoadDipxBundle(context.Background(), bundlePath)
	if err != nil {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("%s: bundle load failed: %v", bundlePath, err)
		out.Hint = "run `tracker validate " + bundlePath + "` for full details"
		return out
	}
	for _, d := range diags {
		out.Details = append(out.Details, CheckDetail{
			Status:  CheckStatusWarn,
			Message: d.String(),
		})
	}
	// Run tracker's semantic validation + lint on the bundled entry graph
	// so .dipx gets the same coverage as the .dip path in checkPipelineFile.
	// dipx.Open + LoadDippinWorkflowFromIR already covered structural
	// validation; this layer adds tracker's handler-aware checks.
	registry := buildDoctorValidationRegistry()
	tracerWarnings := 0
	if ve := pipeline.ValidateAllWithLint(entry, registry); ve != nil {
		for _, e := range ve.Errors {
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusError,
				Message: fmt.Sprintf("error: %s", e),
			})
		}
		for _, w := range ve.Warnings {
			out.Details = append(out.Details, CheckDetail{
				Status:  CheckStatusWarn,
				Message: w,
			})
		}
		if len(ve.Errors) > 0 {
			out.Status = CheckStatusError
			out.Message = fmt.Sprintf("%s failed validation (%d error(s))", bundlePath, len(ve.Errors))
			out.Hint = "run `tracker validate " + bundlePath + "` for full details"
			return out
		}
		tracerWarnings = len(ve.Warnings)
	}
	totalWarnings := len(diags) + tracerWarnings
	out.Details = append(out.Details, CheckDetail{
		Status: CheckStatusOK,
		Message: fmt.Sprintf("%s valid (%d nodes, %d edges, %d subgraph(s), identity %s)",
			bundlePath, len(entry.Nodes), len(entry.Edges), len(subgraphs), info.Identity),
	})
	if totalWarnings > 0 {
		out.Status = CheckStatusWarn
		out.Message = fmt.Sprintf("%s valid with %d warning(s)", bundlePath, totalWarnings)
	} else {
		out.Status = CheckStatusOK
		out.Message = fmt.Sprintf("%s is valid", bundlePath)
	}
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
	return pipeline.Outcome{Status: string(pipeline.OutcomeSuccess)}, nil
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
