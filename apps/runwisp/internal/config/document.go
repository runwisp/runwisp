// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// LoadDocument loads a config an attached app supplies as JSON instead of a
// runwisp.toml on disk. The document has the shape runwisp.toml decodes into,
// with the same keys. path stands in for the file it would be: relative paths
// (include, env_file, working_dir, ...) resolve against its directory, and
// errors name it.
//
// The JSON is rewritten as TOML and decoded by the same strict decoder as a
// file, like a crontab under include_cron, so every key, default, and
// validation rule is the one runwisp.toml gets. There is no second decoder to
// drift out of step.
func LoadDocument(path string, doc []byte) (*Config, error) {
	data, err := jsonToTOML(doc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config document: %w", err)
	}
	root, err := decodeWire(data, filepath.Dir(path), false)
	if err != nil {
		return nil, err
	}
	cfg, err := load(path, root)
	if err != nil {
		return nil, err
	}
	cfg.fromDocument = true
	return cfg, nil
}

// jsonToTOML rewrites a JSON object as TOML text. Integral numbers stay
// integers, so `"max_concurrent": 2` decodes into an int field like
// `max_concurrent = 2` does. TOML has no null, so a null is rejected by key
// path rather than silently dropped, and TOML rejects a key defined twice, so
// a duplicate JSON key is rejected rather than last-one-wins.
func jsonToTOML(doc []byte) ([]byte, error) {
	if err := rejectDuplicateKeys(json.NewDecoder(bytes.NewReader(doc)), nil); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var top any
	if err := dec.Decode(&top); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("unexpected data after the JSON object")
	}
	obj, ok := top.(map[string]any)
	if !ok {
		return nil, errors.New("the document must be a JSON object")
	}
	value, err := tomlValue(obj, nil)
	if err != nil {
		return nil, err
	}
	return toml.Marshal(value)
}

// tomlValue converts one decoded JSON value into what go-toml marshals the
// same way the TOML spelling of it would decode. path is the key path so far,
// for errors.
func tomlValue(v any, path []string) (any, error) {
	switch v := v.(type) {
	case nil:
		return nil, fmt.Errorf("%s: null is not a valid value; leave the key out instead", strings.Join(path, "."))
	case json.Number:
		if i, err := strconv.ParseInt(v.String(), 10, 64); err == nil {
			return i, nil
		}
		return v.Float64()
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			converted, err := tomlValue(item, append(path, k))
			if err != nil {
				return nil, err
			}
			out[k] = converted
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			converted, err := tomlValue(item, append(path, strconv.Itoa(i)))
			if err != nil {
				return nil, err
			}
			out[i] = converted
		}
		return out, nil
	default:
		return v, nil
	}
}

// rejectDuplicateKeys walks one JSON value and fails on an object that has a
// key twice, naming its key path. encoding/json keeps the last one silently.
func rejectDuplicateKeys(dec *json.Decoder, path []string) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch tok {
	case json.Delim('{'):
		if err := rejectDuplicateFields(dec, path); err != nil {
			return err
		}
	case json.Delim('['):
		for i := 0; dec.More(); i++ {
			if err := rejectDuplicateKeys(dec, append(slices.Clone(path), strconv.Itoa(i))); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	_, err = dec.Token() // the closing } or ]
	return err
}

// rejectDuplicateFields walks the fields of an object whose { was just read.
func rejectDuplicateFields(dec *json.Decoder, path []string) error {
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := tok.(string) // an object key is always a string
		keyPath := append(slices.Clone(path), key)
		if seen[key] {
			return fmt.Errorf("%s: duplicate key", strings.Join(keyPath, "."))
		}
		seen[key] = true
		if err := rejectDuplicateKeys(dec, keyPath); err != nil {
			return err
		}
	}
	return nil
}
