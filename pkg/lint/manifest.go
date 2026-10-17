package lint

import (
	"bytes"
	"errors"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
)

// kubernetesDocuments only skips a complete, well-formed manifest stream.
// Mixed streams and malformed trailing documents go through normal validation.
func kubernetesDocuments(path string, src []byte) bool {
	decoder := yaml.NewDecoder(bytes.NewReader(src))
	found := false

	for {
		var document yaml.Node

		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			return found
		}

		if err != nil {
			return false
		}

		if len(document.Content) == 0 {
			continue
		}

		root := document.Content[0]
		if root.Tag == "!!null" {
			continue
		}

		if root.Kind != yaml.MappingNode || !manifestRoot(root) {
			return false
		}

		encoded, err := yaml.Marshal(&document)
		if err != nil {
			return false
		}

		parsed, err := config.Parse(path, encoded)
		if err != nil || len(parsed.DuplicateKeys) != 0 {
			return false
		}

		if !manifestRoot(parsed.Root) {
			return false
		}

		found = true
	}
}

func manifestRoot(root *yaml.Node) bool {
	for _, key := range []string{"apiVersion", "kind"} {
		node := mappingValue(root, key)
		if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Value == "" {
			return false
		}
	}

	return true
}
