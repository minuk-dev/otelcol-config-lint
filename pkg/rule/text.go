package rule

import (
	"sort"
	"strconv"
	"strings"
)

// pathParts splits a settings path into section, component id and the rest.
const pathParts = 3

// Quote wraps a name the way every finding quotes one.
func Quote(s string) string { return "\"" + s + "\"" }

// List renders a set of names for a message, cut off before it becomes a wall
// of text.
func List(items []string) string {
	const maxItems = 8

	if len(items) > maxItems {
		items = append(items[:maxItems:maxItems], "...")
	}

	return strings.Join(items, ", ")
}

// Article returns the indefinite article a word takes, so a message built out
// of a component kind reads as a sentence.
func Article(word string) string {
	if word == "" {
		return "a"
	}

	switch word[0] {
	case 'a', 'e', 'i', 'o', 'u':
		return "an"
	default:
		return "a"
	}
}

// Suggest appends a "did you mean" clause when one candidate is a close match.
func Suggest(got string, candidates []string) string {
	if best, ok := bestMatch(got, candidates); ok {
		return "; did you mean " + Quote(best) + "?"
	}

	return ""
}

// SortedKeys returns a map's keys in sorted order, for stable messages.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// ComponentOf renders the "receivers.otlp" prefix of a settings path.
func ComponentOf(path string) string {
	parts := strings.SplitN(path, ".", pathParts)
	if len(parts) < pathParts-1 {
		return path
	}

	return parts[0] + "." + parts[1]
}

// ShortPath drops the section prefix, leaving the settings path a user typed.
func ShortPath(path string) string {
	parts := strings.SplitN(path, ".", pathParts)
	if len(parts) < pathParts {
		return path
	}

	return parts[2]
}

// Itoa renders a count for a message.
func Itoa(i int) string { return strconv.Itoa(i) }

// Itoa64 renders a setting's value for a message.
func Itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// bestMatch finds the candidate a user most likely meant. Beyond simple typos
// it handles the two mistakes specific to collector configs: writing the Go
// package name instead of the component type ("prometheusreceiver"), and
// missing the underscores upstream added when types were renamed
// ("hostmetrics" for "host_metrics").
func bestMatch(got string, candidates []string) (string, bool) {
	got = strings.ToLower(got)

	trimmed := got
	for _, suffix := range []string{"receiver", "processor", "exporter", "extension", "connector"} {
		if s, ok := strings.CutSuffix(got, suffix); ok && s != "" {
			trimmed = s

			break
		}
	}

	squashed := strings.ReplaceAll(trimmed, "_", "")

	var best string

	bestDist := 3 // only suggest reasonably close matches

	for _, c := range candidates {
		if strings.ReplaceAll(c, "_", "") == squashed {
			return c, true
		}

		if d := editDistance(trimmed, c); d < bestDist {
			best, bestDist = c, d
		}
	}

	return best, best != ""
}

// editDistance is the Levenshtein distance between two short strings.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i

		for j := 1; j <= len(b); j++ { //nolint:varnamelen // j is the inner index of a matrix walk
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			curr[j] = min(prev[j]+1, min(curr[j-1]+1, prev[j-1]+cost))
		}

		prev, curr = curr, prev
	}

	return prev[len(b)]
}
