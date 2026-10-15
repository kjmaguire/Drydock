package runners

import "strings"

// Drive is a Dev Drive: a ReFS volume Windows marks as a developer volume. Trusted ones get Microsoft Defender's performance
// mode (files are scanned after they are opened rather than before).
type Drive struct {
	Letter  string `json:"letter"` // "D:"
	Trusted bool   `json:"trusted"`
}

// parseDevDrive reads `fsutil devdrv query X:`. It says "This is a trusted developer volume." (or an untrusted one) for a Dev
// Drive and "This is not a developer volume." otherwise.
func parseDevDrive(letter, out string) (Drive, bool) {
	o := strings.ToLower(out)
	if !strings.Contains(o, "developer volume") || strings.Contains(o, "not a developer volume") {
		return Drive{}, false
	}
	return Drive{Letter: letter, Trusted: strings.Contains(o, "trusted developer volume") && !strings.Contains(o, "untrusted developer volume")}, true
}
