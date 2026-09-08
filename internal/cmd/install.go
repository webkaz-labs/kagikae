package cmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/installation"
)

// CmdInstall is the staged binary's hidden installer seam. Release transport and
// checksums are verified by the invoking installer before this binary is run.
func CmdInstall(args []string) int {
	flags, positionals := splitArgs(args, "--destination", "--source-kind", "--expected-version")
	var destination, sourceKind, expectedVersion string
	opts, ok := parseCommon("__install", flags, false, func(fs *flag.FlagSet) {
		fs.StringVar(&destination, "destination", "", "absolute direct executable destination")
		fs.StringVar(&sourceKind, "source-kind", "", "release or local_build")
		fs.StringVar(&expectedVersion, "expected-version", "", "required release version")
	})
	if !ok || len(positionals) != 0 || !opts.Yes || !filepath.IsAbs(destination) {
		return usageError("__install requires --yes --destination <absolute path> --source-kind <release|local_build>")
	}
	if sourceKind == constants.InstallSourceRelease && expectedVersion != toolVersion {
		return finish(opts, errf(constants.ExitUnsafeRefused, "staged binary version differs from requested release"))
	}
	if sourceKind != constants.InstallSourceRelease && sourceKind != constants.InstallSourceLocal {
		return usageError("unsupported installation source")
	}
	source, err := os.Executable()
	if err != nil {
		return finish(opts, err)
	}
	app := newApp(opts.ConfigPath)
	r, err := installation.Install(app.Paths.InstallationsDir(), source, destination, toolVersion, sourceKind)
	if errors.Is(err, installation.ErrUnsafe) {
		err = errf(constants.ExitUnsafeRefused, "direct installation ownership could not be verified")
	}
	if err != nil {
		return finish(opts, err)
	}
	if opts.Format == formatJSON {
		return encodeJSON(r)
	}
	fmt.Printf("Installed kae %s to %s (removal receipt recorded)\n", toolVersion, app.displayPath(destination))
	return constants.ExitOK
}
