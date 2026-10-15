package api

import (
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// RemoteWithoutCredentials drops what a clone put in the url to authenticate:
// all the user info of an http or https url, where the user name alone is often
// a token, and the password of any other. It reads the url itself, because git
// takes a password with characters net/url refuses, and a refused url must not
// pass with the password in it.
func RemoteWithoutCredentials(remote string) string {
	scheme, rest, ok := strings.Cut(remote, "://")
	if !ok {
		return remote
	}
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	at := strings.LastIndex(rest[:end], "@")
	if at < 0 {
		return remote
	}
	user, _, hasPassword := strings.Cut(rest[:at], ":")
	switch {
	case strings.EqualFold(scheme, "http"), strings.EqualFold(scheme, "https"):
		return scheme + "://" + rest[at+1:]
	case hasPassword:
		return scheme + "://" + user + "@" + rest[at+1:]
	}
	return remote
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
