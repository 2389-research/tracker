// ABOUTME: The registry entries — every environment variable Tracker reads, plus the implicit names children honor.
// ABOUTME: Add a row here when adding an os.Getenv; TestEveryGetenvLiteralIsRegistered fails otherwise.
package envpolicy

// v builds one explicit (non-implicit) row.
func v(name string, p Purpose, s Source, since, doc string) Var {
	return Var{Name: name, Purpose: p, Source: s, Since: since, Doc: doc}
}

// imp builds one implicit row: a name Tracker never reads but Go, libc or a
// child process honors. Always ShellOnly. "v0.1.0" because the runtime has
// honored these since the first binary, not since they were registered.
func imp(name string, p Purpose, doc string) Var {
	return Var{Name: name, Purpose: p, Source: ShellOnly, Since: "v0.1.0", Doc: doc, Implicit: true}
}

// table is the registry. Grouped by trust tier for review; Table() sorts.
var table = []Var{
	// ── Project-env tier: the ONLY class a <workdir>/.env may supply ──────────
	v("ANTHROPIC_API_KEY", Credential, ProjectEnv, "v0.1.0", "Anthropic API key (native backend; stripped from claude-code / ACP children by default)"),
	v("OPENAI_API_KEY", Credential, ProjectEnv, "v0.1.0", "OpenAI API key"),
	v("GEMINI_API_KEY", Credential, ProjectEnv, "v0.1.0", "Gemini API key"),
	v("GOOGLE_API_KEY", Credential, ProjectEnv, "v0.1.0", "Gemini API key (alternate name)"),
	v("OPENAI_COMPAT_API_KEY", Credential, ProjectEnv, "v0.20.0", "API key for the openai-compat provider"),
	v("OPENROUTER_API_KEY", Credential, ProjectEnv, "v0.16.0", "OpenRouter key; Tracker never reads it but strips it from ACP children with the other provider keys"),

	// ── Config-env tier: operator's ~/.config/tracker/.env or the shell ───────
	v("ANTHROPIC_BASE_URL", NetworkDestination, ConfigEnv, "v0.1.0", "Anthropic endpoint override; wins over TRACKER_GATEWAY_URL"),
	v("OPENAI_BASE_URL", NetworkDestination, ConfigEnv, "v0.1.0", "OpenAI endpoint override; wins over TRACKER_GATEWAY_URL"),
	v("GEMINI_BASE_URL", NetworkDestination, ConfigEnv, "v0.1.0", "Gemini endpoint override; wins over TRACKER_GATEWAY_URL"),
	v("GOOGLE_BASE_URL", NetworkDestination, ConfigEnv, "v0.17.0", "Gemini endpoint (alternate name); Tracker strips it from ACP children, never reads it"),
	v("OPENAI_COMPAT_BASE_URL", NetworkDestination, ConfigEnv, "v0.20.0", "openai-compat endpoint; required for that provider"),
	v("TRACKER_GATEWAY_URL", NetworkDestination, ConfigEnv, "v0.17.0", "AI gateway root URL; Tracker appends a per-provider suffix (--gateway-url)"),
	v("TRACKER_GATEWAY_KIND", NetworkDestination, ConfigEnv, "v0.36.0", "Gateway path convention: cf-aig (default) or bedrock (--gateway-kind)"),
	v("TRACKER_CODEGEN_MODEL", NetworkDestination, ConfigEnv, "v0.25.0", "Model the generate_code tool sends contracts to; registering the tool when set"),
	v("TRACKER_CODEGEN_PROVIDER", NetworkDestination, ConfigEnv, "v0.25.0", "Provider for TRACKER_CODEGEN_MODEL"),
	v("TRACKER_SPRINT_WRITER_MODEL", NetworkDestination, ConfigEnv, "v0.25.0", "Model the write_enriched_sprint tool uses; registering the tool when set"),
	v("TRACKER_SPRINT_WRITER_PROVIDER", NetworkDestination, ConfigEnv, "v0.25.0", "Provider for TRACKER_SPRINT_WRITER_MODEL"),
	v("TRACKER_ACP_CACHE_READ_RATIO", Cost, ConfigEnv, "v0.24.1", "Fraction of ACP input priced as cache-read, in (0, 1]; lowers reported spend"),
	v("TRACKER_DEBUG", UX, ConfigEnv, "v0.14.0", "Print raw provider response previews on empty responses"),
	v("TRACKER_NO_NOTIFY", UX, ConfigEnv, "v0.13.0", "Disable desktop notifications"),
	v("TRACKER_NO_UPDATE_CHECK", UX, ConfigEnv, "v0.12.0", "Skip the background release check on `tracker run`"),
	v("TRACKER_HERDR", UX, ConfigEnv, "v0.76.0", "Set to 0 to opt out of herdr pane reporting even inside a herdr pane"),

	// ── Shell-only tier: security posture, trusted state, subprocess targets ─
	v("TRACKER_ENV_FILES", SecuritySwitch, ShellOnly, "unreleased", "Which .env files to load: all (default), config, none (--env-files)"),
	v("TRACKER_PASS_ENV", SecuritySwitch, ShellOnly, "v0.16.0", "Set to 1 to pass credential-shaped vars to tool nodes, agent bash, verify and ACP terminal commands"),
	v("TRACKER_PASS_API_KEYS", SecuritySwitch, ShellOnly, "v0.13.0", "Set to 1 to keep provider API keys in the claude-code backend subprocess (bypasses subscription auth)"),
	v("TRACKER_STRIP_ACP_KEYS", SecuritySwitch, ShellOnly, "v0.16.0", "Set to 1 to strip provider keys and base URLs from ACP agent subprocesses"),
	v("TRACKER_FAIL_ON_OVERRIDE", SecuritySwitch, ShellOnly, "v0.35.0", "Set to 1 to exit 2 when a run ends validation_overridden (--fail-on-override)"),
	v("TRACKER_AUDIT_DIR", TrustedState, ShellOnly, "v0.28.0", "Absolute base dir for the secure activity log and authoritative checkpoint"),
	v("XDG_STATE_HOME", TrustedState, ShellOnly, "v0.28.0", "Absolute XDG state dir; secure log base when TRACKER_AUDIT_DIR is unset"),
	v("XDG_CONFIG_HOME", TrustedState, ShellOnly, "v0.3.0", "Absolute XDG config dir; locates ~/.config/tracker/.env"),
	v("LOCALAPPDATA", TrustedState, ShellOnly, "v0.28.0", "Windows per-user state dir; secure log base fallback"),
	v("HERDR_ENV", SubprocessTarget, ShellOnly, "v0.76.0", "Set to 1 by herdr; enables reporting through HERDR_BIN_PATH"),
	v("HERDR_BIN_PATH", SubprocessTarget, ShellOnly, "v0.76.0", "herdr CLI executed for every lifecycle report"),
	v("HERDR_PANE_ID", RunIdentity, ShellOnly, "v0.76.0", "herdr pane the run reports against"),
	v("TRACKER_RUN_ID", Output, ShellOnly, "v0.37.0", "Set by Tracker for tool subprocesses: the run ID (operator values are overridden)"),
	v("TRACKER_RUN_DIR", Output, ShellOnly, "v0.37.0", "Set by Tracker for tool subprocesses: the per-run artifact dir"),
	v("TRACKER_WORKDIR", Output, ShellOnly, "v0.37.0", "Set by Tracker for tool subprocesses: the absolute workdir"),
	v("CI", UX, ShellOnly, "v0.12.0", "Set by CI systems; skips the background release check"),
	v("GOBIN", UX, ShellOnly, "v0.12.0", "Install-method detection for `tracker update`"),
	v("GOPATH", UX, ShellOnly, "v0.12.0", "Install-method detection for `tracker update`"),

	// ── Companion binaries (never load .env files; shell-only by construction) ─
	v("CF_AIG_TOKEN", Credential, ShellOnly, "v0.18.0", "tracker-swebench: Cloudflare AI Gateway auth token forwarded into the container"),
	v("SLACK_BOT_TOKEN", Credential, ShellOnly, "v0.46.0", "trackerbot: Slack bot token"),
	v("SLACK_APP_TOKEN", Credential, ShellOnly, "v0.46.0", "trackerbot: Slack app-level token"),
	v("TRACKERBOT_ALLOWED_USERS", SecuritySwitch, ShellOnly, "v0.46.0", "trackerbot: comma-separated users who may start paid runs"),
	v("TRACKERBOT_BACKEND", SubprocessTarget, ShellOnly, "v0.46.0", "trackerbot: agent backend for runs"),
	v("TRACKERBOT_KEEP_WORKDIRS", UX, ShellOnly, "v0.46.0", "trackerbot: keep per-thread workdirs after a run"),
	v("TRACKERBOT_WORKDIR", TrustedState, ShellOnly, "v0.46.0", "trackerbot: base workdir"),
	v("TRACKERBOT_RUNS", TrustedState, ShellOnly, "v0.46.0", "trackerbot: run record store"),
	v("TRACKERBOT_MAX_CONCURRENT", Cost, ShellOnly, "v0.46.0", "trackerbot: concurrent run cap"),
	v("TRACKERBOT_MAX_COST_CENTS", Cost, ShellOnly, "v0.46.0", "trackerbot: per-run cost cap"),
	v("TRACKERBOT_CONFIRM_OVER_CENTS", Cost, ShellOnly, "v0.46.0", "trackerbot: confirmation threshold"),
	v("TRACKERBOT_MODEL", UX, ShellOnly, "v0.47.0", "trackerbot: intent-resolver model"),
	v("TRACKERCHAT_BACKEND", SubprocessTarget, ShellOnly, "v0.46.0", "trackerchat: agent backend for runs"),
	v("TRACKERCHAT_KEEP_WORKDIRS", UX, ShellOnly, "v0.46.0", "trackerchat: keep per-thread workdirs"),
	v("TRACKERCHAT_WORKDIR", TrustedState, ShellOnly, "v0.46.0", "trackerchat: base workdir"),
	v("TRACKERCHAT_RUNS", TrustedState, ShellOnly, "v0.46.0", "trackerchat: run record store"),
	v("TRACKERCHAT_MAX_COST_CENTS", Cost, ShellOnly, "v0.46.0", "trackerchat: per-run cost cap"),
	v("TRACKERCHAT_MODEL", UX, ShellOnly, "v0.47.0", "trackerchat: intent-resolver model"),
	v("SWEBENCH_INSTANCE", RunIdentity, ShellOnly, "v0.18.0", "tracker-swebench agent-runner: instance under test"),
	v("SWEBENCH_REPO_DIR", TrustedState, ShellOnly, "v0.18.0", "tracker-swebench agent-runner: checkout to operate on"),
	v("SWEBENCH_MODEL", NetworkDestination, ShellOnly, "v0.18.0", "tracker-swebench agent-runner: model"),
	v("SWEBENCH_PROVIDER", NetworkDestination, ShellOnly, "v0.18.0", "tracker-swebench agent-runner: provider"),
	v("SWEBENCH_MAX_TURNS", Cost, ShellOnly, "v0.18.0", "tracker-swebench agent-runner: turn cap"),
	v("SWEBENCH_TIMEOUT", Cost, ShellOnly, "v0.18.0", "tracker-swebench agent-runner: wall-time cap"),

	// ── Implicit: honored by Go, libc or children, never read by Tracker ──────
	imp("HTTPS_PROXY", NetworkDestination, "Go net/http proxy for every provider call (frozen on first use)"),
	imp("HTTP_PROXY", NetworkDestination, "Go net/http proxy"),
	imp("NO_PROXY", NetworkDestination, "Go net/http proxy bypass list"),
	imp("https_proxy", NetworkDestination, "lower-case form Go also honors"),
	imp("http_proxy", NetworkDestination, "lower-case form Go also honors"),
	imp("no_proxy", NetworkDestination, "lower-case form Go also honors"),
	imp("SSL_CERT_FILE", NetworkDestination, "TLS root bundle Go trusts on Linux/BSD (frozen on first use)"),
	imp("SSL_CERT_DIR", NetworkDestination, "TLS root directory Go trusts on Linux/BSD"),
	imp("LD_PRELOAD", SubprocessTarget, "glibc dynamic-linker hook injected into every child"),
	imp("LD_LIBRARY_PATH", SubprocessTarget, "glibc library search path for every child"),
	imp("DYLD_INSERT_LIBRARIES", SubprocessTarget, "macOS dynamic-linker hook injected into every child"),
	imp("DYLD_LIBRARY_PATH", SubprocessTarget, "macOS library search path for every child"),
	imp("PATH", SubprocessTarget, "exec.LookPath for claude, dippin, git, ACP bridges and every tool command"),
	imp("HOME", TrustedState, "os.UserHomeDir: config .env location and secure-log fallback base"),
	imp("TMPDIR", TrustedState, "os.TempDir: last-resort secure-log base"),
	imp("GIT_SSH_COMMAND", SubprocessTarget, "command git runs for SSH transports"),
	imp("GIT_CONFIG_COUNT", SubprocessTarget, "git in-environment config (with GIT_CONFIG_KEY_n / GIT_CONFIG_VALUE_n)"),
	imp("GOPROXY", NetworkDestination, "module proxy for `go` commands run by tool nodes and agents"),
	imp("NPM_CONFIG_REGISTRY", NetworkDestination, "registry for `npm` commands run by tool nodes and agents"),
	imp("PIP_INDEX_URL", NetworkDestination, "index for `pip` commands run by tool nodes and agents"),
}
