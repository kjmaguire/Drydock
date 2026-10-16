package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/backup"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Scheduled backups (docs/packaging.md section 8). The scheduler runs inside the app (nothing runs while Drydock is closed):
// daily when idle, keeping 7 daily and 4 weekly backups. A restore cannot replace a database that is open, so it is STAGED:
// backup.restore writes <data>/restore.pending and the next start applies it before the database is opened.

const (
	keyBackupEnabled = "backup.enabled"
	keyBackupDir     = "backup.dir"
	keyBackupDaily   = "backup.daily"
	keyBackupWeekly  = "backup.weekly"
	keyBackupLastOK  = "backup.lastOk"
	keyBackupLastErr = "backup.lastError"

	restorePendingFile = "restore.pending"
	restoreResultFile  = "restore.result.json"
	restoreFailedFile  = "restore.failed.json"
)

// BackupSettings is backup.settings.get / backup.settings.set.
type BackupSettings struct {
	Enabled     bool   `json:"enabled"`
	Dir         string `json:"dir"`
	DefaultDir  string `json:"defaultDir"`
	Daily       int    `json:"daily"`
	Weekly      int    `json:"weekly"`
	LastSuccess int64  `json:"lastSuccess,omitempty"` // ms
	LastError   string `json:"lastError,omitempty"`
	// Warning is the sync-folder or network-location warning for Dir; "" when none applies.
	Warning string `json:"warning,omitempty"`
}

func (p *packaging) defaultBackupDir() string { return filepath.Join(p.c.cfg.DataDir, "backups") }

func (p *packaging) backupSettings(ctx context.Context) BackupSettings {
	s := BackupSettings{Enabled: true, DefaultDir: p.defaultBackupDir(), Daily: 7, Weekly: 4}
	s.Dir = s.DefaultDir
	if v, ok := p.getSetting(ctx, keyBackupEnabled); ok {
		s.Enabled = v != "0"
	}
	if v, ok := p.getSetting(ctx, keyBackupDir); ok && v != "" {
		s.Dir = v
	}
	if v, ok := p.getSetting(ctx, keyBackupDaily); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.Daily = n
		}
	}
	if v, ok := p.getSetting(ctx, keyBackupWeekly); ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			s.Weekly = n
		}
	}
	if v, ok := p.getSetting(ctx, keyBackupLastOK); ok {
		s.LastSuccess, _ = strconv.ParseInt(v, 10, 64)
	}
	s.LastError, _ = p.getSetting(ctx, keyBackupLastErr)
	s.Warning = backup.LocationWarning(s.Dir)
	return s
}

type backupSettingsReq struct {
	Enabled *bool   `json:"enabled"`
	Dir     *string `json:"dir"`
	Daily   *int    `json:"daily"`
	Weekly  *int    `json:"weekly"`
}

func (p *packaging) setBackupSettings(ctx context.Context, r backupSettingsReq) (BackupSettings, error) {
	if r.Daily != nil && (*r.Daily < 1 || *r.Daily > 365) {
		return BackupSettings{}, errors.New("keep between 1 and 365 daily backups")
	}
	if r.Weekly != nil && (*r.Weekly < 0 || *r.Weekly > 520) {
		return BackupSettings{}, errors.New("keep between 0 and 520 weekly backups")
	}
	if r.Dir != nil {
		dir := strings.TrimSpace(*r.Dir)
		if dir == "" { // back to the default
			if err := p.delSetting(ctx, keyBackupDir); err != nil {
				return BackupSettings{}, err
			}
		} else {
			if !filepath.IsAbs(dir) {
				return BackupSettings{}, errors.New("the backup folder must be an absolute path")
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return BackupSettings{}, fmt.Errorf("the backup folder cannot be created: %w", err)
			}
			probe := filepath.Join(dir, ".drydock-write-test")
			if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
				return BackupSettings{}, fmt.Errorf("the backup folder is not writable: %w", err)
			}
			_ = os.Remove(probe)
			if err := p.setSetting(ctx, keyBackupDir, dir); err != nil {
				return BackupSettings{}, err
			}
		}
	}
	if r.Enabled != nil {
		v := "1"
		if !*r.Enabled {
			v = "0"
		}
		if err := p.setSetting(ctx, keyBackupEnabled, v); err != nil {
			return BackupSettings{}, err
		}
	}
	if r.Daily != nil {
		if err := p.setSetting(ctx, keyBackupDaily, strconv.Itoa(*r.Daily)); err != nil {
			return BackupSettings{}, err
		}
	}
	if r.Weekly != nil {
		if err := p.setSetting(ctx, keyBackupWeekly, strconv.Itoa(*r.Weekly)); err != nil {
			return BackupSettings{}, err
		}
	}
	p.audit(ctx, "backup.settings", "", map[string]any{"enabled": r.Enabled, "dir": r.Dir, "daily": r.Daily, "weekly": r.Weekly})
	return p.backupSettings(ctx), nil
}

// BackupResult is what backup.now returns.
type BackupResult struct {
	Backup  backup.Info `json:"backup"`
	Pruned  []string    `json:"pruned"`
	Warning string      `json:"warning,omitempty"`
}

// runBackup takes one verified backup into the configured folder, prunes, and records the outcome. A failure is never silent:
// it is recorded, published as backup.failed, and posted to every project as a SYSTEM message and a Needs-you item.
func (p *packaging) runBackup(ctx context.Context, label string) (BackupResult, error) {
	p.backupMu.Lock()
	defer p.backupMu.Unlock()
	s := p.backupSettings(ctx)
	res := BackupResult{Warning: s.Warning}
	info, err := backup.Create(ctx, p.c.DB, s.Dir, label, p.bnow())
	if err == nil {
		res.Backup = info
		res.Pruned, err = backup.Prune(s.Dir, s.Daily, s.Weekly)
		if err != nil { // the backup itself succeeded; pruning trouble is reported but the backup stands
			p.c.logf("backup prune: %v", err)
			err = nil
		}
	}
	if err != nil {
		p.backupFailed(ctx, s.Dir, label, err)
		return BackupResult{}, err
	}
	_ = p.setSetting(ctx, keyBackupLastOK, strconv.FormatInt(p.bnow().UnixMilli(), 10))
	_ = p.delSetting(ctx, keyBackupLastErr)
	p.mu.Lock()
	p.lastFail = time.Time{}
	p.mu.Unlock()
	p.c.publish(ctx, "backup.completed", "", map[string]any{"name": info.Name, "bytes": info.Size, "label": label})
	return res, nil
}

func (p *packaging) backupFailed(ctx context.Context, dir, label string, err error) {
	msg := err.Error()
	if p.c.Scanner != nil {
		msg = p.c.Scanner.Redact(msg)
	}
	msg = p.identity.Apply(msg)
	_ = p.setSetting(ctx, keyBackupLastErr, msg)
	failedAt := p.bnow()
	p.mu.Lock()
	p.lastFail = failedAt
	p.mu.Unlock()
	p.c.logf("backup failed: %s", msg)
	p.c.publish(ctx, "backup.failed", "", map[string]any{"error": msg, "dir": p.identity.Apply(dir)})
	day := p.bnow().UTC().Format("2006-01-02")
	kind, which := "manual", "The backup you started"
	if label == "scheduled" {
		kind, which = "scheduled", "The scheduled backup"
	}
	p.notify(ctx, "backup-failed:"+kind+":"+day,
		fmt.Sprintf("%s failed: %s. No backup was made. Check that the backup folder exists, has free space and is not locked, then press Back up now on the Backups page.", which, msg), true)
}

// backupDue decides whether the scheduler should take a backup now: enabled, none in the last 24 hours, the app idle, and
// not just after a failure.
func (p *packaging) backupDue(ctx context.Context) bool {
	s := p.backupSettings(ctx)
	if !s.Enabled {
		return false
	}
	p.mu.Lock()
	failedAt, retry := p.lastFail, p.retryAfter
	p.mu.Unlock()
	if !failedAt.IsZero() && p.bnow().Sub(failedAt) < retry {
		return false
	}
	list, _ := backup.List(s.Dir)
	var newest time.Time
	for _, b := range list {
		if b.Created.After(newest) {
			newest = b.Created
		}
	}
	age := p.bnow().Sub(newest)
	if !newest.IsZero() && age < 24*time.Hour {
		return false
	}
	runs, agents, err := p.c.Activity(ctx)
	if err == nil && (runs > 0 || agents > 0) && (newest.IsZero() || age < 72*time.Hour) {
		return false // busy: try again later; after three days a backup is taken regardless (it never blocks the writer)
	}
	return true
}

func (p *packaging) backupLoop(ctx context.Context) {
	// the first check shortly after start (Config.BackupFirstCheck, default 5s), then on the interval
	timer := time.NewTimer(p.backupFirst)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		p.backupTick(ctx)
		timer.Reset(p.backupEvery)
	}
}

func (p *packaging) backupTick(ctx context.Context) {
	if p.backupDue(ctx) {
		_, _ = p.runBackup(ctx, "scheduled")
	}
}

// ---- restore: staged for the next start ----

type restoreMarker struct {
	BackupPath string `json:"backupPath"`
	Name       string `json:"name"`
	SHA256     string `json:"sha256"`
	BackupsDir string `json:"backupsDir"`
	Requested  int64  `json:"requested"`
	By         string `json:"requestedBy"`
}

// resolveBackup finds a backup of the configured folder by file name (never by a path, so the name cannot escape the folder).
func (p *packaging) resolveBackup(ctx context.Context, name string) (backup.Info, BackupSettings, error) {
	s := p.backupSettings(ctx)
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return backup.Info{}, s, errors.New("name a backup from the list")
	}
	list, err := backup.List(s.Dir)
	if err != nil {
		return backup.Info{}, s, err
	}
	for _, b := range list {
		if b.Name == name {
			return b, s, nil
		}
	}
	return backup.Info{}, s, fmt.Errorf("there is no backup named %s in %s", name, p.identity.Apply(s.Dir))
}

func (p *packaging) stageRestore(ctx context.Context, name string, confirm bool) (map[string]any, error) {
	info, s, err := p.resolveBackup(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := backup.Verify(ctx, info); err != nil {
		return nil, fmt.Errorf("this backup is damaged and was not staged: %w", err)
	}
	v, err := storage.FileSchemaVersion(ctx, info.Path)
	if err != nil {
		return nil, fmt.Errorf("this file is not a Drydock database: %w", err)
	}
	if v > storage.LatestVersion() {
		return nil, fmt.Errorf("this backup was made by a newer Drydock (schema %d, this version has %d). Update Drydock first", v, storage.LatestVersion())
	}
	runs, agents, _ := p.c.Activity(ctx)
	if !confirm {
		return nil, errors.New("restoring replaces everything in Drydock with the contents of this backup (the current database is kept as replaced-<time>.db in the backup folder). Confirm to stage it for the next start")
	}
	mk := restoreMarker{BackupPath: info.Path, Name: info.Name, SHA256: info.SHA256, BackupsDir: s.Dir, Requested: p.bnow().UnixMilli(), By: p.c.Human()}
	b, _ := json.MarshalIndent(mk, "", "  ")
	tmp := filepath.Join(p.c.cfg.DataDir, restorePendingFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(p.c.cfg.DataDir, restorePendingFile)); err != nil {
		return nil, err
	}
	p.audit(ctx, "backup.restore.staged", info.Name, map[string]any{"schema": v})
	return map[string]any{"staged": true, "restartRequired": true, "backup": info.Name, "schemaVersion": v,
		"message":  "The restore is staged. Quit and reopen Drydock to apply it; running agents stop when you quit.",
		"warnings": activityWarnings(runs, agents)}, nil
}

func activityWarnings(runs, agents int) []string {
	var w []string
	if runs > 0 {
		w = append(w, fmt.Sprintf("%d workflow run(s) are running and will be interrupted", runs))
	}
	if agents > 0 {
		w = append(w, fmt.Sprintf("%d agent process(es) are alive and will be stopped", agents))
	}
	return w
}

// confineRestore turns a staged restore marker into the backup it names, the way the person chose it: by file name in the backup
// folder, found again by listing that folder (so a path in the marker is not followed anywhere), a plain file and not a link, and
// with the hash its sidecar records now equal to the one that was staged. A marker is a file in the data folder, and the database
// it makes the app install is the whole of Drydock's state (audit 2026-10-16, L19).
func confineRestore(mk restoreMarker) (backup.Info, error) {
	list, err := backup.List(mk.BackupsDir)
	if err != nil {
		return backup.Info{}, fmt.Errorf("the backup folder cannot be read: %w", err)
	}
	for _, b := range list {
		if b.Name != mk.Name {
			continue
		}
		if filepath.Clean(mk.BackupPath) != filepath.Clean(b.Path) {
			return backup.Info{}, errors.New("the staged restore points outside the backup folder")
		}
		if st, err := os.Lstat(b.Path); err != nil || !st.Mode().IsRegular() {
			return backup.Info{}, errors.New("the staged backup is not a plain file")
		}
		if b.SHA256 != mk.SHA256 {
			return backup.Info{}, errors.New("the backup's recorded hash is not the one that was staged: it was changed after the restore was staged")
		}
		return b, nil
	}
	return backup.Info{}, fmt.Errorf("there is no backup named %s in the backup folder", mk.Name)
}

// applyPendingRestore is the hook at the top of Core.Open, before the database is opened: it applies a restore staged by
// backup.restore. The current database is moved aside by backup.Restore, never deleted; if installing the backup fails after
// that move, the old database is put back. The outcome is left in a small JSON file the extension reports on start.
func applyPendingRestore(ctx context.Context, dataDir string) {
	marker := filepath.Join(dataDir, restorePendingFile)
	b, err := os.ReadFile(marker)
	if err != nil {
		return
	}
	fail := func(msg string) {
		fmt.Fprintf(os.Stderr, "drydock: restore not applied: %s\n", msg)
		out, _ := json.Marshal(map[string]any{"ok": false, "error": msg, "at": time.Now().UnixMilli()})
		_ = os.WriteFile(filepath.Join(dataDir, restoreFailedFile), out, 0o644)
		_ = os.Remove(marker)
	}
	var mk restoreMarker
	if err := json.Unmarshal(b, &mk); err != nil || mk.BackupPath == "" || mk.BackupsDir == "" {
		fail("the staged restore marker is unreadable")
		return
	}
	info, err := confineRestore(mk)
	if err != nil {
		fail(err.Error())
		return
	}
	if v, err := storage.FileSchemaVersion(ctx, info.Path); err != nil {
		fail("the backup cannot be read: " + err.Error())
		return
	} else if v > storage.LatestVersion() {
		fail(fmt.Sprintf("the backup is from a newer Drydock (schema %d)", v))
		return
	}
	dbPath := filepath.Join(dataDir, "drydock.db")
	replaced, err := backup.Restore(ctx, info, dbPath, mk.BackupsDir, time.Now())
	if err != nil {
		if replaced != "" { // the current database was moved aside before the failure: put it back
			if _, serr := os.Stat(dbPath); serr != nil {
				_ = os.Rename(replaced, dbPath)
				for _, sfx := range []string{"-wal", "-shm"} {
					if _, e := os.Stat(replaced + sfx); e == nil {
						_ = os.Rename(replaced+sfx, dbPath+sfx)
					}
				}
			}
		}
		fail(err.Error())
		return
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "backup": mk.Name, "replaced": replaced, "at": time.Now().UnixMilli()})
	_ = os.WriteFile(filepath.Join(dataDir, restoreResultFile), out, 0o644)
	_ = os.Remove(marker)
}

// announceRestoreResult tells the human, once, how a restore applied at start went.
func (p *packaging) announceRestoreResult(ctx context.Context) {
	dir := p.c.cfg.DataDir
	for _, name := range []string{restoreResultFile, restoreFailedFile} {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		_ = os.Remove(path)
		var r struct {
			OK       bool   `json:"ok"`
			Backup   string `json:"backup"`
			Replaced string `json:"replaced"`
			Error    string `json:"error"`
			At       int64  `json:"at"`
		}
		if json.Unmarshal(b, &r) != nil {
			continue
		}
		if r.OK {
			p.c.publish(ctx, "backup.restored", "", map[string]any{"backup": r.Backup})
			p.notify(ctx, fmt.Sprintf("restore-ok:%d", r.At), fmt.Sprintf("Restored from backup %s. The database it replaced was kept as %s in the backup folder.", r.Backup, filepath.Base(r.Replaced)), false)
		} else {
			p.c.publish(ctx, "backup.restore_failed", "", map[string]any{"error": r.Error})
			p.notify(ctx, fmt.Sprintf("restore-failed:%d", r.At), "The staged restore was not applied and your data is unchanged: "+r.Error, true)
		}
	}
}

// ---- methods ----

func (p *packaging) backupMethods(m map[string]Method) {
	m["backup.settings.get"] = bind(func(ctx context.Context, _ struct{}) (any, error) { return p.backupSettings(ctx), nil })
	m["backup.settings.set"] = bind(func(ctx context.Context, r backupSettingsReq) (any, error) { return p.setBackupSettings(ctx, r) })
	m["backup.list"] = bind(func(ctx context.Context, _ struct{}) (any, error) {
		s := p.backupSettings(ctx)
		list, err := backup.List(s.Dir)
		if err != nil {
			return nil, err
		}
		if list == nil {
			list = []backup.Info{}
		}
		replaced := []string{}
		if ents, err := os.ReadDir(s.Dir); err == nil {
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), "replaced-") && strings.HasSuffix(e.Name(), ".db") {
					replaced = append(replaced, e.Name())
				}
			}
		}
		return map[string]any{"dir": s.Dir, "backups": list, "replaced": replaced, "warning": s.Warning, "settings": s}, nil
	})
	m["backup.now"] = bind(func(ctx context.Context, r struct {
		Label string `json:"label"`
	}) (any, error) {
		label := r.Label
		if label == "" {
			label = "manual"
		}
		return p.runBackup(ctx, label)
	})
	m["backup.verify"] = bind(func(ctx context.Context, r struct {
		Name string `json:"name"`
	}) (any, error) {
		info, _, err := p.resolveBackup(ctx, r.Name)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"name": info.Name, "size": info.Size, "created": info.Created}
		if err := backup.Verify(ctx, info); err != nil {
			out["ok"], out["error"] = false, err.Error()
			return out, nil
		}
		v, err := storage.FileSchemaVersion(ctx, info.Path)
		out["ok"], out["schemaVersion"], out["restorableHere"] = err == nil, v, err == nil && v <= storage.LatestVersion()
		return out, nil
	})
	m["backup.restore"] = bind(func(ctx context.Context, r struct {
		Name    string `json:"name"`
		Confirm bool   `json:"confirm"`
	}) (any, error) {
		return p.stageRestore(ctx, r.Name, r.Confirm)
	})
	m["backup.restore.status"] = bind(func(ctx context.Context, _ struct{}) (any, error) {
		b, err := os.ReadFile(filepath.Join(p.c.cfg.DataDir, restorePendingFile))
		if err != nil {
			return map[string]any{"pending": false}, nil
		}
		var mk restoreMarker
		_ = json.Unmarshal(b, &mk)
		return map[string]any{"pending": true, "backup": mk.Name, "requested": mk.Requested}, nil
	})
	m["backup.restore.cancel"] = bind(func(ctx context.Context, _ struct{}) (any, error) {
		err := os.Remove(filepath.Join(p.c.cfg.DataDir, restorePendingFile))
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{"cancelled": false}, nil
		}
		return map[string]any{"cancelled": err == nil}, err
	})
}
