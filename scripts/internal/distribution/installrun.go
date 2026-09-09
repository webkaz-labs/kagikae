package distribution

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/webkaz-labs/kagikae/scripts/internal/commandrun"
)

func (v Verified) RunInstaller(ctx context.Context, installer Installer) (err error) {
	if len(v.assets) == 0 {
		return errors.New("release not verified")
	}
	if !filepath.IsAbs(installer.Path) || !identifier.MatchString(installer.Binary) || !regexp.MustCompile(`^[A-Z][A-Z0-9_]*_REPO$`).MatchString(installer.RepositoryEnv) {
		return errors.New("invalid trusted installer spec")
	}
	source, err := readRegular(installer.Path)
	if err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "distribution-install-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, RemoveWorkdir(root)) }()
	env, err := IsolatedEnvironment(root)
	if err != nil {
		return err
	}
	shimDir := filepath.Join(root, "shim")
	if err = os.Mkdir(shimDir, 0o700); err != nil {
		return err
	}
	responses := map[string]Response{}
	for name, data := range v.assets {
		responses["/"+name] = Response{Body: data}
	}
	fixture := NewFixture(responses)
	server := httptest.NewUnstartedServer(fixture)
	server.Config.ReadHeaderTimeout = 5 * time.Second
	server.Start()
	defer func() {
		server.Close()
		err = errors.Join(err, fixture.Verdict())
	}()
	rejection := filepath.Join(root, "fixture-rejected")
	commands := map[string]string{}
	for name := range v.assets {
		url := "https://github.com/" + v.spec.Repository + "/releases/download/" + v.spec.Tag + "/" + name
		commands[url] = "/usr/bin/curl --fail --silent --show-error --output \"$6\" " + ShellQuote(server.URL+"/"+name)
	}
	shim, err := curlFixture(commands, rejection)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(shimDir, "curl"), []byte(shim), 0o700); err != nil {
		return err
	}
	path := filepath.Join(root, "installer.sh")
	if err = os.WriteFile(path, source, 0o600); err != nil {
		return err
	}
	env[0] = "PATH=" + shimDir + ":/usr/bin:/bin"
	env = append(env, installer.RepositoryEnv+"="+v.spec.Repository)
	destination := filepath.Join(root, "bin")
	r, err := (commandrun.Command{Name: "/bin/sh", Args: []string{path, "--version", v.spec.Tag, "--install-dir", destination}, Env: env, Dir: root, Timeout: 2 * time.Minute}).Run(ctx)
	if err != nil {
		return err
	}
	if _, e := os.Lstat(rejection); e == nil {
		return errors.New("installer ignored fixture transport rejection")
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if r.ExitCode != 0 {
		return errors.New("installer failed")
	}
	r, err = (commandrun.Command{Name: filepath.Join(destination, installer.Binary), Args: installer.VersionArgs, Env: env, Dir: root, Timeout: 2 * time.Minute}).Run(ctx)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 || r.Stdout != installer.Expected {
		return errors.New("installed version mismatch")
	}
	return nil
}
