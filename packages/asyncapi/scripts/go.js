// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Emits one Go type per components.schemas entry, read straight from the
// schema (format, nullable, required): no post-processing. Object $refs become
// pointers so absence stays detectable; enums are `type X string` so unknown
// values decode as-is; allOf $refs embed; bare objects are json.RawMessage.

const header = `// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol
`;

function pascal(name) {
    return name
        .replace(/(?:^|[^A-Za-z0-9]+)(\w)/g, (_, c) => c.toUpperCase())
        .replace(/(Id|Url)(?=[A-Z]|$)/g, (m) => m.toUpperCase());
}

function comment(text, indent = "") {
    if (!text) return "";
    const lines = text.trim().split("\n");
    return (
        lines.map((line) => `${indent}// ${line}`.trimEnd()).join("\n") + "\n"
    );
}

export function generateGo(document) {
    const schemas = document.components.schemas;
    const imports = new Set();
    const decls = [];
    const refName = (ref) => ref.split("/").pop();

    function emitEnum(name, schema) {
        const consts = schema.enum.map((value) => `${name}${pascal(value)}`);
        const lines = schema.enum.map(
            (value, i) => `\t${consts[i]} ${name} = ${JSON.stringify(value)}`,
        );
        decls.push(
            `${comment(schema.description)}type ${name} string\n\n` +
                `const (\n${lines.join("\n")}\n)\n\n` +
                `var ${name}Values = []${name}{${consts.join(", ")}}`,
        );
    }

    function goType(schema, propName) {
        if (schema.$ref) {
            const name = refName(schema.$ref);
            return schemas[name].enum ? name : `*${name}`;
        }
        if (schema.enum) {
            emitEnum(pascal(propName), schema);
            return pascal(propName);
        }
        const ptr = schema.nullable ? "*" : "";
        switch (schema.type) {
            case "string":
                if (schema.format !== "date-time") return `${ptr}string`;
                imports.add("time");
                return `${ptr}time.Time`;
            case "integer":
                return ptr + (schema.format === "int64" ? "int64" : "int");
            case "number":
                return `${ptr}float64`;
            case "boolean":
                return `${ptr}bool`;
            case "array":
                return `[]${goType(schema.items, propName).replace(/^\*/, "")}`;
            case "object":
                if (schema.additionalProperties) {
                    return `map[string]${goType(schema.additionalProperties, propName)}`;
                }
                if (schema.properties) {
                    throw new Error(
                        `move inline object ${propName} to components.schemas`,
                    );
                }
                imports.add("encoding/json");
                return "json.RawMessage";
        }
        throw new Error(
            `unsupported schema for ${propName}: ${JSON.stringify(schema)}`,
        );
    }

    function fields(schema) {
        if (schema.allOf) {
            return schema.allOf.flatMap((sub) =>
                sub.$ref ? [`\t${refName(sub.$ref)}`] : fields(sub),
            );
        }
        return Object.entries(schema.properties ?? {}).map(([key, prop]) => {
            const omit = schema.required?.includes(key) ? "" : ",omitzero";
            const tag = `\`json:"${key}${omit}"\``;
            return `${comment(prop.description, "\t")}\t${pascal(key)} ${goType(prop, key)} ${tag}`;
        });
    }

    for (const [name, schema] of Object.entries(schemas)) {
        if (schema.enum) emitEnum(name, schema);
        else
            decls.push(
                `${comment(schema.description)}type ${name} struct {\n${fields(schema).join("\n")}\n}`,
            );
    }

    const importLines = [...imports]
        .sort((a, b) => a.localeCompare(b))
        .map((path) => `\t"${path}"`);
    const importBlock = imports.size
        ? `\nimport (\n${importLines.join("\n")}\n)\n`
        : "";
    return `${header}${importBlock}\n${decls.join("\n\n")}\n`;
}
