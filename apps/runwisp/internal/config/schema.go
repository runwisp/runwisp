// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"strings"

	"github.com/runwisp/runwisp/internal/model"
	"gopkg.in/yaml.v3"
)

type fileConfig struct {
	Daemon   Daemon          `yaml:"daemon,omitempty"`
	Storage  Storage         `yaml:"storage,omitempty"`
	Defaults Defaults        `yaml:"defaults,omitempty"`
	Tasks    taskDefinitions `yaml:"tasks"`
}

func (c *fileConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawFileConfig fileConfig
	var raw rawFileConfig
	if err := model.ValidateMappingKeys(value, "daemon", "storage", "defaults", "tasks"); err != nil {
		return err
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*c = fileConfig(raw)
	return nil
}

func (c fileConfig) intoConfig() *Config {
	return &Config{
		Tasks:    []model.Task(c.Tasks),
		Defaults: c.Defaults,
		Storage:  c.Storage,
		Daemon:   c.Daemon,
	}
}

type taskDefinitions []model.Task

func (d *taskDefinitions) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == 0 || value.Tag == "!!null" {
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("tasks must be a mapping of task names to task definitions")
	}

	tasks := make([]model.Task, 0, len(value.Content)/2)
	seen := make(map[string]struct{}, len(value.Content)/2)
	for i := 0; i < len(value.Content); i += 2 {
		name := strings.TrimSpace(value.Content[i].Value)
		if name == "" {
			return fmt.Errorf("task name is required")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate task name: %s", name)
		}
		seen[name] = struct{}{}

		var task model.Task
		if err := value.Content[i+1].Decode(&task); err != nil {
			return err
		}
		task.Name = name
		tasks = append(tasks, task)
	}

	*d = tasks
	return nil
}
