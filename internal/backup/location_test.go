package backup_test

import (
	"testing"

	"github.com/kjmaguire/drydock/internal/backup"
)

func TestLocationWarning(t *testing.T) {
	for _, p := range []string{`C:\Users\kyle\OneDrive\Backups`, `C:\Users\kyle\OneDrive - Contoso\x`, `/home/kyle/Dropbox/drydock`, `D:\Google Drive\b`, `C:\Users\k\iCloudDrive\b`, `\\nas\share\drydock`, `//nas/share/x`} {
		if backup.LocationWarning(p) == "" {
			t.Errorf("no warning for %s", p)
		}
	}
	for _, p := range []string{`C:\Users\kyle\AppData\Local\Drydock\backups`, `/var/lib/drydock/backups`, `D:\Backups\Drydock`, `/home/kyle/dropboxer-notes`} {
		if w := backup.LocationWarning(p); w != "" {
			t.Errorf("unexpected warning for %s: %s", p, w)
		}
	}
}
