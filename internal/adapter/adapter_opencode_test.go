// Opencode adapter tests: artifacts, detection and auth.json handling.

package adapter_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter/opencode"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestOpencodeArtifactsAndXDGDataHome(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Kind != constants.KindJSONPointer ||
		specs[0].Target != filepath.Join(env.Home, ".local", "share", "opencode", "auth.json") ||
		specs[0].Pointer != "/openai" {
		t.Fatalf("unexpected auth spec: %+v", specs[0])
	}

	dataHome := t.TempDir()
	env = testEnv(t, "darwin", map[string]string{"XDG_DATA_HOME": dataHome})
	specs, err = opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil || specs[0].Target != filepath.Join(dataHome, "opencode", "auth.json") {
		t.Fatalf("XDG_DATA_HOME not honored: %+v %v", specs, err)
	}

	// A relative value is ignored per the XDG spec (paths.XDGDataHome).
	env = testEnv(t, "darwin", map[string]string{"XDG_DATA_HOME": "relative/data"})
	specs, err = opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil || specs[0].Target != filepath.Join(env.Home, ".local", "share", "opencode", "auth.json") {
		t.Fatalf("relative XDG_DATA_HOME must fall back to the default: %+v %v", specs, err)
	}
}

func TestOpencodeDetect(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	info, err := opencodeAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("expected no auth without auth.json: %+v %v", info, err)
	}
	if len(info.Warnings) != 0 {
		t.Fatalf("missing auth.json must not warn: %+v", info.Warnings)
	}

	authPath := filepath.Join(env.Home, ".local", "share", "opencode", "auth.json")

	// API-key-only auth.json: no subscription login, explanatory warning.
	write(t, authPath, `{"openrouter":{"type":"api","key":"sk-x"}}`)
	info, err = opencodeAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("expected no auth without an openai entry: %+v %v", info, err)
	}
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0].Error(), "openai") {
		t.Fatalf("expected missing-openai warning: %+v", info.Warnings)
	}

	write(t, authPath, `{"openai":{"type":"oauth","refresh":"r","access":"a"},"openrouter":{"type":"api","key":"sk-x"}}`)
	info, err = opencodeAdapter.Detect(context.Background(), env)
	if err != nil || !info.AuthPresent || info.Driver != constants.DriverOpencodeFilePatch {
		t.Fatalf("unexpected: %+v %v", info, err)
	}
}

// Two ways the switched auth.json stops being what opencode reads, both visible
// offline: OPENCODE_AUTH_CONTENT carries the whole body inline and is consulted
// before the file, and a relative XDG_DATA_HOME (which opencode uses verbatim,
// against its own working directory, while kae ignores it per the XDG spec) puts
// the two on different files. Neither can be fixed by writing somewhere else, so
// both warn on Detect and in doctor.
func TestOpencodeWarnsWhenAuthJSONIsNotWhatItReads(t *testing.T) {
	wantEnvConflictWarning(t, opencodeAdapter,
		map[string]string{opencode.EnvAuthContent: `{"openai":{}}`}, opencode.EnvAuthContent)
	wantEnvConflictWarning(t, opencodeAdapter,
		map[string]string{"XDG_DATA_HOME": "relative/data"}, "XDG_DATA_HOME is relative")

	// An absolute value is the normal case and must stay silent.
	env := testEnv(t, "darwin", map[string]string{"XDG_DATA_HOME": t.TempDir()})
	info, err := opencodeAdapter.Detect(context.Background(), env)
	if err != nil || len(info.Warnings) != 0 {
		t.Fatalf("an absolute XDG_DATA_HOME must not warn: %+v %v", info.Warnings, err)
	}
}

func TestOpencodeRefusesUnrecognizedAuthJSON(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	authPath := specs[0].Target

	// Malformed auth.json: reading refuses instead of misparsing.
	write(t, authPath, `not json`)
	if _, err := artifact.ReadLive(context.Background(), specs[0]); !errors.Is(err, artifact.ErrUnsafe) {
		t.Fatalf("expected structure-guard refusal: %v", err)
	}
	checks := opencodeAdapter.Doctor(context.Background(), env)
	foundError := false
	for _, check := range checks {
		if check.Code == constants.CheckAuthPresent && check.Status == constants.StatusError {
			foundError = true
		}
	}
	if !foundError {
		t.Fatalf("doctor should flag the unrecognized auth.json: %+v", checks)
	}

	// Non-object root: applying refuses instead of replacing the file.
	write(t, authPath, `["not","an","object"]`)
	err = artifact.ApplyLive(context.Background(), specs[0],
		artifact.Value{Data: []byte(`{"type":"oauth"}`), Present: true})
	if !errors.Is(err, artifact.ErrUnsafe) {
		t.Fatalf("expected apply refusal on non-object root: %v", err)
	}
}
