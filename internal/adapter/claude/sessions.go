package claude

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/unicode/norm"
)

// The naming rule is owned by docs/CLI.md § kae ls Semantics; the measurements
// behind it and how to re-verify them are docs/VALIDATION.md § Upstream Behaviour
// Assumptions.

// ProjectDirNameEnv is the variable that replaces the per-working-directory name
// claude gives a directory under `projects/`.
const ProjectDirNameEnv = "CLAUDE_CODE_PROJECT_DIR_NAME"

// maxProjectDirNameLen is how many converted characters claude keeps before it
// appends the path's hash.
const maxProjectDirNameLen = 200

var (
	projectDirNameOverride = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	reservedDeviceName     = regexp.MustCompile(`(?i)^(?:con|prn|aux|nul|com[0-9]|lpt[0-9])$`)
)

// validProjectDirName reports whether claude honours value as
// CLAUDE_CODE_PROJECT_DIR_NAME: 1-64 of [A-Za-z0-9_-] and not a device name. An
// invalid value is ignored upstream and the derived name is used.
func validProjectDirName(value string) bool {
	return projectDirNameOverride.MatchString(value) && !reservedDeviceName.MatchString(value)
}

// SessionDirName is the name claude files the current directory's transcripts
// under, given the facts the caller observed: override is the value of
// CLAUDE_CODE_PROJECT_DIR_NAME in the caller's environment, configDirInLaunchEnv
// whether CLAUDE_CONFIG_DIR is in the environment claude is launched in (without
// it claude ignores the override), and physicalCwd reads the physical working
// directory. It is not called when the override is honoured, and only then is a
// failure to read the directory not an error.
func SessionDirName(override string, configDirInLaunchEnv bool, physicalCwd func() (string, error)) (string, error) {
	if configDirInLaunchEnv && validProjectDirName(override) {
		return override, nil
	}
	cwd, err := physicalCwd()
	if err != nil {
		return "", err
	}
	return ProjectDirName(cwd), nil
}

// ProjectDirName is the directory under `<config dir>/projects/` that claude
// files a session's transcripts in, for physicalCwd, the process's physical
// working directory (docs/CLI.md § kae ls Semantics owns the measurement). It
// takes the path claude sees: symlinks resolved and the on-disk case, so
// syscall.Getwd, not a spelling the user typed.
//
// The path is NFC-normalised, then each UTF-16 code unit that is not
// [A-Za-z0-9] becomes "-" (an astral character therefore gives two). A converted
// name over 200 characters keeps its first 200, then "-" and the base 36 of the
// hash of the (NFC) path's UTF-16 units.
func ProjectDirName(physicalCwd string) string {
	units := utf16.Encode([]rune(norm.NFC.String(physicalCwd)))
	var b strings.Builder
	b.Grow(len(units))
	for _, u := range units {
		if u < 0x80 && isASCIIAlnum(byte(u)) {
			b.WriteByte(byte(u))
		} else {
			b.WriteByte('-')
		}
	}
	name := b.String()
	if len(name) <= maxProjectDirNameLen {
		return name
	}
	return name[:maxProjectDirNameLen] + "-" + hashSuffix(javaHash(units))
}

func isASCIIAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

// javaHash is Java's String.hashCode over UTF-16 units: h = h*31 + unit in int32.
func javaHash(units []uint16) int32 {
	var h int32
	for _, u := range units {
		h = h*31 + int32(u)
	}
	return h
}

// hashSuffix is the base 36 of h's absolute value, widened to 64 bits first so
// that the minimum int32 has one (`zik0zk`).
func hashSuffix(h int32) string {
	v := int64(h)
	if v < 0 {
		v = -v
	}
	return strconv.FormatInt(v, 36)
}
