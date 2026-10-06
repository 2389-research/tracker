# Environment trust policy (#659)

Tracker has one channel for runtime knobs: the process environment. Around
forty `os.Getenv` sites read it, and Go, libc and every child process honor
more names Tracker never reads (`HTTPS_PROXY`, `SSL_CERT_FILE`, `LD_PRELOAD`,
`PATH`). Until v0.78.0 the `.env` loader flattened three populations with
different writers into that one namespace, and every security control added
later assumed `env == operator intent`. This document is the model that
replaced it.

## The three sources

| Source | Who can write it | What it may set |
|---|---|---|
| **Shell** (the environment `tracker` is started with) | The operator | Anything. A shell value is never overwritten by a file. |
| **Config `.env`** — `~/.config/tracker/.env` (`$XDG_CONFIG_HOME/tracker/.env` when that is absolute; written by `tracker setup`) | The operator, or `tracker setup` on their behalf | `ProjectEnv` ∪ `ConfigEnv` names: provider keys, per-provider `*_BASE_URL`, `TRACKER_GATEWAY_URL` / `TRACKER_GATEWAY_KIND`, the cost and UX knobs. |
| **Project `.env`** — `<workdir>/.env` | Any committer, a prior unjailed builder agent, any model-run command in that tree | `ProjectEnv` names only: the provider `*_API_KEY` credentials. |

The loader is `cmd/tracker/envfiles.go`. It reads the config file first, then
the project file, with a presence snapshot of the shell taken before either
(so "shell wins" is by presence, including `NAME=`). For a name both files
set and the tier allows, the project file wins — that is the one legitimate
per-project override: which key pays for the run.

Everything a file is *not* allowed to set is skipped for that key with one
stderr line naming the file, the key and where the key belongs:

```
tracker: /repo/.env: ignoring TRACKER_PASS_ENV — security switch: allowed from the shell environment only, not from this project-env file
tracker: /repo/.env: ignoring OPENAI_BASE_URL — network destination: allowed from the shell or the config .env (~/.config/tracker/.env), not from this project-env file
tracker: /repo/.env: ignoring FOO — not a variable tracker reads; a .env file may only supply registered names (export it in the shell if a subprocess needs it)
```

File hygiene, applied before parsing: a symlinked `.env` is skipped whole (the
open also uses `O_NOFOLLOW` on Unix so the `Lstat` check cannot be raced), and
a group- or world-writable **project** `.env` is skipped whole. Both are
notices, never hard errors; a file that exists but cannot be read or parsed
is still a hard error, as before.

Which files load at all is an explicit switch: `--env-files=all|config|none`
on `tracker run` and `tracker doctor` (default `all`), or the shell-only
`TRACKER_ENV_FILES`. `tracker version` loads the config file only — it is the
command people type casually inside an untrusted checkout. The library API
(`tracker.Run`, `NewEngineFromGraph`, `Doctor`) never loads `.env` files;
embedders own their process environment.

## The registry — `internal/envpolicy`

Every environment variable Tracker reads is a row in `internal/envpolicy/table.go`:

| Field | Meaning |
|---|---|
| `Name` | Exact variable name. |
| `Purpose` | `Credential`, `SecuritySwitch`, `NetworkDestination`, `TrustedState`, `SubprocessTarget`, `Cost`, `UX`, `Output`, `RunIdentity`. |
| `Source` | The **most permissive** file that may set it: `ShellOnly` < `ConfigEnv` < `ProjectEnv`. A file of tier *t* may set a name iff `Source ≥ t`. |
| `Doc`, `Since` | One line, and the release that introduced it. |
| `Implicit` | Names Tracker never reads but Go/libc/children honor (`HTTPS_PROXY`, `SSL_CERT_*`, `LD_*`, `DYLD_*`, `PATH`, `HOME`, `GIT_SSH_COMMAND`, `GIT_CONFIG_*`, `GOPROXY`, `NPM_CONFIG_REGISTRY`, `PIP_INDEX_URL`). Always `ShellOnly`. |

The invariants are tests in the package, so the table cannot drift quietly:

- `TestSecurityPurposesAreShellOnly` — `SecuritySwitch`, `TrustedState`,
  `SubprocessTarget`, `Output`, `RunIdentity` ⇒ `ShellOnly`;
  `NetworkDestination`, `Cost`, `UX` ⇒ at most `ConfigEnv`; `ProjectEnv` ⇒
  `Credential`. The purpose decides the tier; a row cannot be argued into a
  more permissive one.
- `TestEveryGetenvLiteralIsRegistered` — walks every non-test Go file in the
  repo and asserts each `os.Getenv` / `os.LookupEnv` literal (or
  package-const) argument is registered. Adding a `Getenv` without a row
  fails the build's tests. The exclusion list (`GEN_*_OUT`, dev-time
  generators) is justified inline. This is the interim guard until the
  `tools/envcheck` AST gate lands.
- `TestProviderKeyVarsSupersetOfLegacyTables` plus live cross-checks in `llm`
  and `pipeline/handlers` — `envpolicy.ProviderKeyVars()` /
  `ProviderBaseURLVars()` are supersets of every per-package provider list
  (the client's key table, the setup wizard's write allowlist, the
  claude-code and ACP strip lists), so a key one list knows cannot be
  unknown to the loader.

`cmd/tracker`'s black-box tests pin the loader to the table:
`TestProjectEnvCannotSetAnyShellOnlyVar` writes every `ShellOnly` row (and
the implicit families) into a project `.env` and asserts none lands;
`TestConfigEnvCannotFlipSecuritySwitches`,
`TestProjectEnvOverridesConfigOnlyForCredentials`,
`TestProjectEnvSymlinkIsRefused`, `TestProjectEnvWorldWritableIsSkipped`,
`TestVersionDoesNotLoadProjectEnv`, and
`TestCommandEnvIgnoresPassEnvFromProjectFile` (a project `.env` with
`TRACKER_PASS_ENV=1` → `exec.CommandEnv()` still strips `ANTHROPIC_API_KEY`).

## Provenance and doctor

The loader records every decision (`envpolicy.RecordApplied` /
`RecordSkipped`). `tracker doctor`'s *Environment Warnings* check reports:

- dangerous switches that are on, with origin — `TRACKER_PASS_ENV=1 (from
  shell)` (warning);
- routing / trusted-state / herdr knobs that are set, with origin —
  `TRACKER_GATEWAY_URL=https://gw (from /home/op/.config/tracker/.env)`
  (hint — intentional configuration is not a warning);
- every shell-only name a `.env` file tried to set — `TRACKER_AUDIT_DIR (from
  /repo/.env — ignored; …)` (warning: that file is misconfigured or hostile).

The *Working Directory* check warns when `<workdir>/.env` is tracked by git
(`git ls-files --error-unmatch .env`): a committed `.env` is readable by every
clone and `tracker run` loads it. Warn, never refuse.

## Residual risks

- **An unjailed builder runs as the operator.** Without `writable_paths` an
  agent can edit `~/.zshrc`, `~/.config/tracker/.env`, or the operator's
  shell startup files directly; the next shell then *is* the operator's
  intent as far as this policy can tell. Only the `writable_paths` jail
  bounds that. This policy removes the *project-checkout* vector, which is
  the one a clone, a PR or a prior run can reach without touching the home
  directory.
- **Proxy / TLS freezing.** Go freezes proxy configuration and system roots on
  first use. Because `tracker run` on a release build may start the update
  check before `.env` loading, an `HTTPS_PROXY` in a project `.env` was racy
  rather than deterministic even before this change; it is now simply never
  applied from a file.
- **Shell-wins is by presence.** `NAME=` in the shell pins a name to empty
  and blocks every file from setting it, silently. That is deliberate (the
  operator said so) but worth knowing when a key "mysteriously" does not
  load.
- **Ownership is not checked.** Only mode bits are (group/world-writable).
  A uid check misfires on NFS and uid-mapped homes, so it was left out.
- **`Lookup` of implicit families is prefix-based** (`DYLD_*`, `GIT_CONFIG_*`,
  `LD_*`) so the refusal message names the right tier; the allowlist itself
  needs no prefixes because every unregistered name is refused anyway.

## Adding a variable

1. Add the `os.Getenv` site.
2. Add a row in `internal/envpolicy/table.go` with the purpose, the *least*
   permissive source that still serves the use case, a one-line doc and the
   release. The invariant tests tell you if the purpose and source disagree.
3. If it is a provider key or base URL, nothing else: the setup wizard,
   loader and doctor pick it up from the registry.
4. Document it on the website (`site/content/cli.html` env table).

Ask the review question: *who can set this?* If the honest answer includes
"a file in the checkout", the row must be `ShellOnly` or `ConfigEnv`.
