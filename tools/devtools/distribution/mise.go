package distribution

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// MinimumMise is the oldest mise the kagikae Packslip consumer has passed. Newer
// releases are accepted; refusal controls and each result's recorded version are
// the compatibility evidence, not a reviewed version list.
const MinimumMise = "2026.9.3"

// MiseAtLeast parses the leading YYYY.M.P of `mise --version` and fails closed
// on anything it cannot compare numerically. It returns the parsed version.
func MiseAtLeast(output, minimum string) (string, error) {
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return "", errors.New("mise --version printed no version")
	}
	got, ok := Triple(fields[0])
	want, wantOK := Triple(minimum)
	if !ok || !wantOK {
		return "", fmt.Errorf("unrecognized mise version %q", fields[0])
	}
	if slices.Compare(got[:], want[:]) < 0 {
		return "", fmt.Errorf("requires mise %s or later; found %s", minimum, fields[0])
	}
	return fields[0], nil
}

// Triple parses exactly three dot-separated decimal fields (calver or semver
// without pre-release or build suffixes).
func Triple(text string) ([3]uint64, bool) {
	var parts [3]uint64
	fields := strings.Split(text, ".")
	if len(fields) != len(parts) {
		return parts, false
	}
	for i, field := range fields {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return parts, false
		}
		parts[i] = value
	}
	return parts, true
}
