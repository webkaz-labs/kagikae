package distribution

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func ShellQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }

// CurlFixture handles only the installer's fixed download argv and exact URLs.
// It receives trusted local fixture/verified assets; it never fetches a URL.
func CurlFixture(files map[string]string) (string, error) { return CurlFixtureWithVerdict(files, "") }

func CurlFixtureWithVerdict(files map[string]string, rejectionPath string) (string, error) {
	commands := map[string]string{}
	for url, path := range files {
		if !filepath.IsAbs(path) {
			return "", errors.New("fixture path must be absolute")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("fixture not regular")
		}
		commands[url] = "cp " + ShellQuote(path) + " \"$6\""
	}
	return curlFixture(commands, rejectionPath)
}

func curlFixture(commands map[string]string, rejectionPath string) (string, error) {
	if len(commands) == 0 {
		return "", errors.New("empty installer fixture")
	}
	urls := make([]string, 0, len(commands))
	for url := range commands {
		if !strings.HasPrefix(url, "https://") {
			return "", errors.New("expected exact HTTPS fixture URL")
		}
		urls = append(urls, url)
	}
	sort.Strings(urls)
	shim := "#!/bin/sh\nset -eu\n"
	failure := "exit 90"
	if rejectionPath != "" {
		if !filepath.IsAbs(rejectionPath) {
			return "", errors.New("rejection path must be absolute")
		}
		shim += "reject() { : > " + ShellQuote(rejectionPath) + "; exit 90; }\n"
		failure = "reject"
	}
	shim += "[ \"$#\" -eq 7 ] || " + failure + "\n[ \"$1\" = --fail ] && [ \"$2\" = --location ] && [ \"$3\" = --silent ] && [ \"$4\" = --show-error ] && [ \"$5\" = --output ] || " + failure + "\ncase \"$7\" in\n"
	for _, url := range urls {
		shim += ShellQuote(url) + ") " + commands[url] + " ;;\n"
	}
	return shim + "*) " + failure + " ;;\nesac\n", nil
}

// Installer accepts a trusted installer with conventional --version/--install-dir
// arguments. Product-specific receipt and lifecycle checks remain in adapters.
type Installer struct {
	Path          string
	RepositoryEnv string
	Binary        string
	VersionArgs   []string
	Expected      string
}
