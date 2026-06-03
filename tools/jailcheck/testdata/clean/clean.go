// ABOUTME: jailcheck fixture — a clean tool that routes all mutations through env.
// ABOUTME: Must produce zero violations.
package clean

import (
	"context"
	"os"
)

type env interface {
	WriteFile(ctx context.Context, path, content string) error
	RemoveFile(ctx context.Context, path string) error
}

// writeViaEnv routes through the ExecutionEnvironment seam — no os.* mutation.
func writeViaEnv(ctx context.Context, e env, path, content string) error {
	return e.WriteFile(ctx, path, content)
}

// readOnly uses read-only os.* funcs, which are not jail bypasses.
func readOnly(path string) ([]byte, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// guardedFallback mutates os.* only on the env==nil path and is annotated, so
// it is allowed.
//
//jail:allow-unjailed-fallback env==nil path can never coexist with an active jail
func guardedFallback(ctx context.Context, e env, path, content string) error {
	if e != nil {
		return e.WriteFile(ctx, path, content)
	}
	if err := os.MkdirAll("dir", 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
