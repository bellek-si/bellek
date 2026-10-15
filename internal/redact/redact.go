package redact

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

type rule struct {
	re   *regexp.Regexp
	repl string
	keep func(name string) bool
}

var bellekKeys = map[string]bool{
	"taskkey": true, "projectkey": true, "parentkey": true, "relatedkey": true,
	"notekey": true, "focuskey": true, "turntaskkey": true, "nodekey": true,
}

func isBellekKey(name string) bool { return bellekKeys[strings.ToLower(name)] }

// The closing quote is optional so a value cut short is still caught.
const value = `(?:"(?:[^"\\\n]|\\.){8,}"?|'[^'\n]{8,}'?|[^\s"']{8,})`

// The paired forms run before the open ended ones, so a closed marker or a
// whole PEM block is consumed first and only a truncated one reaches the
// catch all. auth and pat are whole names only, because as parts of a name
// they would take PATH and author. Base64 wrapped at 64 or 76 columns is a
// blob when two full lines follow each other.
var rules = []rule{
	{re: regexp.MustCompile(`(?s)<private>.*?</private>`), repl: ""},
	{re: regexp.MustCompile(`(?s)<private>.*`), repl: "[redacted:private]"},
	{re: regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY(?: BLOCK)?-----.*?-----END [A-Z ]*PRIVATE KEY(?: BLOCK)?-----`), repl: "[redacted:pem]"},
	{re: regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY(?: BLOCK)?-----.*`), repl: "[redacted:pem]"},
	{re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), repl: "[redacted:jwt]"},
	{re: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`), repl: "[redacted:anthropic]"},
	{re: regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}`), repl: "[redacted:openai]"},
	{re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`), repl: "[redacted:github]"},
	{re: regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`), repl: "[redacted:aws]"},
	{re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`), repl: "[redacted:google]"},
	{re: regexp.MustCompile(`\b(?:xox[baprs]|xapp)-[A-Za-z0-9-]{10,}`), repl: "[redacted:slack]"},
	{re: regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}`), repl: "[redacted:sendgrid]"},
	{re: regexp.MustCompile(`\bhf_[A-Za-z0-9]{30,}`), repl: "[redacted:huggingface]"},
	{re: regexp.MustCompile(`\bdckr_pat_[A-Za-z0-9_-]{20,}`), repl: "[redacted:docker]"},
	{re: regexp.MustCompile(`\b[sr]k_(?:live|test)_[A-Za-z0-9]{16,}`), repl: "[redacted:stripe]"},
	{re: regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`), repl: "[redacted:gitlab]"},
	{re: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`), repl: "[redacted:npm]"},
	{re: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{16,}=*`), repl: "Bearer [redacted:bearer]"},
	{re: regexp.MustCompile(`(?i)\b((?:proxy-)?authorization["']?\s*[:=]\s*["']?(?:[A-Za-z]+\s+)?)[^\s"'\[]{8,}`), repl: "$1[redacted:auth]"},
	{re: regexp.MustCompile(`(?i)\b((?:set-)?cookie["']?\s*:\s*["']?)[^\s=;"']+=[^\r\n]*`), repl: "$1[redacted:cookie]"},
	{re: regexp.MustCompile(`(\s(?:-u|--user)[\s=]+['"]?[^\s:'"]*):[^\s'"]+`), repl: "$1:[redacted:user]"},
	{re: regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s/:@]*):[^\s@/]+@`), repl: "$1:[redacted:url]@"},
	{re: regexp.MustCompile(`\b([A-Za-z0-9_]*(?i:key|token|secret|password|passwd|credential|signature)[A-Za-z0-9_]*)(["']?\s*[=:]\s*)` + value), repl: "$1$2[redacted:env]", keep: isBellekKey},
	{re: regexp.MustCompile(`\b((?:[A-Za-z0-9]+_)*_?(?i:auth|pat))(["']?\s*[=:]\s*)` + value), repl: "$1$2[redacted:env]"},
	{re: regexp.MustCompile(`[A-Za-z0-9+/=]{200,}`), repl: "[blob]"},
	{re: regexp.MustCompile(`(?m)^[ \t]*[A-Za-z0-9+/]{60,}={0,2}(?:\r?\n[ \t]*[A-Za-z0-9+/]{60,}={0,2})+(?:\r?\n[ \t]*[A-Za-z0-9+/]+={0,2}$)?`), repl: "[blob]"},
}

func (r rule) apply(s string) string {
	if r.keep == nil {
		return r.re.ReplaceAllString(s, r.repl)
	}
	var out []byte
	last := 0
	for _, m := range r.re.FindAllStringSubmatchIndex(s, -1) {
		out = append(out, s[last:m[0]]...)
		if r.keep(s[m[2]:m[3]]) {
			out = append(out, s[m[0]:m[1]]...)
		} else {
			out = r.re.ExpandString(out, r.repl, s, m)
		}
		last = m[1]
	}
	return string(append(out, s[last:]...))
}

func Text(s string, max int) string {
	for _, r := range rules {
		s = r.apply(s)
	}
	if max > 0 && len(s) > max {
		for max > 0 && !utf8.RuneStart(s[max]) {
			max--
		}
		s = s[:max]
	}
	return s
}
