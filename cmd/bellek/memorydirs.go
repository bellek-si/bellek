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

// memoryLocator finds Claude Code auto memory directories: one per project under
// <config dir>/projects/<name>/memory, plus autoMemoryDirectory from settings.json
// and any directories given by BELLEK_MEMORY_DIRS.
type memoryLocator struct {
	configDir string
	toplevel  func(cwd string) string
	extra     []string
	// settings memoises readSettingsDir for the life of a hook run. It is a
	// closure rather than a sync.Once because this struct is copied by value.
	settings func() string
}

// isMemoryFile asks for the configured directories on every candidate path,
// and nothing changes the file inside one hook run.
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

// claudeProjectName is the folder Claude Code derives from a repository path:
// every character outside letters and digits becomes a dash.
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

// configured lists the memory directories that do not depend on cwd.
func (l memoryLocator) configured() []string {
	return append([]string{l.settingsDir()}, l.extra...)
}

// dirs lists the memory directories that exist for cwd, the project one first.
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

// isMemoryFile reports whether p is a memory fact file: a markdown file other than
// the index, inside a project memory directory or one of the configured ones.
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

// memoryFiles lists the fact files in dir, sorted, without the index.
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
