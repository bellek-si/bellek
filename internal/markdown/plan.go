package markdown

import (
	"regexp"
	"strings"

	"github.com/bellek-si/bellek/api"
)

type PlanTask struct {
	Key    string
	Title  string
	Status string
	Body   string
	Steps  int
	Done   int
}

const slugMaxLen = 40

var (
	reTaskHeading = regexp.MustCompile(`^### Task [\d.]+:\s*(.+?)\s*$`)
	reStep        = regexp.MustCompile(`^\s*- \[( |x|X)\]`)
	reSectionEnd  = regexp.MustCompile(`^#{1,3} `)
	reNonSlug     = regexp.MustCompile(`[^a-z0-9]+`)
)

func Slug(s string) string {
	out := strings.Trim(reNonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(out) > slugMaxLen {
		out = strings.TrimRight(out[:slugMaxLen], "-")
	}
	if out == "" {
		return "task"
	}
	return out
}

// IsPlan uses the same heading shape ParsePlan does, so a file it accepts
// never parses to zero tasks and leaves an import reporting nothing at all.
func IsPlan(b []byte) bool {
	text := string(b)
	if strings.HasPrefix(text, fence) {
		return false
	}
	if !strings.Contains(text, "- [ ]") && !strings.Contains(text, "- [x]") {
		return false
	}
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence && reTaskHeading.MatchString(line) {
			return true
		}
	}
	return false
}

func ParsePlan(b []byte) []PlanTask {
	lines := strings.Split(string(b), "\n")
	var tasks []PlanTask
	var cur *PlanTask
	var body []string
	inFence := false
	flush := func() {
		if cur == nil {
			return
		}
		cur.Body = strings.TrimSpace(strings.Join(body, "\n"))
		switch {
		case cur.Done == 0:
			cur.Status = string(api.TaskTodo)
		case cur.Done == cur.Steps:
			cur.Status = string(api.TaskDone)
		default:
			cur.Status = string(api.TaskInProgress)
		}
		tasks = append(tasks, *cur)
		cur, body = nil, nil
	}
	for _, line := range lines {
		isFenceLine := strings.HasPrefix(strings.TrimSpace(line), "```")
		if isFenceLine {
			inFence = !inFence
		}
		if isFenceLine || inFence {
			if cur != nil {
				body = append(body, line)
			}
			continue
		}
		if m := reTaskHeading.FindStringSubmatch(line); m != nil {
			flush()
			cur = &PlanTask{Key: Slug(m[1]), Title: m[1]}
			continue
		}
		if cur == nil {
			continue
		}
		if reSectionEnd.MatchString(line) {
			flush()
			continue
		}
		if m := reStep.FindStringSubmatch(line); m != nil {
			cur.Steps++
			if m[1] != " " {
				cur.Done++
			}
		}
		body = append(body, line)
	}
	flush()
	return tasks
}
