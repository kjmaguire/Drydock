package core

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/runners"
)

// The usual code folder (B4): the saved parent of the last new project while it is a full path, else on Windows a Dev
// Drive's src folder (a trusted one first), else the fallback.
func TestCodeDirFrom(t *testing.T) {
	drives := []runners.Drive{{Letter: "E:"}, {Letter: `D:\`, Trusted: true}}
	saved := t.TempDir()
	for _, c := range []struct {
		saved   string
		drives  []runners.Drive
		windows bool
		want    string
	}{
		{saved, drives, true, saved},
		{"relative/code", drives, true, `D:\src`},
		{"", drives, true, `D:\src`},
		{"", []runners.Drive{{Letter: "E:"}}, true, `E:\src`},
		{"", nil, true, "fallback"},
		{"", drives, false, "fallback"},
	} {
		if got := codeDirFrom(c.saved, c.drives, c.windows, "fallback"); got != c.want {
			t.Errorf("%+v: got %q", c, got)
		}
	}
}
