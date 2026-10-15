package markdown

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Memory struct {
	Name            string
	Description     string
	Type            string
	Scope           string
	OriginSessionID string
	Modified        time.Time
	Body            string
}

type memoryFrontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Metadata    struct {
		Type            string `yaml:"type"`
		Scope           string `yaml:"scope"`
		OriginSessionID string `yaml:"originSessionId"`
		Modified        string `yaml:"modified"`
	} `yaml:"metadata"`
}

const (
	ScopeProject = "project"
	ScopeGlobal  = "global"
)

var ErrNotMemory = errors.New("not a memory file")

func DecodeMemory(b []byte) (Memory, error) {
	head, body, err := splitFrontMatter(b)
	if err != nil {
		return Memory{}, fmt.Errorf("%w: %s", ErrNotMemory, err)
	}
	var fm memoryFrontMatter
	if err := yaml.Unmarshal([]byte(head), &fm); err != nil {
		return Memory{}, fmt.Errorf("%w: %s", ErrNotMemory, err)
	}
	if fm.Name == "" {
		return Memory{}, fmt.Errorf("%w: no name", ErrNotMemory)
	}
	if fm.Metadata.Type == "" {
		return Memory{}, fmt.Errorf("%w: no metadata type", ErrNotMemory)
	}
	m := Memory{Name: fm.Name, Description: fm.Description, Type: fm.Metadata.Type, OriginSessionID: fm.Metadata.OriginSessionID, Body: strings.TrimRight(body, "\n")}
	if fm.Metadata.Scope == ScopeProject || fm.Metadata.Scope == ScopeGlobal {
		m.Scope = fm.Metadata.Scope
	}
	if fm.Metadata.Modified != "" {
		// A zero Modified is never After the marker, so the memory would be
		// pinned at whatever it said when it was first seen and never sent
		// again. That is worse than saying the file is wrong.
		if m.Modified, err = time.Parse(time.RFC3339Nano, fm.Metadata.Modified); err != nil {
			return Memory{}, fmt.Errorf("modified: %w", err)
		}
	}
	return m, nil
}
