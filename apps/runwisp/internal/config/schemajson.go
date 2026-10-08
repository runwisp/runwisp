// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import _ "embed" // for //go:embed below

// SchemaURL is where the runwisp.toml JSON Schema is published; the embedded
// copy (SchemaJSON) is byte-identical to it.
const SchemaURL = "https://docs.runwisp.com/config.schema.json"

// SchemaDirective is the `#:schema` comment prepended to every runwisp.toml
// RunWisp generates, so TOML editors (taplo) validate against SchemaURL.
const SchemaDirective = "#:schema " + SchemaURL + "\n"

// schemaJSON is the runwisp.toml JSON Schema (draft 2020-12), embedded so
// `runwisp schema` works offline. TestSchemaCoversWireTags keeps it in sync with
// the wire structs in wire.go.
//
//go:embed config.schema.json
var schemaJSON string

// SchemaJSON returns the embedded runwisp.toml JSON Schema as a string.
func SchemaJSON() string {
	return schemaJSON
}
