package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bellek-si/bellek/api"
	"github.com/stretchr/testify/require"
)

var repoRoot = filepath.Join("..", "..")

func readJSON(t *testing.T, path string, out any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, path))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, out), path)
}

func TestPluginHooksMatchTheSettingsExample(t *testing.T) {
	var plugin, example struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	readJSON(t, "plugin/hooks/hooks.json", &plugin)
	readJSON(t, "hooks/claude-code/settings.example.json", &example)
	require.Equal(t, example.Hooks, plugin.Hooks)
	require.Len(t, plugin.Hooks, 8)
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "PreCompact", "SessionEnd", "CwdChanged"} {
		require.Contains(t, plugin.Hooks, event)
		for _, group := range plugin.Hooks[event] {
			switch event {
			case "PreToolUse":
				require.Equal(t, "Bash", group.Matcher, "only a Bash call takes a snapshot")
			case "PostToolUse":
				require.Equal(t, strings.Join(append(slices.Clone(editorTools), "Bash"), "|"), group.Matcher,
					"the tools the hook is called for are the tools touchedPaths reads, and Bash")
			}
			for _, h := range group.Hooks {
				require.Equal(t, "command", h.Type)
				require.True(t, strings.HasPrefix(h.Command, "bellek hook "), h.Command)
				require.Contains(t, hookEvents, strings.TrimPrefix(h.Command, "bellek hook "))
				require.Equal(t, 5, h.Timeout)
			}
		}
	}
}

func TestPluginManifestAndServer(t *testing.T) {
	var manifest struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
	}
	readJSON(t, "plugin/.claude-plugin/plugin.json", &manifest)
	require.Equal(t, "bellek", manifest.Name)
	require.NotEmpty(t, manifest.Description)
	require.NotEmpty(t, manifest.Version)

	var servers struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	readJSON(t, "plugin/.mcp.json", &servers)
	require.Equal(t, "bellek", servers.MCPServers["bellek"].Command)
	require.Equal(t, []string{"mcp"}, servers.MCPServers["bellek"].Args)

	var marketplace struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	readJSON(t, ".claude-plugin/marketplace.json", &marketplace)
	require.Equal(t, "bellek", marketplace.Name)
	require.Len(t, marketplace.Plugins, 1)
	require.Equal(t, "./plugin", marketplace.Plugins[0].Source)
}

func TestPluginSkillDescribesWhenToReadAndWrite(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "plugin", "skills", "bellek", "SKILL.md"))
	require.NoError(t, err)
	body := string(raw)
	require.True(t, strings.HasPrefix(body, "---\nname: bellek\ndescription: "), "the front matter names the skill and describes it")
	head, _, ok := strings.Cut(strings.TrimPrefix(body, "---\n"), "\n---\n")
	require.True(t, ok)
	description := strings.TrimPrefix(strings.SplitN(head, "\n", 2)[1], "description: ")
	require.Less(t, len(description), 1536, "Claude Code truncates longer descriptions")
	for _, word := range []string{"before touching", "record"} {
		require.Contains(t, description, word)
	}
	require.Contains(t, body, "before your first edit, even for a small one line request", "the skill says when to create the task")
	require.Contains(t, body, "tagged `report` with key `<task key>-report`", "the skill says how to write the report note")
	for _, tool := range api.Tools {
		require.Contains(t, body, tool.Name)
	}
}
