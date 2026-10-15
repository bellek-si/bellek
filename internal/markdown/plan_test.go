package markdown

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const samplePlan = `# Bellek Phase 9 Implementation Plan

**Goal:** Ship it.

---

### Task 1: Keyword lookup on the store

**Files:**
- Modify: internal/store/store.go

- [x] **Step 1: Add the contract test**
- [x] **Step 2: Implement**

### Task 2: Notes and link on write!

Some prose about notes.

- [x] **Step 1: Write tests**
- [ ] **Step 2: Implement**
- [ ] **Step 3: Ready for commit**

### Task 3: Docs

No steps yet.

### Task 4.2: Dotted numbering

Text with a fenced sample:

` + "```" + `
### Task 99: Not a real task
- [x] not a real step
` + "```" + `

- [ ] **Step 1: real step**

## Verification

- [ ] not a task step
`

func TestIsPlan(t *testing.T) {
	require.True(t, IsPlan([]byte(samplePlan)))
	require.False(t, IsPlan([]byte("---\nkind: task\n---\nbody")))
	require.False(t, IsPlan([]byte("# notes\n- [ ] no task heading")))
	require.False(t, IsPlan([]byte("---\nkind: task\n---\n\n"+samplePlan)), "front matter wins over plan detection")
}

func TestParsePlan(t *testing.T) {
	tasks := ParsePlan([]byte(samplePlan))
	require.Len(t, tasks, 4)
	require.Equal(t, "keyword-lookup-on-the-store", tasks[0].Key)
	require.Equal(t, "Keyword lookup on the store", tasks[0].Title)
	require.Equal(t, "done", tasks[0].Status)
	require.Equal(t, 2, tasks[0].Steps)
	require.Equal(t, 2, tasks[0].Done)
	require.Contains(t, tasks[0].Body, "internal/store/store.go")

	require.Equal(t, "notes-and-link-on-write", tasks[1].Key)
	require.Equal(t, "inProgress", tasks[1].Status)
	require.Equal(t, 3, tasks[1].Steps)
	require.Equal(t, 1, tasks[1].Done)
	require.Contains(t, tasks[1].Body, "Some prose about notes.")
	require.NotContains(t, tasks[1].Body, "Verification")

	require.Equal(t, "docs", tasks[2].Key)
	require.Equal(t, "todo", tasks[2].Status)
	require.Equal(t, 0, tasks[2].Steps)
	require.NotContains(t, tasks[2].Body, "not a task step")

	require.Equal(t, "dotted-numbering", tasks[3].Key)
	require.Equal(t, 1, tasks[3].Steps)
	require.Equal(t, "todo", tasks[3].Status)
	require.Contains(t, tasks[3].Body, "Not a real task")
}

func TestSlug(t *testing.T) {
	require.Equal(t, "auth-refresh", Slug("Auth  Refresh!"))
	require.Equal(t, "a", Slug("--a--"))
	require.Len(t, Slug(strings.Repeat("ab", 40)), 40)
	require.Equal(t, "task", Slug("!!!"))
}

func TestParsePlanHeadingWithoutStepsOrBody(t *testing.T) {
	plan := "# Plan\n\n### Task 1: Bare\n\n### Task 2: Has a step\n\n- [ ] **Step 1: a**\n"
	tasks := ParsePlan([]byte(plan))
	require.Len(t, tasks, 2)
	require.Equal(t, "bare", tasks[0].Key)
	require.Equal(t, "Bare", tasks[0].Title)
	require.Equal(t, "todo", tasks[0].Status)
	require.Equal(t, 0, tasks[0].Steps)
	require.Equal(t, 0, tasks[0].Done)
	require.Empty(t, tasks[0].Body)
	require.Equal(t, "has-a-step", tasks[1].Key)
}

func TestParsePlanCountsCapitalXSteps(t *testing.T) {
	plan := "# Plan\n\n### Task 1: Shouty\n\n- [X] **Step 1: a**\n- [X] **Step 2: b**\n\n### Task 2: Mixed\n\n- [X] **Step 1: a**\n- [ ] **Step 2: b**\n"
	tasks := ParsePlan([]byte(plan))
	require.Len(t, tasks, 2)
	require.Equal(t, 2, tasks[0].Steps)
	require.Equal(t, 2, tasks[0].Done)
	require.Equal(t, "done", tasks[0].Status)
	require.Equal(t, 1, tasks[1].Done)
	require.Equal(t, "inProgress", tasks[1].Status)
}

func TestIsPlanAcceptsWindowsLineEndings(t *testing.T) {
	plan := strings.ReplaceAll("# Plan\n\n### Task 1: Do it\n\n- [ ] **Step 1: a**\n", "\n", "\r\n")
	require.True(t, IsPlan([]byte(plan)))
	tasks := ParsePlan([]byte(plan))
	require.Len(t, tasks, 1)
	require.Equal(t, "do-it", tasks[0].Key)
	require.Equal(t, "Do it", tasks[0].Title)
	require.Equal(t, 1, tasks[0].Steps)
	require.Equal(t, 0, tasks[0].Done)
}

func TestSlugWithUnicode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"accents", "Café Résumé", "caf-r-sum"},
		{"turkish", "Şifre Güncelle", "ifre-g-ncelle"},
		{"emoji only", "🚀🚀", "task"},
		{"emoji with words", "Ship 🚀 it", "ship-it"},
		{"cjk", "日本語", "task"},
		{"digits survive", "Go 1.25 upgrade", "go-1-25-upgrade"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Slug(tt.in))
		})
	}
}

func TestIsPlanAgreesWithParsePlan(t *testing.T) {
	cases := []string{
		"# Plan\n\n### Task A: Do the thing\n\n- [ ] step\n",
		"# Plan\n\n### Task: Do the thing\n\n- [ ] step\n",
		"# Plan\n\n## Chapter\n\n- [ ] step\n",
		"# Notes\n\nA plan looks like this:\n\n```\n### Task 1: Ship it\n\n- [ ] step\n```\n",
	}
	for _, in := range cases {
		require.False(t, IsPlan([]byte(in)), in)
	}

	first := "### Task 1: Ship it\n\n- [ ] step\n"
	require.True(t, IsPlan([]byte(first)), "the heading may be the very first line")
	require.Len(t, ParsePlan([]byte(first)), 1)

	for _, in := range cases {
		require.Empty(t, ParsePlan([]byte(in)), "what IsPlan rejects would have parsed to nothing")
	}
}

func TestPlanTaskSectionEndsAtAnyHeading(t *testing.T) {
	plan := "# Plan\n\n### Task 1: Ship it\n\n- [ ] step\n\nfirst body\n\n# Appendix\n\nnot the task body\n"
	tasks := ParsePlan([]byte(plan))
	require.Len(t, tasks, 1)
	require.Contains(t, tasks[0].Body, "first body")
	require.NotContains(t, tasks[0].Body, "not the task body", "an h1 ends the section")
}
