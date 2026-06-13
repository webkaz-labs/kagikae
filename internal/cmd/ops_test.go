package cmd

import (
	"testing"

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
