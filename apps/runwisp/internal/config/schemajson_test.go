// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

// compileSchema loads the embedded JSON Schema. A failure here means the schema
// is not a well-formed draft 2020-12 document.
func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(SchemaJSON()))
	require.NoError(t, err, "embedded schema is not valid JSON")
	c := jsonschema.NewCompiler()
	require.NoError(t, c.AddResource("config.schema.json", doc))
	sch, err := c.Compile("config.schema.json")
	require.NoError(t, err, "embedded schema is not a valid JSON Schema")
	return sch
}

// TestSchemaCompiles asserts the embedded schema is itself a valid JSON Schema.
func TestSchemaCompiles(t *testing.T) {
	compileSchema(t)
}

// TestSchemaCoversWireTags is the anti-drift guard: every TOML key the config
// layer decodes (a `toml:"..."` tag on a wire struct) must appear as a declared
// property somewhere in the JSON Schema. Add a key to wire.go without updating
// config.schema.json and this fails — keeping the hand-authored schema honest.
func TestSchemaCoversWireTags(t *testing.T) {
	schemaKeys := collectSchemaPropertyNames(t)

	wireKeys := map[string]struct{}{}
	collectTOMLTags(reflect.TypeOf(tomlConfig{}), wireKeys, map[reflect.Type]bool{})

	var missing []string
	for key := range wireKeys {
		if _, ok := schemaKeys[key]; !ok {
			missing = append(missing, key)
		}
	}
	require.Empty(t, missing,
		"config.schema.json is missing properties for TOML keys declared in wire.go: %v", missing)
}

// TestSchemaAcceptsRealConfigs validates the fixtures RunWisp itself ships —
// the demo config and every importer golden — against the schema. These are all
// valid configs, so a rejection means the schema is too strict (a bug in the
// schema, not the config).
func TestSchemaAcceptsRealConfigs(t *testing.T) {
	sch := compileSchema(t)

	fixtures := []string{"../demo/runwisp.toml"}
	goldens, err := filepath.Glob("../importer/testdata/*/*.golden.toml")
	require.NoError(t, err)
	fixtures = append(fixtures, goldens...)
	nested, err := filepath.Glob("../importer/testdata/*/*/*.golden.toml")
	require.NoError(t, err)
	fixtures = append(fixtures, nested...)

	for _, path := range fixtures {
		t.Run(path, func(t *testing.T) {
			instance := tomlFileAsJSONValue(t, path)
			require.NoError(t, sch.Validate(instance),
				"%s should validate against config.schema.json", path)
		})
	}
}

// tomlFileAsJSONValue decodes a TOML file and normalizes it to JSON-native Go
// types (map[string]any / []any / json.Number / string / bool) so the schema
// validator sees the same shape it would for a JSON document.
func tomlFileAsJSONValue(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var decoded any
	require.NoError(t, toml.Unmarshal(raw, &decoded), "decode %s", path)

	// Round-trip through JSON so TOML int64/float64/datetime collapse to the
	// number/string forms the schema validator expects.
	buf, err := json.Marshal(decoded)
	require.NoError(t, err)
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(buf))
	require.NoError(t, err)
	return instance
}

// collectSchemaPropertyNames walks the schema and gathers every key declared
// under a "properties" object, anywhere in the document (including $defs).
func collectSchemaPropertyNames(t *testing.T) map[string]struct{} {
	t.Helper()
	var doc any
	require.NoError(t, json.Unmarshal([]byte(SchemaJSON()), &doc))

	names := map[string]struct{}{}
	var walk func(v any)
	walk = func(v any) {
		switch node := v.(type) {
		case map[string]any:
			if props, ok := node["properties"].(map[string]any); ok {
				for name := range props {
					names[name] = struct{}{}
				}
			}
			for _, child := range node {
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(doc)
	return names
}

// collectTOMLTags recurses a wire struct type, gathering the name portion of
// every `toml:"..."` tag (skipping "-" and omitempty). It descends into nested
// structs, pointers, slices, and map values so embedded cores and repeatable
// blocks are covered.
func collectTOMLTags(rt reflect.Type, out map[string]struct{}, seen map[reflect.Type]bool) {
	for rt != nil && (rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Map) {
		rt = rt.Elem()
	}
	if rt == nil || rt.Kind() != reflect.Struct || seen[rt] {
		return
	}
	seen[rt] = true

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		tag := field.Tag.Get("toml")
		if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
			out[name] = struct{}{}
		}
		collectTOMLTags(field.Type, out, seen)
	}
}
