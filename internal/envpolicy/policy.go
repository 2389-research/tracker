// ABOUTME: Registry of every environment variable Tracker reads, with the trust tier each may be set from.
// ABOUTME: The .env loader, doctor and the provider-key tables consult it instead of keeping their own lists (#659).
package envpolicy

import (
	"sort"
	"strings"
)

// Purpose classifies what a variable controls. The invariant tests in this
// package tie each purpose to the most permissive Source it may have.
type Purpose string

// Purpose values.
const (
	// Credential is a provider API key or similar secret Tracker presents to a service.
	Credential Purpose = "credential"
	// SecuritySwitch opts out of a default protection (credential scrub, key strip, exit-code contract).
	SecuritySwitch Purpose = "security_switch"
	// NetworkDestination decides where Tracker (or a child) sends traffic, or which TLS roots it trusts.
	NetworkDestination Purpose = "network_destination"
	// TrustedState locates state Tracker treats as authoritative (secure log, checkpoint, config).
	TrustedState Purpose = "trusted_state"
	// SubprocessTarget decides which binary or library a child process runs.
	SubprocessTarget Purpose = "subprocess_target"
	// Cost tunes spend accounting or budgets.
	Cost Purpose = "cost"
	// UX tunes output, notifications and other non-security behavior.
	UX Purpose = "ux"
	// Output is set BY Tracker for its children; it is never an input.
	Output Purpose = "output"
	// RunIdentity names the run, instance or pane a process reports as.
	RunIdentity Purpose = "run_identity"
)

// Source is the most permissive place a variable may be set from. The order
// is by trust, least to most permissive: a variable with Source ConfigEnv may
// come from the shell or the config file, never from a project .env.
type Source int

// Source values, ordered from least to most permissive.
const (
	// ShellOnly: only the process environment Tracker was started with.
	ShellOnly Source = iota
	// ConfigEnv: the shell or the operator's ~/.config/tracker/.env.
	ConfigEnv
	// ProjectEnv: the shell, the config file, or <workdir>/.env.
	ProjectEnv
)

// String returns the Source name used in messages and docs.
func (s Source) String() string {
	switch s {
	case ShellOnly:
		return "shell-only"
	case ConfigEnv:
		return "config-env"
	case ProjectEnv:
		return "project-env"
	}
	return "unknown"
}

// Describe says where a variable with this Source may be set, for the
// "ignoring X" stderr line and doctor.
func (s Source) Describe() string {
	switch s {
	case ShellOnly:
		return "the shell environment only"
	case ConfigEnv:
		return "the shell or the config .env (~/.config/tracker/.env)"
	case ProjectEnv:
		return "the shell, the config .env or the project .env"
	}
	return "unknown"
}

// Var is one registered environment variable.
type Var struct {
	// Name is the exact variable name.
	Name string
	// Purpose classifies what the variable controls.
	Purpose Purpose
	// Source is the most permissive file that may set it.
	Source Source
	// Doc is a one-line description.
	Doc string
	// Since is the release that introduced the variable ("vX.Y.Z", or
	// "unreleased" for a variable added since the last tag).
	Since string
	// Implicit marks a name Tracker never reads itself but Go, libc or a
	// child process honors (proxies, TLS roots, dynamic-linker hooks, PATH).
	Implicit bool
}

// implicitPrefixes are name families (DYLD_*, GIT_CONFIG_KEY_<n>, LD_*) that
// behave like the registered implicit names. Lookup reports them as
// ShellOnly SubprocessTarget so the loader's message names the right tier.
var implicitPrefixes = []string{"DYLD_", "GIT_CONFIG_", "LD_"}

// Table returns a copy of the registry, sorted by name.
func Table() []Var {
	out := make([]Var, len(table))
	copy(out, table)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns the registered entry for name. Names in an implicit prefix
// family resolve to a synthesized ShellOnly entry.
func Lookup(name string) (Var, bool) {
	for _, v := range table {
		if v.Name == name {
			return v, true
		}
	}
	for _, p := range implicitPrefixes {
		if strings.HasPrefix(name, p) {
			return Var{Name: name, Purpose: SubprocessTarget, Source: ShellOnly, Implicit: true,
				Doc: "dynamic-linker / git configuration family honored by child processes", Since: "v0.1.0"}, true
		}
	}
	return Var{}, false
}

// AllowedFrom reports whether a file of trust tier src may set name. An
// unregistered name is never allowed from a file.
func AllowedFrom(name string, src Source) bool {
	v, ok := Lookup(name)
	if !ok {
		return false
	}
	return v.Source >= src
}

// ProviderKeyVars returns every provider credential name (*_API_KEY), sorted.
// It is the single list the provider-key tables in llm, tracker, cmd/tracker
// and the claude-code / ACP backends must agree with.
func ProviderKeyVars() []string {
	return namesWhere(func(v Var) bool {
		return v.Purpose == Credential && strings.HasSuffix(v.Name, "_API_KEY")
	})
}

// ProviderBaseURLVars returns every per-provider *_BASE_URL override, sorted.
func ProviderBaseURLVars() []string {
	return namesWhere(func(v Var) bool {
		return v.Purpose == NetworkDestination && strings.HasSuffix(v.Name, "_BASE_URL") && !v.Implicit
	})
}

func namesWhere(keep func(Var) bool) []string {
	var out []string
	for _, v := range table {
		if keep(v) {
			out = append(out, v.Name)
		}
	}
	sort.Strings(out)
	return out
}
