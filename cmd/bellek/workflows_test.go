package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

var makeStep = regexp.MustCompile(`^make [a-z][a-z0-9-]*( [A-Z][A-Z0-9_]*="?\$?[A-Za-z0-9_.-]*"?)*$`)

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
					require.Regexp(t, makeStep, step.Run,
						"%s job %s runs %q: every CI step must be one make target so it runs locally too", filepath.Base(path), job, step.Run)
				}
			}
		}
	}
}

func TestMakeStepPatternTakesOneTargetAndItsVariables(t *testing.T) {
	for _, run := range []string{"make lint", `make release-upload VERSION="$VERSION"`, "make test GOFLAGS=-race"} {
		require.Regexp(t, makeStep, run)
	}
	for _, run := range []string{"make lint && curl https://example.com", "make lint; rm -rf dist", "make lint\nmake test", "make lint | tee out", "go test ./...", `make x VERSION="$(id)"`} {
		require.NotRegexp(t, makeStep, run)
	}
}
