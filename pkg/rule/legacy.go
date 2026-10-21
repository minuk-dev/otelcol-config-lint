package rule

import (
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// DecodeScalar reads quoted numbers and booleans using the target Collector's
// default confmap behavior. The returned node retains the source location.
func (c *Context) DecodeScalar(node *yaml.Node, typ string) *yaml.Node {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" ||
		HasExpansion(node.Value) || c == nil || c.Schema == nil {
		return node
	}

	// shortcut: use release defaults; add overrides when Collector feature gates are modeled.
	version := c.Schema.CollectorVersion
	if version == "" || version == schema.Latest || schema.Compare(version, "v0.105.0") >= 0 {
		return node
	}

	return decodeQuotedScalar(node, typ)
}

func decodeQuotedScalar(node *yaml.Node, typ string) *yaml.Node {
	// StrictlyTypedInput became the default in v0.105.0. Keep the source tree unchanged.
	value := node.Value

	var tag string

	switch typ {
	case "int":
		// shortcut: schemas erase integer widths; support int64 until schemas distinguish unsigned fields.
		if value == "" {
			value = "0"
		}

		num, err := strconv.ParseInt(value, 0, 64)
		if err != nil {
			return node
		}

		value, tag = strconv.FormatInt(num, 10), intTag
	case "float":
		if value == "" {
			value = "0"
		}

		num, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return node
		}

		value, tag = strconv.FormatFloat(num, 'g', -1, 64), floatTag
	case "bool":
		if value == "" {
			value = "false"
		}

		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return node
		}

		value, tag = strconv.FormatBool(enabled), BoolTag
	default:
		return node
	}

	decoded := *node
	decoded.Value, decoded.Tag = value, tag

	return &decoded
}
