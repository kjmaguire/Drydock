// Command drydock-tsgen writes the UI's TypeScript types that are generated from Go types (decision P87): today the agent
// activity vocabulary (internal/agui) to ui/app/api/agui.gen.ts. Run it from the repository root after changing those types;
// a test in internal/agui fails while the file is out of date.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kjmaguire/drydock/internal/agui"
)

func main() {
	src, err := agui.TypeScript()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.FromSlash(agui.TSPath), []byte(src), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", agui.TSPath)
}
