package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// TestPlansFromBackupMetaPreservesKeychainAccount guards the rollback path:
// a keychain artifact's account must survive the metadata round-trip so a
// recreated item is restored under the tool's own account (e.g. cursor-user),
// not the generic fallback.
func TestPlansFromBackupMetaPreservesKeychainAccount(t *testing.T) {
	meta := backup.Meta{Artifacts: []backup.ArtifactRecord{{
		Tool: constants.ToolCursor, Name: "access_token", Kind: constants.KindKeychain,
		Target: "cursor-access-token", KeychainAccount: "cursor-user",
		SecretRef: "backup/x/cursor/access_token", Present: true,
	}}}
	plans := plansFromBackupMeta(meta)
	if len(plans) != 1 || len(plans[0].Specs) != 1 {
		t.Fatalf("unexpected plans: %+v", plans)
	}
	if got := plans[0].Specs[0].KeychainAccount; got != "cursor-user" {
		t.Fatalf("keychain account lost in metadata round-trip: %q", got)
	}
}

// TestSpecFromRecordRestoresJSONC guards the restore path for JSONC targets
// (GitHub Copilot's commented config.json): if specFromRecord drops the JSONC
// bit, applyBackup falls through to the plain-JSON patch, which rejects the
// leading // comments and fails the rollback/restore. The reconstructed spec
// must patch through the comment-preserving path instead.
func TestSpecFromRecordRestoresJSONC(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	doc := "// managed automatically\n{\n  \"trustedFolders\": [\"/w\"],\n  \"lastLoggedInUser\": {\"host\":\"h\",\"login\":\"a\"}\n}\n"
	if err := os.WriteFile(cfg, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := backup.ArtifactRecord{
		Tool: constants.ToolCopilot, Name: "last_logged_in_user",
		Kind: constants.KindJSONPointer, Target: cfg, Pointer: "/lastLoggedInUser",
		JSONC: true, SecretRef: "backup/x/copilot/last_logged_in_user", Present: true,
	}
	if got := specFromRecord(rec); !got.JSONC {
		t.Fatalf("JSONC bit lost in metadata round-trip: %+v", got)
	}
	value := artifact.Value{Data: []byte(`{"host":"h","login":"b"}`), Present: true}
	if err := artifact.ApplyLive(context.Background(), specFromRecord(rec), value); err != nil {
		t.Fatalf("restore of a JSONC config must not fail on comments: %v", err)
	}
	out, _ := os.ReadFile(cfg)
	if s := string(out); !strings.Contains(s, "// managed automatically") || !strings.Contains(s, `"login":"b"`) {
		t.Fatalf("restore lost the comment or did not switch the value:\n%s", s)
	}
}
