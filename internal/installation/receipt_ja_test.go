package installation

import (
	"errors"
	"os"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// TestFinalizationFailureRendersInJapanese pins the error kae install ends on
// when the binary is in place but its receipt cannot be finalized: English
// Error() unchanged, Japanese on a human sink, and the storage cause reachable
// and quoted verbatim in both.
func TestFinalizationFailureRendersInJapanese(t *testing.T) {
	l10ntest.UseJapanese(t)
	root, r := testReceipt(t)
	if err := os.WriteFile(r.Destination, []byte("previous binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.Status = constants.InstallPending
	failure := errors.New("receipt storage failed")
	_, err := recordReplacement(root, r, []byte("replacement binary"), func(root string, next Receipt) error {
		if next.Status == constants.InstallActive {
			return failure
		}
		return save(root, next)
	})
	if !errors.Is(err, failure) {
		t.Fatalf("storage failure hidden: %v", err)
	}
	l10ntest.ErrorText(t, "finalize", err,
		"binary installed; receipt finalization failed; reinstall to repair: "+failure.Error(),
		"バイナリはインストールしましたが、インストール記録を確定できませんでした。再インストールして修復してください: "+failure.Error(), false)
}
