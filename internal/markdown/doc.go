package markdown

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	fence          = "---\n"
	timelineMarker = "<!-- bellek:timeline -->\n"
)

type Link struct {
	Type string `yaml:"type"`
	To   string `yaml:"to"`
	Kind string `yaml:"kind,omitempty"`
}

type Doc struct {
	Kind       string
	Key        string
	Title      string
	Status     string
	Tags       []string
	Props      map[string]any
	Links      []Link
	Visibility string
	Version    int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Body       string
	Timeline   string
}

type frontMatter struct {
	Kind       string         `yaml:"kind"`
	Key        string         `yaml:"key"`
	Title      string         `yaml:"title"`
	Status     string         `yaml:"status,omitempty"`
	Tags       []string       `yaml:"tags,omitempty"`
	Props      map[string]any `yaml:"props,omitempty"`
	Links      []Link         `yaml:"links,omitempty"`
	Visibility string         `yaml:"visibility,omitempty"`
	Version    int            `yaml:"version,omitempty"`
	CreatedAt  string         `yaml:"createdAt,omitempty"`
	UpdatedAt  string         `yaml:"updatedAt,omitempty"`
}

func Encode(d Doc) ([]byte, error) {
	// Decode splits on the first marker anywhere in the body, so a body that
	// quotes it would come back truncated and the import would supersede the
	// node with the short version. Refusing is louder and loses nothing.
	if strings.Contains(d.Body, strings.TrimRight(timelineMarker, "\n")) {
		return nil, fmt.Errorf("%s %s: body contains the timeline marker", d.Kind, d.Key)
	}
	fm := frontMatter{Kind: d.Kind, Key: d.Key, Title: d.Title, Status: d.Status, Tags: d.Tags, Props: d.Props, Links: d.Links, Visibility: d.Visibility, Version: d.Version}
	if !d.CreatedAt.IsZero() {
		fm.CreatedAt = d.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !d.UpdatedAt.IsZero() {
		fm.UpdatedAt = d.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	head, err := yaml.Marshal(fm)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(fence)
	b.Write(head)
	b.WriteString(fence)
	b.WriteString("\n")
	if body := strings.TrimRight(d.Body, "\n"); body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}
	if d.Timeline != "" {
		b.WriteString("\n")
		b.WriteString(timelineMarker)
		b.WriteString(d.Timeline)
	}
	return b.Bytes(), nil
}

func splitFrontMatter(b []byte) (head, body string, err error) {
	text := string(b)
	if !strings.HasPrefix(text, fence) {
		return "", "", errors.New("missing front matter")
	}
	rest := text[len(fence):]
	end := strings.Index(rest, "\n"+fence)
	if end < 0 {
		return "", "", errors.New("unterminated front matter")
	}
	return rest[:end+1], strings.TrimPrefix(rest[end+1+len(fence):], "\n"), nil
}

func Decode(b []byte) (Doc, error) {
	head, body, err := splitFrontMatter(b)
	if err != nil {
		return Doc{}, err
	}
	var fm frontMatter
	if err := yaml.Unmarshal([]byte(head), &fm); err != nil {
		return Doc{}, err
	}
	d := Doc{Kind: fm.Kind, Key: fm.Key, Title: fm.Title, Status: fm.Status, Tags: fm.Tags, Props: fm.Props, Links: fm.Links, Visibility: fm.Visibility, Version: fm.Version}
	// readDocs does not carry either timestamp into an ImportDoc, so refusing
	// a hand written date would cost an import and buy nothing.
	if fm.CreatedAt != "" {
		d.CreatedAt, _ = time.Parse(time.RFC3339Nano, fm.CreatedAt)
	}
	if fm.UpdatedAt != "" {
		d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, fm.UpdatedAt)
	}
	if i := strings.Index(body, timelineMarker); i >= 0 {
		d.Timeline = body[i+len(timelineMarker):]
		body = body[:i]
	}
	d.Body = strings.TrimRight(body, "\n")
	return d, nil
}
