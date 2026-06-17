package cmd

import "fmt"

// didYouMean returns a " — did you mean %q?" suffix naming the single nearest
// candidate to input, or "" when none is close enough to suggest. It is
// suggestion-only: callers append it to an existing error whose exit code and
// JSON contract are unchanged; only the human-facing message gains the hint.
func didYouMean(input string, candidates []string) string {
	if m, ok := nearestMatch(input, candidates); ok {
		return fmt.Sprintf(" — did you mean %q?", m)
	}
	return ""
}

// nearestMatch returns the single closest candidate to input by Levenshtein
// distance and true, when it is close enough to suggest. To avoid noise it
// suggests only when the best distance is both <= 2 and <= len(input)/3 + 1
// (so a short typo of a long word still hints, but a wildly different token
// does not). An exact match, a tie for the best distance, or no candidate under
// the threshold returns ("", false) — the caller appends nothing and the error
// is unchanged.
func nearestMatch(input string, candidates []string) (string, bool) {
	bestDist := -1
	best := ""
	tie := false
	for _, c := range candidates {
		if c == input {
			return "", false // exact match: nothing to suggest
		}
		d := levenshtein(input, c)
		switch {
		case bestDist == -1 || d < bestDist:
			bestDist, best, tie = d, c, false
		case d == bestDist:
			tie = true
		}
	}
	if best == "" || tie {
		return "", false
	}
	limit := len(input)/3 + 1
	if bestDist > 2 || bestDist > limit {
		return "", false
	}
	return best, true
}

// levenshtein computes the edit distance between a and b with the standard
// single-row dynamic-programming table. Hand-rolled to keep kae dependency-free
// (docs/RELEASE.md v0.8.5 §A: no fuzzy-matching dependency).
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr := make([]int, len(rb)+1)
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = curr
	}
	return prev[len(rb)]
}
