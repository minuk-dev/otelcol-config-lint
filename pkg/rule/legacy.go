package rule

import (
	"strconv"

	"gopkg.in/yaml.v3"
)

// DecodeScalar reads quoted numbers and booleans using the target Collector's
// default confmap behavior. The returned node retains the source location.
func (c *Context) DecodeScalar(node *yaml.Node, typ string) *yaml.Node {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != strTag ||
		HasExpansion(node.Value) || c == nil || c.Schema == nil {
		return node
	}

	weak, _, known := c.Schema.ScalarDecoding()
	if !known || !weak {
		return node
	}

	return decodeQuotedScalar(node, typ)
}

func decodeQuotedScalar(node *yaml.Node, typ string) *yaml.Node {
	// StrictlyTypedInput became the default in v0.105.0. Keep the source tree unchanged.
	value := node.Value

	var tag string

	switch typ {
	case typeInt:
		// shortcut: schemas erase integer widths; support int64 until schemas distinguish unsigned fields.
		if value == "" {
			value = "0"
		}

		num, err := strconv.ParseInt(value, 0, 64)
		if err != nil {
			return node
		}

		value, tag = strconv.FormatInt(num, 10), intTag
	case typeFloat:
		if value == "" {
			value = "0"
		}

		num, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return node
		}

		value, tag = strconv.FormatFloat(num, 'g', -1, 64), floatTag
	case typeBool:
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

// ReadInt uses the selected decoder while retaining the original scalar for range checks.
func (c *Context) ReadInt(node *yaml.Node) Setting {
	setting := ReadInt(c.DecodeScalar(node, typeInt))
	setting.Node = node

	return setting
}
