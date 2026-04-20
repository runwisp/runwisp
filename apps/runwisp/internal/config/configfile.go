// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"fmt"
	"os"

	"github.com/runwisp/runwisp/internal/model"
	"gopkg.in/yaml.v3"
)

// ReadDocument reads a YAML config file as a yaml.Node tree, preserving
// comments and formatting for round-trip editing.
func ReadDocument(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return &doc, nil
}

// WriteDocument marshals a yaml.Node tree back to file with 2-space indent.
func WriteDocument(path string, doc *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("failed to encode YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// TaskNamesFromDocument returns task names from the document in declaration order.
func TaskNamesFromDocument(doc *yaml.Node) []string {
	tasks := findTasksNode(doc)
	if tasks == nil || tasks.Kind != yaml.MappingNode {
		return nil
	}
	names := make([]string, 0, len(tasks.Content)/2)
	for i := 0; i < len(tasks.Content); i += 2 {
		names = append(names, tasks.Content[i].Value)
	}
	return names
}

// AddTaskToDocument appends a new task to the tasks mapping in the document.
func AddTaskToDocument(doc *yaml.Node, name string, task model.Task) error {
	tasks := ensureTasksNode(doc)
	if tasks == nil {
		return fmt.Errorf("could not find or create tasks section")
	}
	for i := 0; i < len(tasks.Content); i += 2 {
		if tasks.Content[i].Value == name {
			return fmt.Errorf("task %q already exists", name)
		}
	}
	taskNode, err := marshalTaskNode(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: name, Tag: "!!str"}
	tasks.Content = append(tasks.Content, keyNode, taskNode)
	tasks.Style = 0
	return nil
}

// UpdateTaskInDocument replaces a task definition, optionally renaming the key.
func UpdateTaskInDocument(doc *yaml.Node, oldName, newName string, task model.Task) error {
	tasks := findTasksNode(doc)
	if tasks == nil || tasks.Kind != yaml.MappingNode {
		return fmt.Errorf("tasks section not found")
	}
	if oldName != newName {
		for i := 0; i < len(tasks.Content); i += 2 {
			if tasks.Content[i].Value == newName {
				return fmt.Errorf("task %q already exists", newName)
			}
		}
	}
	for i := 0; i < len(tasks.Content); i += 2 {
		if tasks.Content[i].Value == oldName {
			taskNode, err := marshalTaskNode(task)
			if err != nil {
				return fmt.Errorf("failed to marshal task: %w", err)
			}
			tasks.Content[i].Value = newName
			tasks.Content[i+1] = taskNode
			return nil
		}
	}
	return fmt.Errorf("task %q not found", oldName)
}

// LoadRaw loads the config file without applying defaults or running validation.
func LoadRaw(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var parsed fileConfig
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	return parsed.intoConfig(), nil
}

// EnsureConfigFile reads the config document, creating a minimal file if it
// does not exist. Returns the document and whether the file was newly created.
func EnsureConfigFile(path string) (*yaml.Node, bool, error) {
	doc, err := ReadDocument(path)
	if err == nil {
		return doc, false, nil
	}
	if !os.IsNotExist(err) {
		return nil, false, err
	}
	const minimalConfig = "# RunWisp configuration\n# Docs: https://github.com/runwisp/runwisp\n\ntasks: {}\n"
	if err := os.WriteFile(path, []byte(minimalConfig), 0644); err != nil {
		return nil, false, fmt.Errorf("failed to create %s: %w", path, err)
	}
	doc, err = ReadDocument(path)
	if err != nil {
		return nil, false, err
	}
	return doc, true, nil
}

func findTasksNode(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "tasks" {
			return root.Content[i+1]
		}
	}
	return nil
}

func ensureTasksNode(doc *yaml.Node) *yaml.Node {
	if n := findTasksNode(doc); n != nil {
		return n
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: "tasks", Tag: "!!str"}
	valueNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	root.Content = append(root.Content, keyNode, valueNode)
	return valueNode
}

func marshalTaskNode(task model.Task) (*yaml.Node, error) {
	data, err := yaml.Marshal(task)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0], nil
	}
	return nil, fmt.Errorf("unexpected YAML node structure")
}
