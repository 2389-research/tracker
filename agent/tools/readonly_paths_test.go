// ABOUTME: Read-only path guard: symlinks out of the workdir and secret-looking files
// ABOUTME: are refused by read, grep_search and glob under tool_access: read_only.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent/exec"
)

func readOnlyRepo(t *testing.T) (root, outside string) {
	t.Helper()
	root = t.TempDir()
	outside = t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main // needle\n")
	write(".env", "SECRET=needle\n")
	write(".env.example", "SECRET=placeholder needle\n")
	write("sub/.env.local", "X=needle\n")
	write("id_rsa", "needle\n")
	write("store.p12", "needle\n")
	write("key.pem", "-----BEGIN RSA PRIVATE KEY-----\nneedle\n-----END RSA PRIVATE KEY-----\n")
	write("cert.pem", "-----BEGIN CERTIFICATE-----\nneedle\n-----END CERTIFICATE-----\n")
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	return root, outside
}

func TestCheckReadOnlyPath(t *testing.T) {
	root, _ := readOnlyRepo(t)
	allowed := []string{"main.go", ".env.example", "cert.pem"}
	refused := []string{".env", "sub/.env.local", "id_rsa", "store.p12", "key.pem", "link.txt", "../outside.txt"}
	for _, p := range allowed {
		if err := CheckReadOnlyPath(root, p); err != nil {
			t.Errorf("CheckReadOnlyPath(%q) = %v, want allowed", p, err)
		}
	}
	for _, p := range refused {
		if err := CheckReadOnlyPath(root, p); err == nil {
			t.Errorf("CheckReadOnlyPath(%q) = nil, want refused", p)
		}
	}
}

func readOnlyTool(t *testing.T, root, name string) Tool {
	t.Helper()
	for _, tl := range NewReadOnlyTools(exec.NewLocalEnvironment(root)) {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("NewReadOnlyTools has no %q", name)
	return nil
}

func TestReadOnlyTools_ExactSet(t *testing.T) {
	var names []string
	for _, tl := range NewReadOnlyTools(exec.NewLocalEnvironment(t.TempDir())) {
		names = append(names, tl.Name())
	}
	if strings.Join(names, ",") != "read,grep_search,glob" {
		t.Fatalf("NewReadOnlyTools = %v, want read,grep_search,glob", names)
	}
}

func TestReadOnlyTools_ReadRefusesSecretAndSymlink(t *testing.T) {
	root, _ := readOnlyRepo(t)
	read := readOnlyTool(t, root, "read")
	for _, p := range []string{".env", "link.txt"} {
		args, _ := json.Marshal(map[string]string{"path": p})
		if _, err := read.Execute(context.Background(), args); err == nil {
			t.Errorf("read %s: want a refusal error", p)
		}
	}
	args, _ := json.Marshal(map[string]string{"path": "main.go"})
	if out, err := read.Execute(context.Background(), args); err != nil || !strings.Contains(out, "needle") {
		t.Fatalf("read main.go = %q, %v", out, err)
	}
}

func TestReadOnlyTools_GrepAndGlobSkipRefused(t *testing.T) {
	root, _ := readOnlyRepo(t)
	args, _ := json.Marshal(map[string]any{"pattern": "needle", "path": "."})
	out, err := readOnlyTool(t, root, "grep_search").Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go") || !strings.Contains(out, ".env.example") {
		t.Fatalf("grep should still find allowed files: %s", out)
	}
	for _, bad := range []string{"SECRET=needle", "id_rsa", "store.p12", "key.pem", "link.txt", ".env.local"} {
		if strings.Contains(out, bad) {
			t.Errorf("grep leaked refused file content %q: %s", bad, out)
		}
	}
	gargs, _ := json.Marshal(map[string]string{"pattern": "**/*"})
	gout, err := readOnlyTool(t, root, "glob").Execute(context.Background(), gargs)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"link.txt", "id_rsa", "key.pem"} {
		if strings.Contains(gout, bad) {
			t.Errorf("glob listed refused path %q: %s", bad, gout)
		}
	}
}
