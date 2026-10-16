package manifests

import (
	"embed"
	"io/fs"
)

//go:embed cli/*.yaml compat.yaml
var embedded embed.FS

// Embedded returns the manifests shipped with Drydock.
func Embedded() (*Set, error) { return Load(embedded, "cli") }

// EmbeddedFS exposes the embedded files (cli/*.yaml and compat.yaml).
func EmbeddedFS() fs.FS { return embedded }
