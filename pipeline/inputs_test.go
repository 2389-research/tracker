// ABOUTME: Tests for declared-input value validation and the closed, untrusted
// ABOUTME: inputs.* expansion namespace (#553).
package pipeline

import "testing"

func TestValidateInputValues_Table(t *testing.T) {
	specs := []InputSpec{
		{Name: "idea", Kind: InputText, Required: true, MaxLength: 10},
		{Name: "branch", Kind: InputText, HasDefault: true, Default: "main", Pattern: "^[a-z]+$"},
		{Name: "count", Kind: InputNumber, Min: "1", Max: "5"},
		{Name: "dry", Kind: InputBool},
		{Name: "risk", Kind: InputEnum, Options: []string{"low", "high"}},
	}

	tests := []struct {
		name    string
		values  map[string]string
		wantErr InputErrorKind // "" means expect success
		errName string
		seedK   string // a seed key to assert present
		seedV   string
	}{
		{name: "all valid", values: map[string]string{"idea": "ship it", "count": "3", "dry": "TRUE", "risk": "high"}, seedK: "dry", seedV: "true"},
		{name: "missing required", values: map[string]string{}, wantErr: ErrMissingRequired, errName: "idea"},
		{name: "required supplied empty", values: map[string]string{"idea": "   "}, wantErr: ErrMissingRequired, errName: "idea"},
		{name: "default applied for omitted optional", values: map[string]string{"idea": "x"}, seedK: "branch", seedV: "main"},
		{name: "text too long", values: map[string]string{"idea": "way too many characters"}, wantErr: ErrLength, errName: "idea"},
		{name: "pattern mismatch", values: map[string]string{"idea": "x", "branch": "Main9"}, wantErr: ErrPattern, errName: "branch"},
		{name: "number not a number", values: map[string]string{"idea": "x", "count": "abc"}, wantErr: ErrTypeMismatch, errName: "count"},
		{name: "number out of range", values: map[string]string{"idea": "x", "count": "9"}, wantErr: ErrRange, errName: "count"},
		{name: "bad bool", values: map[string]string{"idea": "x", "dry": "yes"}, wantErr: ErrTypeMismatch, errName: "dry"},
		{name: "enum not an option", values: map[string]string{"idea": "x", "risk": "medium"}, wantErr: ErrEnum, errName: "risk"},
		{name: "unknown supplied key", values: map[string]string{"idea": "x", "bogus": "1"}, wantErr: ErrUnknownInput, errName: "bogus"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed, errs := ValidateInputValues(specs, tt.values)
			if tt.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("want no errors, got %v", errs)
				}
			} else {
				if !hasInputErr(errs, tt.errName, tt.wantErr) {
					t.Fatalf("want %s on %q, got %v", tt.wantErr, tt.errName, errs)
				}
			}
			if tt.seedK != "" && seed[tt.seedK] != tt.seedV {
				t.Fatalf("seed[%q] = %q, want %q", tt.seedK, seed[tt.seedK], tt.seedV)
			}
		})
	}
}

func TestValidateInputValues_UnknownKindErrorsOnValue(t *testing.T) {
	specs := []InputSpec{{Name: "x", Kind: InputKind("duration"), Required: true}}
	_, errs := ValidateInputValues(specs, map[string]string{"x": "5m"})
	if !hasInputErr(errs, "x", ErrUnknownKind) {
		t.Fatalf("want unknown_kind, got %v", errs)
	}
}

// TestValidateInputValues_SecretValidatesAsText: a secret value passes
// validation (text rules); it is the tracker binding layer that stages it to a
// 0600 file and exposes only the path (#555), so validation itself accepts it.
func TestValidateInputValues_SecretValidatesAsText(t *testing.T) {
	specs := []InputSpec{{Name: "token", Kind: InputSecret, Required: true, MaxLength: 4}}
	if _, errs := ValidateInputValues(specs, map[string]string{"token": "s3cr3t"}); !hasInputErr(errs, "token", ErrLength) {
		t.Fatalf("secret should validate as text (max_length enforced), got %v", errs)
	}
	if _, errs := ValidateInputValues(specs, map[string]string{"token": "ok"}); len(errs) != 0 {
		t.Fatalf("valid secret should pass, got %v", errs)
	}
}

func TestValidateInputValues_NoSpecsIsNoop(t *testing.T) {
	_, errs := ValidateInputValues(nil, nil)
	if len(errs) != 0 {
		t.Fatalf("want no errors for no declared inputs, got %v", errs)
	}
}

func hasInputErr(errs []InputError, name string, kind InputErrorKind) bool {
	for _, e := range errs {
		if e.Name == name && e.Kind == kind {
			return true
		}
	}
	return false
}

func TestInputsNamespace_ResolvesAndIsUntrusted(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set(inputContextPrefix+"idea", "ship the thing")
	ctx.Set(inputContextPrefix+"count", "42")

	// Renders in a normal (prompt) context, typed value unquoted.
	got, err := ExpandVariables("Build ${inputs.idea} x${inputs.count}", ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if want := "Build ship the thing x42"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// Blocked wholesale in tool_command mode — the whole namespace is untrusted.
	if _, err := ExpandVariables("echo ${inputs.idea}", ctx, nil, nil, false, true); err == nil {
		t.Fatal("expected inputs.* to be rejected in tool_command mode")
	}
}
