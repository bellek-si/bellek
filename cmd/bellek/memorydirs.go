package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const memoryIndex = "MEMORY.md"

var nonProjectChar = regexp.MustCompile(`[^A-Za-z0-9]`)

type memoryLocator struct {
	configDir string
	toplevel  func(cwd string) string
	extra     []string
	// settings is a closure rather than a sync.Once because memoryLocator is
	// copied by value.
	settings func() string
}

func (l memoryLocator) memoise() memoryLocator {
	l.settings = sync.OnceValue(l.readSettingsDir)
	return l
}

func claudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func claudeProjectName(path string) string {
	return nonProjectChar.ReplaceAllString(filepath.Clean(path), "-")
}

func (l memoryLocator) projectDir(cwd string) string {
	if cwd == "" {
		return ""
	}
	root := ""
	if l.toplevel != nil {
		root = l.toplevel(cwd)
	}
	if root == "" {
		root = cwd
	}
	return filepath.Join(l.configDir, "projects", claudeProjectName(root), "memory")
}

func (l memoryLocator) settingsDir() string {
	if l.settings != nil {
		return l.settings()
	}
	return l.readSettingsDir()
}

func (l memoryLocator) readSettingsDir() string {
	raw, err := os.ReadFile(filepath.Join(l.configDir, "settings.json"))
	if err != nil {
		return ""
	}
	var settings struct {
		AutoMemoryDirectory string `json:"autoMemoryDirectory"`
	}
	if json.Unmarshal(raw, &settings) != nil || settings.AutoMemoryDirectory == "" {
		return ""
	}
	dir := settings.AutoMemoryDirectory
	if strings.HasPrefix(dir, "~/") {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, dir[2:])
	}
	return filepath.Clean(dir)
}

func (l memoryLocator) configured() []string {
	return append([]string{l.settingsDir()}, l.extra...)
}

func (l memoryLocator) dirs(cwd string) []string {
	candidates := append([]string{l.projectDir(cwd)}, l.configured()...)
	seen := map[string]bool{}
	var out []string
	for _, d := range candidates {
		if d == "" {
			continue
		}
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

func (l memoryLocator) isMemoryFile(p string) bool {
	if !filepath.IsAbs(p) || filepath.Ext(p) != ".md" {
		return false
	}
	p = filepath.Clean(p)
	if filepath.Base(p) == memoryIndex {
		return false
	}
	parent := filepath.Dir(p)
	for _, d := range l.configured() {
		if d != "" && filepath.Clean(d) == parent {
			return true
		}
	}
	if filepath.Base(parent) != "memory" {
		return false
	}
	return filepath.Dir(filepath.Dir(parent)) == filepath.Join(l.configDir, "projects")
}

func memoryFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".md" && e.Name() != memoryIndex {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}
