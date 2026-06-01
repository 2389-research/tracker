package exec

import (
	"errors"
	"fmt"
	"testing"
)

func TestJailErrorsSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"ErrLandlockUnavailable", ErrLandlockUnavailable},
		{"ErrPathEscape", ErrPathEscape},
		{"ErrPathNotAllowed", ErrPathNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("sentinel is nil")
			}
			if tc.err.Error() == "" {
				t.Fatal("sentinel has empty message")
			}
			wrapped := fmt.Errorf("context: %w", tc.err)
			if !errors.Is(wrapped, tc.err) {
				t.Errorf("errors.Is failed for wrapped %s", tc.name)
			}
		})
	}
}
