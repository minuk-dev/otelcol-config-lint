package config

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// headComments records source lines before normalization changes node positions.
// Only parser-recognized head comments are considered; text in block or quoted
// scalars is never a directive, even if a line starts with a hash.
func headComments(root *yaml.Node, source string) map[int]string {
	// Match yaml.v3's line counting, including CRLF as a single break.
	breaks := strings.NewReplacer("\r\n", "\n", "\r", "\n", "\u0085", "\n", "\u2028", "\n", "\u2029", "\n")
	lines := strings.Split(breaks.Replace(source), "\n")
	out := map[int]string{}

	pending := []*yaml.Node{root}
	for len(pending) > 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]

		if node.HeadComment != "" {
			comments := strings.Split(node.HeadComment, "\n")

			next := len(comments) - 1
			//nolint:mnd // preceding physical line in zero-based source coordinates
			for i := node.Line - 2; i >= 0 && next >= 0; i-- {
				text := strings.TrimSpace(lines[i])
				if text == "" {
					continue
				}

				for next >= 0 && strings.TrimSpace(comments[next]) == "" {
					next--
				}

				if next < 0 || text != strings.TrimSpace(comments[next]) {
					break
				}

				out[i+1] = text
				next--
			}
		}

		pending = append(pending, node.Content...)
	}

	return out
}
