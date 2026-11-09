package app

import (
	"errors"
	"strings"
)

// revealCmdLine is the command line that opens File Explorer at path's folder with the file selected ("Show in folder", A11).
// Explorer reads its own command line: the path goes in quotes after "/select," with no space, which Go's own argument quoting
// would not write. A path with a quote or a line break in it (no Windows file name has one) is refused.
func revealCmdLine(explorer, path string) (string, error) {
	if path == "" || strings.ContainsAny(path, "\"\x00\r\n") {
		return "", errors.New("not a file path")
	}
	return `"` + explorer + `" /select,"` + path + `"`, nil
}
