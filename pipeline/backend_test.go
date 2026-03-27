// ABOUTME: Tests for AgentBackend config types, MCP server parsing, and tool list validation.
// ABOUTME: Covers PermissionMode validation, ParseMCPServers edge cases, and tool list parsing.
package pipeline

import "testing"

func TestPermissionModeValidation(t *testing.T) {
	valid := []PermissionMode{PermissionPlan, PermissionAutoEdit, PermissionFullAuto}
	for _, m := range valid {
		if !m.Valid() {
			t.Errorf("expected %q to be valid", m)
		}
	}
	if PermissionMode("bogus").Valid() {
		t.Error("expected 'bogus' to be invalid")
	}
	// Empty string is not valid — callers must explicitly set a mode
	// (buildClaudeCodeConfig defaults to PermissionFullAuto).
	if PermissionMode("").Valid() {
		t.Error("expected empty string to be invalid")
	}
}

func TestParseMCPServers(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"single", "pg=npx server", 1, false},
		{"multi", "pg=npx server\ngh=npx github", 2, false},
		{"empty lines", "\n  pg=npx server  \n\n", 1, false},
		{"no equals", "broken", 0, true},
		{"empty name", "=command", 0, true},
		{"empty command", "name=", 0, true},
		{"equals in command", "pg=npx server --conn=host=localhost", 1, false},
		{"duplicate names", "pg=a\npg=b", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			servers, err := ParseMCPServers(tt.input)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(servers) != tt.want {
				t.Errorf("got %d servers, want %d", len(servers), tt.want)
			}
		})
	}
}

func TestParseMCPServersCommandArgs(t *testing.T) {
	servers, err := ParseMCPServers("pg=npx @mcp/server-postgres connstr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	s := servers[0]
	if s.Name != "pg" {
		t.Errorf("name = %q, want %q", s.Name, "pg")
	}
	if s.Command != "npx" {
		t.Errorf("command = %q, want %q", s.Command, "npx")
	}
	if len(s.Args) != 2 || s.Args[0] != "@mcp/server-postgres" || s.Args[1] != "connstr" {
		t.Errorf("args = %v, want [\"@mcp/server-postgres\", \"connstr\"]", s.Args)
	}
}

func TestParseToolList(t *testing.T) {
	tools := ParseToolList("Read,Write,Bash")
	if len(tools) != 3 || tools[0] != "Read" {
		t.Errorf("got %v", tools)
	}
	if len(ParseToolList("")) != 0 {
		t.Error("empty string should return empty list")
	}
	if len(ParseToolList("  ")) != 0 {
		t.Error("whitespace-only should return empty list")
	}
	// Handles whitespace around items
	tools = ParseToolList(" Read , Write ")
	if len(tools) != 2 || tools[0] != "Read" || tools[1] != "Write" {
		t.Errorf("got %v, want [Read Write]", tools)
	}
}

func TestValidateToolLists(t *testing.T) {
	if err := ValidateToolLists([]string{"Read"}, []string{"Bash"}); err == nil {
		t.Error("expected error when both allowed and disallowed are set")
	}
	if err := ValidateToolLists([]string{"Read"}, nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := ValidateToolLists(nil, []string{"Bash"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := ValidateToolLists(nil, nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
