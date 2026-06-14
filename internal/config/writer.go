package config

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/creachadair/tomledit"
	"github.com/creachadair/tomledit/parser"
	"github.com/creachadair/tomledit/transform"
)

// Editor applies surgical, comment-preserving edits to a config.toml document.
//
// Load decodes config with BurntSushi/toml, which drops every comment on
// re-encode, so mutations that must keep the file's comments, field order, and
// unrelated keys intact — the profile edits driven by account rm/rename and the
// kae profile commands — go through this editor instead of a decode/encode
// round-trip. It edits the parsed TOML syntax tree directly. The caller holds
// the config lock and writes Bytes back atomically.
type Editor struct {
	doc *tomledit.Document
}

// NewEditor parses config.toml content for editing.
func NewEditor(data []byte) (*Editor, error) {
	doc, err := tomledit.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse config for editing: %w", err)
	}
	return &Editor{doc: doc}, nil
}

// Bytes renders the edited document, preserving comments and ordering.
func (e *Editor) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	if err := tomledit.Format(&buf, e.doc); err != nil {
		return nil, fmt.Errorf("format config: %w", err)
	}
	return buf.Bytes(), nil
}

// SetProfileAccount sets profiles.<profile>.accounts.<tool> = account, creating
// the accounts subtable if it does not exist yet. An existing value for the
// tool is replaced.
func (e *Editor) SetProfileAccount(profile, tool, account string) {
	val := parser.MustValue(strconv.Quote(account))
	// Replacing an existing mapping touches only the value datum so the
	// mapping's own trailing/block comments survive the edit.
	if cur := e.doc.First("profiles", profile, "accounts", tool); cur != nil && cur.IsMapping() {
		val.Trailer = cur.Value.Trailer
		cur.KeyValue.Value = val
		return
	}
	kv := &parser.KeyValue{Name: parser.Key{tool}, Value: val}
	if tab := transform.FindTable(e.doc, "profiles", profile, "accounts"); tab != nil {
		transform.InsertMapping(tab.Section, kv, false)
		return
	}
	e.doc.Sections = append(e.doc.Sections, &tomledit.Section{
		Heading: &parser.Heading{Name: parser.Key{"profiles", profile, "accounts"}},
		Items:   []parser.Item{kv},
	})
}

// RemoveProfileAccount removes profiles.<profile>.accounts.<tool>. It reports
// whether the mapping existed.
func (e *Editor) RemoveProfileAccount(profile, tool string) bool {
	return e.doc.First("profiles", profile, "accounts", tool).Remove()
}

// ClearProfileAccounts removes the [profiles.<profile>.accounts] subtable
// while leaving the profile's other keys (e.g. label) intact. It reports
// whether the subtable existed. Used by profile save to overwrite the account
// set without dropping a hand-written label.
func (e *Editor) ClearProfileAccounts(profile string) bool {
	return e.removeSectionsByPrefix(parser.Key{"profiles", profile, "accounts"})
}

// RemoveProfile removes the whole [profiles.<profile>] section and every
// subtable under it (e.g. .accounts). It reports whether anything was removed.
func (e *Editor) RemoveProfile(profile string) bool {
	return e.removeSectionsByPrefix(parser.Key{"profiles", profile})
}

// removeSectionsByPrefix drops every section whose table name is prefixed by
// key (the section itself and any subtables), reporting whether any matched.
func (e *Editor) removeSectionsByPrefix(key parser.Key) bool {
	kept := e.doc.Sections[:0]
	removed := false
	for _, s := range e.doc.Sections {
		if key.IsPrefixOf(s.TableName()) {
			removed = true
			continue
		}
		kept = append(kept, s)
	}
	e.doc.Sections = kept
	return removed
}

// SetDefaultProfile sets the top-level default_profile key; an empty name
// clears it. It reports whether the document changed.
func (e *Editor) SetDefaultProfile(name string) bool {
	cur := e.doc.First("default_profile")
	if name == "" {
		return cur.Remove()
	}
	val := parser.MustValue(strconv.Quote(name))
	if cur != nil && !cur.IsSection() {
		cur.Value = val
		return true
	}
	kv := &parser.KeyValue{Name: parser.Key{"default_profile"}, Value: val}
	if e.doc.Global == nil {
		e.doc.Global = &tomledit.Section{}
	}
	e.doc.Global.Items = append(e.doc.Global.Items, kv)
	return true
}
