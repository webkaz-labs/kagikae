package distribution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

func (s Spec) ValidateStatement(raw []byte, dir string) error {
	tag, commit := s.Tag, s.Commit
	repository := s.Repository
	var statement struct {
		Type          string `json:"_type"`
		PredicateType string `json:"predicateType"`
		Subject       []struct {
			Name   string            `json:"name"`
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
		Predicate struct {
			Project string `json:"project"`
			Version string `json:"version"`
			Source  struct {
				Repo   string `json:"repo"`
				Commit string `json:"commit"`
				Tag    string `json:"tag"`
			} `json:"source"`
			Artifacts []map[string]json.RawMessage `json:"artifacts"`
			Resources []map[string]string          `json:"resources"`
		} `json:"predicate"`
	}
	if len(raw) > 1024*1024 || json.Unmarshal(raw, &statement) != nil {
		return errors.New("invalid packslip statement")
	}
	p := statement.Predicate
	base := "https://github.com/" + repository
	if statement.Type != "https://in-toto.io/Statement/v1" || statement.PredicateType != "https://packslip.dev/release/v1" ||
		p.Project != s.Project || p.Version != s.Version ||
		p.Source.Repo != base || p.Source.Tag != tag || p.Source.Commit != commit {
		return errors.New("packslip project/version/source mismatch")
	}
	names := make([]string, 0, len(s.Artifacts))
	for _, a := range s.Artifacts {
		names = append(names, a.Name)
	}
	if len(statement.Subject) != len(names) || len(p.Artifacts) != len(names) {
		return errors.New("packslip archive set mismatch")
	}
	for _, expected := range s.Artifacts {
		name := expected.Name
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		subjects, artifacts := 0, 0
		for _, subject := range statement.Subject {
			if subject.Name == name {
				subjects++
				if subject.Digest["sha256"] != hex.EncodeToString(digest[:]) {
					return errors.New("packslip subject digest mismatch")
				}
			}
		}
		for _, artifact := range p.Artifacts {
			var gotName string
			if json.Unmarshal(artifact["name"], &gotName) != nil || gotName != name {
				continue
			}
			artifacts++
			want := map[string]any{
				"name": name, "os": expected.OS, "arch": expected.Arch, "size": float64(len(data)),
				"url": base + "/releases/download/" + tag + "/" + name, "format": "tar.gz", "bin": []any{expected.Binary},
				"provenance": []any{"https://api.github.com/repos/" + repository + "/attestations/sha256:" + hex.EncodeToString(digest[:])},
			}
			if expected.Libc != "" {
				want["libc"] = expected.Libc
			}
			for key, value := range want {
				var got any
				if json.Unmarshal(artifact[key], &got) != nil || !reflect.DeepEqual(got, value) {
					return fmt.Errorf("packslip artifact %s mismatch: %s", name, key)
				}
			}
			for key := range artifact {
				if _, known := want[key]; !known && key != "requires" {
					return fmt.Errorf("unexpected packslip artifact field: %s", key)
				}
			}
		}
		if subjects != 1 || artifacts != 1 {
			return errors.New("packslip duplicate or missing archive")
		}
	}
	if len(p.Resources) != len(s.Resources) {
		return errors.New("packslip completion resource set mismatch")
	}
	used := make([]bool, len(s.Resources))
	for _, r := range p.Resources {
		found := false
		for i, w := range s.Resources {
			if !used[i] && reflect.DeepEqual(r, w) {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			return errors.New("packslip completion resource mismatch")
		}
	}
	return nil
}
