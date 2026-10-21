package runners

import (
	"errors"
	"strings"
	"testing"
)

// A volume is a Dev Drive when its persistent state carries the developer volume bit (ntifs.h); the trusted bit on top makes
// it a trusted one. Other bits do not matter.
func TestDevDriveFromFlags(t *testing.T) {
	if d, ok := devDriveFromFlags("D:", persistentVolumeStateDevVolume|persistentVolumeStateTrustedVolume); !ok || !d.Trusted || d.Letter != "D:" {
		t.Fatal(d, ok)
	}
	if d, ok := devDriveFromFlags("E:", persistentVolumeStateDevVolume|0x1); !ok || d.Trusted {
		t.Fatal(d, ok)
	}
	if _, ok := devDriveFromFlags("C:", 0); ok {
		t.Fatal("no bits is a Dev Drive")
	}
	if _, ok := devDriveFromFlags("C:", persistentVolumeStateTrustedVolume); ok {
		t.Fatal("the trusted bit alone is a Dev Drive")
	}
}

// When no Dev Drive is found the scan says why, in words the page can show after "No Dev Drive was found: ".
func TestDevDriveScanSaysWhyNothingWasFound(t *testing.T) {
	queryErr := errors.New("FSCTL_QUERY_PERSISTENT_VOLUME_STATE: The parameter is incorrect.")
	cases := []struct {
		name    string
		checks  []volumeCheck
		listErr error
		drives  int
		want    string
	}{
		{"found", []volumeCheck{{Letter: "D:", Drive: Drive{Letter: "D:", Trusted: true}, IsDev: true}, {Letter: "E:"}}, nil, 1, ""},
		{"found by fsutil", []volumeCheck{{Letter: "D:", Drive: Drive{Letter: "D:"}, IsDev: true, QueryErr: queryErr, Fsutil: "This is an untrusted developer volume."}}, nil, 1, ""},
		{"no drives listed", nil, errors.New("boom"), 0, "Windows did not list this PC's drives: boom"},
		{"no ReFS", nil, nil, 0, "no fixed drive on this PC uses ReFS, and a Dev Drive always does"},
		{"one plain ReFS", []volumeCheck{{Letter: "D:"}}, nil, 0, "D: uses ReFS but Windows does not mark it as a Dev Drive"},
		{"two plain ReFS", []volumeCheck{{Letter: "D:"}, {Letter: "E:"}}, nil, 0, "D: and E: use ReFS but Windows does not mark them as Dev Drives"},
		{"query failed, fsutil refused", []volumeCheck{{Letter: "D:", QueryErr: queryErr, Fsutil: "Error:  Access is denied."}}, nil, 0,
			`the Dev Drive check is not available on this Windows version (the volume state query for D: failed: FSCTL_QUERY_PERSISTENT_VOLUME_STATE: The parameter is incorrect.), and fsutil devdrv query, which needs administrator rights, said "Error:  Access is denied."`},
		{"query failed, fsutil silent", []volumeCheck{{Letter: "D:", QueryErr: queryErr}, {Letter: "E:", QueryErr: queryErr}, {Letter: "F:"}}, nil, 0,
			"the Dev Drive check is not available on this Windows version (the volume state query for D: and E: failed: FSCTL_QUERY_PERSISTENT_VOLUME_STATE: The parameter is incorrect.), and fsutil devdrv query, which needs administrator rights, said nothing"},
	}
	for _, c := range cases {
		s := devDriveScan(c.checks, c.listErr)
		if len(s.Drives) != c.drives || s.Reason != c.want {
			t.Errorf("%s: drives %v reason %q", c.name, s.Drives, s.Reason)
		}
		if c.drives == 0 && strings.HasSuffix(s.Reason, ".") {
			t.Errorf("%s: the reason ends the sentence itself: %q", c.name, s.Reason)
		}
	}
	if andList([]string{"D:", "E:", "F:"}) != "D:, E: and F:" || andList(nil) != "" {
		t.Fatal(andList([]string{"D:", "E:", "F:"}))
	}
	if firstLine("\r\n  Error:  Access is denied.\r\nmore") != "Error:  Access is denied." || firstLine("") != "" {
		t.Fatal(firstLine("\r\n  Error:  Access is denied.\r\nmore"))
	}
}
