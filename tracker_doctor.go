// ABOUTME: Library API for preflight health checks.
// ABOUTME: Pure read-only — no network probes unless ProbeProviders: true.
package tracker

import (
	"context"
	"fmt"
	"os"
)

// PinnedDippinVersion is the dippin-lang version from go.mod. Kept in sync
// with go.mod by TestPinnedDippinVersionMatchesGoMod.
const PinnedDippinVersion = "v0.59.1"

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
	applyDoctorOptions(&cfg, opts)
	if err := resolveDoctorWorkDir(&cfg); err != nil {
		return nil, err
	}

	r := &DoctorReport{}
	r.Checks = baseDoctorChecks(ctx, cfg)
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

	tallyDoctorReport(r)
	return r, nil
}

// applyDoctorOptions applies each non-nil functional option to cfg.
func applyDoctorOptions(cfg *DoctorConfig, opts []DoctorOption) {
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(cfg)
	}
}

// resolveDoctorWorkDir defaults cfg.WorkDir to the current working directory
// when unset.
func resolveDoctorWorkDir(cfg *DoctorConfig) error {
	if cfg.WorkDir != "" {
		return nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}
	cfg.WorkDir = wd
	return nil
}

// baseDoctorChecks runs the checks that always execute regardless of config.
func baseDoctorChecks(ctx context.Context, cfg DoctorConfig) []CheckResult {
	return []CheckResult{
		checkEnvWarnings(),
		checkProviders(ctx, cfg.ProbeProviders),
		checkDippin(ctx),
		checkVersionCompat(ctx, cfg.versionInfo.version, cfg.versionInfo.commit),
		checkOtherBinaries(ctx, cfg.Backend),
		checkWorkdir(cfg.WorkDir),
		checkArtifactDirs(cfg.WorkDir),
		checkDiskSpace(cfg.WorkDir),
	}
}

// tallyDoctorReport sets OK and counts warnings/errors across all checks.
func tallyDoctorReport(r *DoctorReport) {
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
}
