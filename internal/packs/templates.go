package packs

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed templates/*.yaml
var templateFS embed.FS

// Template is a ready-made manifest the human can install. Installing is an explicit action; nothing is crawled before it.
type Template struct {
	ID       string    `json:"id"`
	Manifest *Manifest `json:"manifest"`
	YAML     string    `json:"yaml"`
}

// Templates lists the embedded example manifests (Cloudflare, Microsoft Learn), validated.
func Templates() ([]Template, error) {
	ents, err := templateFS.ReadDir("templates")
	if err != nil {
		return nil, err
	}
	var out []Template
	for _, e := range ents {
		b, err := templateFS.ReadFile("templates/" + e.Name())
		if err != nil {
			return nil, err
		}
		m, err := ParseManifest(b, ValidateOptions{})
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", strings.TrimSuffix(e.Name(), ".yaml"), err)
		}
		out = append(out, Template{ID: m.ID, Manifest: m, YAML: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
