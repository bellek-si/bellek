package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestWorkflowStepsRunMakeTargets(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(repoRoot, ".github", "workflows", "*.y*ml"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var workflow struct {
			Jobs map[string]struct {
				Steps []struct {
					Run string `yaml:"run"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		require.NoError(t, yaml.Unmarshal(raw, &workflow), path)
		require.NotEmpty(t, workflow.Jobs, path)
		for job, spec := range workflow.Jobs {
			for _, step := range spec.Steps {
				if step.Run != "" {
					require.True(t, strings.HasPrefix(step.Run, "make "),
						"%s job %s runs %q: every CI step must be a make target so it runs locally too", filepath.Base(path), job, step.Run)
				}
			}
		}
	}
}
