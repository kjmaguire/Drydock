package backup

import (
	"path/filepath"
	"strings"
)

// syncFolderNames are directory names used by cloud-sync clients (matched case-insensitively as a whole segment or as a
// prefix such as "OneDrive - Contoso").
var syncFolderNames = []string{"onedrive", "dropbox", "google drive", "googledrive", "icloud", "iclouddrive", "pcloud", "nextcloud", "owncloud",
	"box sync", "sync.com", "mega", "tresorit", "syncthing"}

// LocationWarning returns a plain-language warning when path looks like a cloud-sync folder or a network location, and ""
// when it does not. It is a HEURISTIC on the path text (plus a drive-type check on Windows): a sync client in an unusual
// folder, or a mapped network drive on a non-Windows host, is not detected. A warning is advice, not a refusal: SQLite
// databases and WAL files on sync or network paths risk lock and corruption problems, and sync clients can upload backups
// that contain source code and room messages.
func LocationWarning(path string) string {
	p := strings.ReplaceAll(path, `\`, "/")
	if strings.HasPrefix(p, "//") {
		return "This looks like a network share. File locking over the network can corrupt a database in use, and a backup there is only as safe as that server. Prefer a local folder, and copy backups elsewhere yourself."
	}
	for _, seg := range strings.Split(strings.ToLower(p), "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		for _, name := range syncFolderNames {
			if seg == name || strings.HasPrefix(seg, name+" ") || strings.HasPrefix(seg, name+"-") {
				return "This looks like a cloud-sync folder (" + name + "). The sync client may upload your backups, which contain room messages and code excerpts, and may lock or conflict with files while they are written. Prefer a local folder that is not synced."
			}
		}
	}
	if isRemoteDrive(filepath.VolumeName(path)) {
		return "This drive is a network drive. File locking over the network can corrupt a database in use. Prefer a local folder."
	}
	return ""
}
