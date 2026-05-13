package plugin

// FieldType describes the expected type of a plugin config value.
type FieldType string

const (
	FieldTypeString   FieldType = "string"   // plain string
	FieldTypePattern  FieldType = "pattern"  // string with {field} runtime interpolation
	FieldTypeInt      FieldType = "int"      // integer
	FieldTypeBool     FieldType = "bool"     // boolean
	FieldTypeDuration FieldType = "duration" // Go duration string, e.g. "1h", "30m"
	FieldTypeEnum     FieldType = "enum"     // string restricted to Enum values
	FieldTypeList     FieldType = "list"     // []string
	FieldTypeDict     FieldType = "dict"     // map[string]any (sub-plugin lists, nested config)
)

// FieldSchema describes one config key accepted by a plugin.
// Plugins declare their schema by populating Descriptor.Schema; plugins
// without a schema still work but get a generic key-value editor in the
// visual pipeline editor.
type FieldSchema struct {
	Key       string    // config map key, e.g. "url", "tracking"
	Type      FieldType // expected value type
	Required  bool      // whether the key must be present
	Default   any       // optional default shown as placeholder (nil = no default)
	Enum      []string  // valid values for FieldTypeEnum
	Hint      string    // one-line description shown in the visual editor
	Multiline bool      // opens a full text-editor modal instead of an inline input
}
