package runners

import (
	"fmt"
	"strings"
)

// Drive is a Dev Drive: a ReFS volume Windows marks as a developer volume. Trusted ones get Microsoft Defender's performance
// mode (files are scanned after they are opened rather than before).
type Drive struct {
	Letter  string `json:"letter"` // "D:"
	Trusted bool   `json:"trusted"`
}

// DevDriveScan is what a look for Dev Drives found: the drives, and when there are none on Windows, why in a few words (a
// clause that follows "No Dev Drive was found: "). Off Windows both are empty.
type DevDriveScan struct {
	Drives []Drive
	Reason string
}

// The persistent volume state flags Windows keeps for a Dev Drive (ntifs.h, FILE_FS_PERSISTENT_VOLUME_INFORMATION.VolumeFlags,
// answered by FSCTL_QUERY_PERSISTENT_VOLUME_STATE). `fsutil devdrv query` reads the same two bits, but fsutil needs
// administrator rights and Drydock runs as the signed-in person, so Drydock asks the volume itself.
const (
	persistentVolumeStateDevVolume     uint32 = 0x00002000 // PERSISTENT_VOLUME_STATE_DEV_VOLUME: a developer volume
	persistentVolumeStateTrustedVolume uint32 = 0x00004000 // PERSISTENT_VOLUME_STATE_TRUSTED_VOLUME: a trusted one
)

// devDriveFromFlags decides from a volume's state flags: it is a Dev Drive when the developer volume bit is set, trusted when
// the trusted bit is set too.
func devDriveFromFlags(letter string, flags uint32) (Drive, bool) {
	if flags&persistentVolumeStateDevVolume == 0 {
		return Drive{}, false
	}
	return Drive{Letter: letter, Trusted: flags&persistentVolumeStateTrustedVolume != 0}, true
}

// parseDevDrive reads `fsutil devdrv query X:` (the fallback when the volume state query fails). It says "This is a trusted
// developer volume." (or an untrusted one) for a Dev Drive and "This is not a developer volume." otherwise; without
// administrator rights it prints an error instead.
func parseDevDrive(letter, out string) (Drive, bool) {
	o := strings.ToLower(out)
	if !strings.Contains(o, "developer volume") || strings.Contains(o, "not a developer volume") {
		return Drive{}, false
	}
	return Drive{Letter: letter, Trusted: strings.Contains(o, "trusted developer volume") && !strings.Contains(o, "untrusted developer volume")}, true
}

// volumeCheck is what the Dev Drive check learnt about one fixed ReFS volume.
type volumeCheck struct {
	Letter   string
	Drive    Drive
	IsDev    bool
	QueryErr error  // the volume state query failed: fsutil was asked instead
	Fsutil   string // what fsutil answered then (its first line)
}

// devDriveScan sums up the volume checks: the Dev Drives found, or why none was (one clause per situation, so the page can
// say it instead of a bare "no Dev Drive was found").
func devDriveScan(checks []volumeCheck, listErr error) DevDriveScan {
	var s DevDriveScan
	for _, c := range checks {
		if c.IsDev {
			s.Drives = append(s.Drives, c.Drive)
		}
	}
	if len(s.Drives) > 0 {
		return s
	}
	switch {
	case listErr != nil:
		s.Reason = fmt.Sprintf("Windows did not list this PC's drives: %v", listErr)
	case len(checks) == 0:
		s.Reason = "no fixed drive on this PC uses ReFS, and a Dev Drive always does"
	default:
		var plain, failed []string
		var firstErr, firstFsutil string
		for _, c := range checks {
			if c.QueryErr == nil {
				plain = append(plain, c.Letter)
				continue
			}
			failed = append(failed, c.Letter)
			if firstErr == "" {
				firstErr = c.QueryErr.Error()
				firstFsutil = c.Fsutil
			}
		}
		if len(failed) == 0 {
			if len(plain) == 1 {
				s.Reason = fmt.Sprintf("%s uses ReFS but Windows does not mark it as a Dev Drive", plain[0])
			} else {
				s.Reason = fmt.Sprintf("%s use ReFS but Windows does not mark them as Dev Drives", andList(plain))
			}
			break
		}
		said := "said nothing"
		if firstFsutil != "" {
			said = fmt.Sprintf("said %q", firstFsutil)
		}
		s.Reason = fmt.Sprintf("the Dev Drive check is not available on this Windows version (the volume state query for %s failed: %s), and fsutil devdrv query, which needs administrator rights, %s",
			andList(failed), firstErr, said)
	}
	return s
}

// andList joins "D:", "E:", "F:" as "D:, E: and F:".
func andList(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// firstLine is the first non-empty line of a program's output, trimmed.
func firstLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
