# Sprint 002 — Hello World End-to-End Proof (enriched spec)

## Scope
Wire the CLI entrypoint so that `./agent version` prints a version string and `./agent doctor`
prints Go version and working directory. One integration test builds the binary and asserts
both commands produce correct output.

## Non-goals
- No real doctor logic, no TUI, no cobra, no flag parsing library
- No config file, no DB connection in doctor
- No additional subcommands

## Dependencies
- Sprint 001: `internal/store` package exists; module name is `agent`

## Go/runtime conventions (inherited from Sprint 001)
- Module: `agent`
- All error wrapping: `fmt.Errorf("...: %w", err)`
- All temp dirs in tests: `t.TempDir()`
- All helpers call `t.Helper()`
- Go version: 1.25+; no third-party CLI framework in this sprint

## File structure

```
cmd/agent/main.go          — package main; os.Args dispatch, calls runVersion/runDoctor
cmd/agent/cmd_version.go   — package main; const version; runVersion()
cmd/agent/cmd_doctor.go    — package main; runDoctor() using runtime + os
tests/integration/smoke_test.go — package integration; TestMain builds binary; TestSmoke runs it
```

## Interface contract

```go
// cmd/agent/cmd_version.go
const version = "0.1.0-dev"
func runVersion()   // prints version to stdout, no newline trimming needed

// cmd/agent/cmd_doctor.go
func runDoctor()    // prints two lines to stdout: "go: <runtime.Version()>" and "dir: <wd>"

// cmd/agent/main.go
func main()         // dispatches os.Args[1] to runVersion/runDoctor; unknown → stderr + exit 1
```

## Imports per file

**`cmd/agent/main.go`**
```go
import (
    "fmt"
    "os"
)
```

**`cmd/agent/cmd_version.go`**
```go
import "fmt"
```

**`cmd/agent/cmd_doctor.go`**
```go
import (
    "fmt"
    "os"
    "runtime"
)
```

**`tests/integration/smoke_test.go`**
```go
import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
)
```

## Algorithm notes

### `main()`
Copy this structure exactly:

```go
func main() {
    if len(os.Args) < 2 {
        fmt.Fprintln(os.Stderr, "usage: agent <command>")
        os.Exit(1)
    }
    switch os.Args[1] {
    case "version":
        runVersion()
    case "doctor":
        runDoctor()
    default:
        fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
        os.Exit(1)
    }
}
```

### `runVersion()`
```go
func runVersion() {
    fmt.Println(version)
}
```

### `runDoctor()`
```go
func runDoctor() {
    wd, err := os.Getwd()
    if err != nil {
        wd = "(unknown)"
    }
    fmt.Printf("go:  %s\n", runtime.Version())
    fmt.Printf("dir: %s\n", wd)
}
```

### `TestMain` — builds binary once for all tests
```go
var agentBin string

func TestMain(m *testing.M) {
    tmp, err := os.MkdirTemp("", "agent-smoke-*")
    if err != nil {
        fmt.Fprintln(os.Stderr, "mkdirtemp:", err)
        os.Exit(1)
    }
    defer os.RemoveAll(tmp)

    agentBin = filepath.Join(tmp, "agent")
    cmd := exec.Command("go", "build", "-o", agentBin, "agent/cmd/agent")
    if out, err := cmd.CombinedOutput(); err != nil {
        fmt.Fprintf(os.Stderr, "go build failed: %v\n%s\n", err, out)
        os.Exit(1)
    }

    os.Exit(m.Run())
}
```

### `TestSmoke` — copy exactly
```go
func TestSmoke(t *testing.T) {
    t.Run("version exits 0 and prints non-empty output", func(t *testing.T) {
        out, err := exec.Command(agentBin, "version").Output()
        if err != nil {
            t.Fatalf("agent version: %v", err)
        }
        if strings.TrimSpace(string(out)) == "" {
            t.Fatal("expected non-empty version output")
        }
    })

    t.Run("doctor exits 0 and output contains go version", func(t *testing.T) {
        out, err := exec.Command(agentBin, "doctor").Output()
        if err != nil {
            t.Fatalf("agent doctor: %v", err)
        }
        if !strings.Contains(string(out), "go:") {
            t.Fatalf("expected 'go:' in doctor output, got: %s", out)
        }
    })
}
```

## Test plan

```go
func TestSmoke(t *testing.T)
```

Subtests:
```
TestSmoke/version_exits_0_and_prints_non-empty_output
TestSmoke/doctor_exits_0_and_output_contains_go_version
```

## Rules
- `tests/integration/smoke_test.go` must NOT have a `//go:build` tag — it must run with plain `go test`
- `cmd/agent/` files are all `package main` — no exported symbols
- `version` constant is the single source of truth — no duplicate declarations
- `runDoctor()` never calls `os.Exit` — only `main()` exits
- `TestMain` builds the binary once; subtests reuse `agentBin`
- Use module-path form `agent/cmd/agent` in the `go build` command, not a relative path
- Do not add a `go.sum` entry — `modernc.org/sqlite` from Sprint 001 already handles that

## New files
- `cmd/agent/main.go` — package main; os.Args dispatch, calls runVersion/runDoctor
- `cmd/agent/cmd_version.go` — package main; const version; runVersion()
- `cmd/agent/cmd_doctor.go` — package main; runDoctor() using runtime + os
- `tests/integration/smoke_test.go` — package integration; TestMain builds binary; TestSmoke runs it

## Modified files
(none — this sprint introduces new files only. Subsequent sprints that touch existing files would list them here as `- `path/to/existing/file` — what changes`)

## Expected Artifacts
- `cmd/agent/main.go`
- `cmd/agent/cmd_version.go`
- `cmd/agent/cmd_doctor.go`
- `tests/integration/smoke_test.go`

## DoD
- [ ] `go build -o agent ./cmd/agent/` succeeds
- [ ] `./agent version` exits 0 and prints a non-empty version string
- [ ] `./agent doctor` exits 0 and prints a line starting with `go:`
- [ ] `go test ./tests/integration/ -run TestSmoke -v` passes

## Validation
```bash
go build -o agent ./cmd/agent/
./agent version
./agent doctor
go test ./tests/integration/ -run TestSmoke -v
```
