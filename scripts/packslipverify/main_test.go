package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScenarioStopsAfterFirstFailure(t *testing.T) {
	root := t.TempDir()
	s := &scenario{ctx: context.Background(), env: map[string]string{"HOME": root}}
	s.require(false, "first failure")
	s.write(filepath.Join(root, "file"), "unexpected", 0o600)
	s.run(root, "/bin/sh", "-c", "touch child-write")
	s.remove(filepath.Join(root, "missing"))
	s.require(false, "second failure")
	if s.err == nil || s.err.Error() != "first failure" {
		t.Fatalf("lost first failure: %v", s.err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("operations continued: %v %v", entries, err)
	}
}

func TestExpectedFailureCannotHideLaunchFailure(t *testing.T) {
	root := t.TempDir()
	s := &scenario{ctx: context.Background(), env: map[string]string{"HOME": root}}
	s.command(root, "", nil, true, filepath.Join(root, "missing"))
	if s.err == nil {
		t.Fatal("expected nonzero exit accepted failed launch")
	}
}

func TestSmokeEnvironmentAndHomeGuard(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("GH_TOKEN", "fixture-secret")
	t.Setenv("MISE_GITHUB_TOKEN", "fixture-secret")
	t.Setenv("KAE_RELEASE_VERIFY_FRESH", "1")
	env := smokeEnvironment()
	for _, key := range []string{"GH_TOKEN", "MISE_GITHUB_TOKEN", "KAE_RELEASE_VERIFY_FRESH"} {
		if _, ok := env[key]; ok {
			t.Fatalf("inherited %s", key)
		}
	}
	if _, err := newScenario(context.Background()); err == nil || !strings.Contains(err.Error(), "canonical smoke HOME") {
		t.Fatalf("unsafe HOME accepted: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("guard mutated HOME: %v %v", entries, err)
	}
}

func TestFixtureServerRefusesUnknownAndAuthorizedRequests(t *testing.T) {
	server := &fixtureServer{releases: map[string]map[string]any{}, files: map[string][]byte{"/github.com/fixture": []byte("fixture")}}
	for _, test := range []struct {
		path, auth string
		status     int
	}{{"/github.com/fixture", "", 200}, {"/unknown", "", 404}, {"/github.com/fixture", "fixture-secret", 400}} {
		req := httptest.NewRequest(http.MethodGet, "http://fixture"+test.path, nil)
		if test.auth != "" {
			req.Header.Set("Authorization", test.auth)
		}
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, req)
		if recorder.Code != test.status {
			t.Fatalf("%s: %d", test.path, recorder.Code)
		}
	}
	if server.requests.Load() != 3 {
		t.Fatal("request count mismatch")
	}
}

func TestShellMarkersFailClosed(t *testing.T) {
	s := &scenario{}
	s.section("missing marker", "FIRST\n", "SECOND\n")
	if s.err == nil {
		t.Fatal("missing result marker accepted")
	}
	first := s.err
	s.section("", "OTHER", "")
	if !errors.Is(s.err, first) {
		t.Fatal("lost first failure")
	}
}
