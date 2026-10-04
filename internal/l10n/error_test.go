package l10n

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestErrorfKeepsFmtErrorfTextAndCauses(t *testing.T) {
	first, second := errors.New("first"), os.ErrPermission
	cases := []struct {
		format string
		args   []any
		causes []error
	}{
		{"plain %s", []any{"x"}, nil},
		{"one: %w", []any{first}, []error{first}},
		{"two: %w and %w", []any{first, second}, []error{first, second}},
	}
	for _, tc := range cases {
		e := Errorf(tc.format, tc.args...)
		if want := fmt.Errorf(tc.format, tc.args...).Error(); e.Error() != want {
			t.Errorf("Errorf(%q).Error() = %q, want fmt.Errorf's %q", tc.format, e.Error(), want)
		}
		if got := e.Unwrap(); len(got) != len(tc.causes) {
			t.Errorf("Errorf(%q).Unwrap() = %v, want %v", tc.format, got, tc.causes)
		}
		for _, cause := range tc.causes {
			if !errors.Is(e, cause) {
				t.Errorf("Errorf(%q): errors.Is misses %v", tc.format, cause)
			}
		}
		if format, args := e.MessageFormat(); format != tc.format || len(args) != len(tc.args) {
			t.Errorf("Errorf(%q).MessageFormat() = %q, %v", tc.format, format, args)
		}
	}
}

func TestErrorfSentinelMatchesByIdentityAndRendersLocalized(t *testing.T) {
	sentinel := Errorf("thing busy")
	same := Errorf("thing busy")
	wrapped := Errorf("%w: retry %s", sentinel, "later")
	outer := fmt.Errorf("op: %w", wrapped)

	if !errors.Is(outer, sentinel) {
		t.Fatal("errors.Is must find the sentinel through Errorf and fmt.Errorf")
	}
	if errors.Is(outer, same) {
		t.Fatal("a sentinel matches by identity, not by its text")
	}
	if got := Render(wrapped); got != "thing busy: retry later" {
		t.Fatalf("English render: %q", got)
	}
	withJapanese(t, map[string]string{
		"thing busy":   "使用中です",
		"%w: retry %s": "%w: %s に再試行してください",
	})
	if got := Render(wrapped); got != "使用中です: later に再試行してください" {
		t.Fatalf("Japanese render must localize the wrapped sentinel: %q", got)
	}
	if got := wrapped.Error(); got != "thing busy: retry later" {
		t.Fatalf("Error() must stay English: %q", got)
	}
	if !errors.Is(wrapped, sentinel) {
		t.Fatal("errors.Is must not depend on the language")
	}
	var nilError *Error
	if got := Render(nilError); got != "<nil>" {
		t.Fatalf("a nil *Error renders %q, want <nil>", got)
	}
}
