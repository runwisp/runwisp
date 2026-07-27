// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

// The blank import pulls in the embed package purely so the //go:embed directive
// on schemaJSON below is honoured by the compiler. schemaJSON is a string, not an
// embed.FS, so the package is never referenced by name and must be imported blank.
import _ "embed"

// SchemaURL is the canonical, stable URL where the runwisp.toml JSON Schema is
// published. It is what a `#:schema` directive in a generated runwisp.toml
// points at, so editors (Even Better TOML / taplo) can validate and complete
// the file. The embedded copy (SchemaJSON) is byte-identical to what is served
// there.
const SchemaURL = "https://docs.runwisp.com/config.schema.json"

// SchemaDirective is the `#:schema` line prepended to every runwisp.toml that
// RunWisp generates (first-run scaffold, `runwisp import`). Editors with TOML
// schema support (Even Better TOML / taplo) read it and validate + autocomplete
// the file against the published JSON Schema. It is a TOML comment, so it never
// affects parsing.
const SchemaDirective = "#:schema " + SchemaURL + "\n"

// schemaJSON is the JSON Schema (draft 2020-12) describing the full runwisp.toml
// surface. It is the machine-readable twin of the human docs and the dense agent
// reference, embedded so `runwisp schema` works fully offline. The wire structs
// in wire.go are the ground truth; TestSchemaCoversWireTags guards against drift.
//
//go:embed config.schema.json
var schemaJSON string

// SchemaJSON returns the embedded runwisp.toml JSON Schema as a string.
func SchemaJSON() string {
	return schemaJSON
}
