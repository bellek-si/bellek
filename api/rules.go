package api

import (
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// RepoAddress names the repository a git remote points at as host/owner/repo,
// lowercased, so every clone url of one repository gives the same address. It
// drops the scheme, user info, port, query and fragment, which is where a clone
// keeps its credentials. A remote on the local disk names no repository and gives
// "", and so does one with a control character or an IPv6 host.
func RepoAddress(remote string) string {
	r := strings.TrimSpace(remote)
	if strings.ContainsFunc(r, unicode.IsControl) {
		return ""
	}
	scp := scpHost(r)
	var host, rest string
	switch {
	case strings.Contains(r, "://"):
		scheme, after, _ := strings.Cut(r, "://")
		if strings.EqualFold(scheme, "file") {
			return ""
		}
		if i := strings.IndexAny(after, "?#"); i >= 0 {
			after = after[:i]
		}
		host, rest, _ = strings.Cut(after, "/")
		if i := strings.LastIndex(host, "@"); i >= 0 {
			host = host[i+1:]
		}
		host, _, _ = strings.Cut(host, ":")
	case scp != "":
		host = scp
		_, rest, _ = strings.Cut(r, ":")
	default:
		host, rest, _ = strings.Cut(r, "/")
		if !strings.Contains(host, ".") || strings.HasPrefix(host, ".") || strings.Contains(host, ":") {
			return ""
		}
	}
	rest = strings.Trim(rest, "/")
	rest = strings.Trim(strings.TrimSuffix(rest, ".git"), "/")
	if host == "" || rest == "" || strings.ContainsAny(host, "[]") {
		return ""
	}
	return strings.ToLower(host + "/" + rest)
}

// scpHost is the host of git's short ssh form, user@host:path, which has no
// slash before its first colon. A one letter host is a Windows drive.
func scpHost(r string) string {
	before, _, ok := strings.Cut(r, ":")
	if !ok || strings.ContainsAny(before, `/\`) {
		return ""
	}
	if _, h, found := strings.Cut(before, "@"); found {
		before = h
	}
	if len(before) < 2 {
		return ""
	}
	return before
}

// CanonicalUUID returns s in canonical form when it is a uuid written with
// dashes and nothing around it, the only form claude --resume takes.
func CanonicalUUID(s string) (string, bool) {
	u, err := uuid.Parse(s)
	if err != nil || len(s) != 36 {
		return "", false
	}
	return u.String(), true
}

// OneLine keeps a stored value on the line it is written to, so it cannot open
// a section of its own in the context of the agent that reads it.
func OneLine(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, s))
}
