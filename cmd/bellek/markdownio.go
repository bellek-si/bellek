package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bellek-si/bellek/api"
	"github.com/bellek-si/bellek/internal/markdown"
	"github.com/google/uuid"
)

func writeSnapshot(dir string, snap *api.Snapshot) (int, error) {
	byID := map[uuid.UUID]*api.Node{}
	for _, n := range snap.Nodes {
		byID[n.ID] = n
	}
	written := 0
	for _, n := range snap.Nodes {
		doc := markdown.Doc{
			Kind: string(n.Kind), Key: n.Key, Title: n.Title, Status: n.Status, Tags: n.Tags, Props: n.Props,
			Version: n.Version, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, Body: n.Body,
		}
		if n.Kind.Private() {
			doc.Visibility = string(n.Visibility)
		}
		for _, e := range snap.Edges[n.ID] {
			link := markdown.Link{Type: string(e.Type), To: e.ToKey}
			if target, ok := byID[e.ToID]; ok {
				link.To, link.Kind = target.Key, string(target.Kind)
			} else if target, ok := snap.Targets[e.ToID]; ok {
				link.To, link.Kind = target.Key, string(target.Kind)
			}
			if link.To == "" {
				continue
			}
			doc.Links = append(doc.Links, link)
		}
		var tl strings.Builder
		for _, ev := range snap.Events[n.ID] {
			fmt.Fprintf(&tl, "- %s %s %s\n", ev.CreatedAt.UTC().Format(time.RFC3339), ev.Type, ev.Message)
		}
		doc.Timeline = tl.String()
		b, err := markdown.Encode(doc)
		if err != nil {
			return written, err
		}
		// The kind and the key come from the server's answer, so neither may
		// name a path: a known kind is one plain word, and the key must stay
		// under it.
		if !n.Kind.Valid() {
			return written, fmt.Errorf("kind %q is not known", n.Kind)
		}
		kindDir := filepath.Join(dir, string(n.Kind)+"s")
		file := filepath.Join(kindDir, n.Key+".md")
		if rel, err := filepath.Rel(kindDir, file); err != nil || !filepath.IsLocal(rel) {
			return written, fmt.Errorf("key %q would write outside %s", n.Key, kindDir)
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			return written, err
		}
		if err := os.WriteFile(file, b, 0o600); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func readDocs(path string) ([]api.ImportDoc, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var files []string
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") && d.Name() != memoryIndex {
				files = append(files, p)
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	} else {
		files = []string{path}
	}
	var docs []api.ImportDoc
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		if markdown.IsPlan(b) {
			for _, t := range markdown.ParsePlan(b) {
				docs = append(docs, api.ImportDoc{Kind: "task", Key: t.Key, Title: t.Title, Status: t.Status, Body: t.Body})
			}
			continue
		}
		m, memErr := markdown.DecodeMemory(b)
		switch {
		case memErr == nil:
			n := memoryNote(uuid.Nil, m)
			docs = append(docs, api.ImportDoc{Kind: n.Kind, Key: n.Key, Title: n.Title, Body: n.Body, Tags: n.Tags, Props: n.Props})
			continue
		case !errors.Is(memErr, markdown.ErrNotMemory):
			return nil, fmt.Errorf("%s: %w", f, memErr)
		}
		d, err := markdown.Decode(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if d.Kind == "session" || d.Kind == "artifact" {
			continue
		}
		doc := api.ImportDoc{Kind: d.Kind, Key: d.Key, Title: d.Title, Status: d.Status, Body: d.Body, Tags: d.Tags, Props: d.Props, Visibility: d.Visibility}
		for _, l := range d.Links {
			doc.Links = append(doc.Links, api.ImportLink{Type: l.Type, To: l.To, Kind: l.Kind})
		}
		docs = append(docs, doc)
	}
	return docs, nil
}
