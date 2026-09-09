package distribution

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var checksumPattern = regexp.MustCompile(`^([0-9a-fA-F]{64})  ([^\s]+)$`)

func ReadArchive(path string, members map[string]bool, binary string) ([]byte, error) {
	if len(members) == 0 {
		return nil, errors.New("empty archive member set")
	}
	for name, enabled := range members {
		if !enabled || !safePath(name) {
			return nil, errors.New("invalid archive member specification")
		}
	}
	if binary != "" && !members[binary] {
		return nil, errors.New("binary absent from member set")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("archive not regular")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	t := tar.NewReader(z)
	seen := map[string]bool{}
	var payload []byte
	for {
		h, e := t.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if seen[h.Name] || !members[h.Name] || h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("unexpected, duplicate or nonregular archive member: %s", h.Name)
		}
		seen[h.Name] = true
		if h.Name == binary && binary != "" {
			payload, err = io.ReadAll(t)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(seen) != len(members) {
		return nil, errors.New("missing archive member")
	}
	// Consume the gzip trailer too, so truncated/compressed corruption fails.
	if _, err = io.Copy(io.Discard, z); err != nil {
		return nil, err
	}
	return payload, nil
}

func VerifyArchives(dir string, artifacts []Artifact) error {
	if len(artifacts) == 0 {
		return errors.New("empty artifact set")
	}
	seen := map[string]bool{}
	for _, a := range artifacts {
		if !identifier.MatchString(a.Name) || !strings.HasSuffix(a.Name, ".tar.gz") || seen[a.Name] {
			return errors.New("invalid artifact name or duplicate")
		}
		seen[a.Name] = true
		if len(a.Members) == 0 || len(a.MemberSet()) != len(a.Members) {
			return errors.New("empty or duplicate member set")
		}
	}
	names := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		names = append(names, a.Name)
	}
	data, err := readRegular(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return err
	}
	manifest := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		m := checksumPattern.FindStringSubmatch(line)
		if m == nil || manifest[m[2]] != "" {
			return errors.New("malformed or duplicate checksum entry")
		}
		manifest[m[2]] = strings.ToLower(m[1])
	}
	if len(manifest) != len(names) {
		return errors.New("unexpected checksum manifest set")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.tar.gz"))
	if err != nil {
		return err
	}
	if len(files) != len(names) {
		return errors.New("unexpected downloaded archive set")
	}
	for _, artifact := range artifacts {
		name := artifact.Name
		if manifest[name] == "" {
			return errors.New("missing checksum entry")
		}
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("archive is not a regular file")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if hex.EncodeToString(h.Sum(nil)) != manifest[name] {
			return fmt.Errorf("checksum mismatch: %s", name)
		}
		if _, err := ReadArchive(path, artifact.MemberSet(), ""); err != nil {
			return err
		}
	}
	return nil
}
