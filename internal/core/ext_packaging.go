package core

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/applog"
	"github.com/kjmaguire/drydock/internal/license"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/scrub"
)

// The Packaging extension (docs/packaging.md sections 6 to 10): the license in the app and its read-only enforcement,
// scheduled backups with staged restore, project export and import, and the support bundle. Everything lives in the
// ext_packaging*.go files; the base files only call RequireLicensed, gateMethods, fileLog, licenseChannelAllowed and
// applyPendingRestore.
func init() {
	RegisterExtension(Extension{Name: "packaging", After: []string{"agents-dev"}, Start: startPackaging})
}

// Build-time settings, set with -ldflags "-X github.com/kjmaguire/drydock/internal/core.<Name>=<value>".
var (
	// LicensePublicKeys lists the embedded license verification keys as "kid=<base64 Ed25519 public key>" pairs separated by
	// commas (several kids support key rotation). With none configured the build is an unlicensed/development build: the
	// license state is "none", licensing is reported as not configured, and EVERYTHING IS ALLOWED (decision P12).
	LicensePublicKeys = ""
	// AppVersion and AppBuild identify the build in support bundles and project exports.
	AppVersion = "0.0.0-dev"
	AppBuild   = ""
	// InstallScope is how the app was installed (NSIS per-user in v1, decision D8).
	InstallScope = "user"
)

const (
	keyLicenseToken     = "license.token"
	keyLicenseMaxSeen   = "license.maxseen"
	keyLicenseAnnounced = "license.announced"
)

// packaging is the per-Core state of the extension.
type packaging struct {
	c   *Core
	bg  context.Context
	now func() time.Time // injectable clock (tests)

	mu       sync.Mutex
	keys     license.Keys
	cachedTk string
	cachedP  *license.Payload
	cachedE  error
	maxSeen  time.Time
	savedMax time.Time
	kickCh   chan struct{}

	identity *scrub.Identity
	// backupClock stamps and schedules backups (the real clock; tests override it independently of the license clock)
	backupClock func() time.Time

	backupMu    sync.Mutex
	backupFirst time.Duration // the scheduler's first check after start
	backupEvery time.Duration // scheduler tick
	retryAfter  time.Duration // wait after a failed scheduled backup
	licenseTick time.Duration
	lastFail    time.Time
}

var packagingStates sync.Map // *Core -> *packaging

func (p *packaging) bnow() time.Time {
	p.mu.Lock()
	f := p.backupClock
	p.mu.Unlock()
	if f != nil {
		return f()
	}
	return time.Now()
}

func (c *Core) pkg() *packaging {
	if v, ok := packagingStates.Load(c); ok {
		return v.(*packaging)
	}
	return nil
}

// parseLicenseKeys reads "kid=base64,kid2=base64".
func parseLicenseKeys(spec string) (license.Keys, error) {
	keys := license.Keys{}
	for _, part := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		kid, b64, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || kid == "" {
			return nil, fmt.Errorf("license key %q is not kid=base64", part)
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("license key %q is not a base64 Ed25519 public key", kid)
		}
		keys[kid] = ed25519.PublicKey(raw)
	}
	return keys, nil
}

// backupFirstDefault is when the backup scheduler first checks after start when Config.BackupFirstCheck is zero. The
// internal/core test binary moves it out to an hour (export_test.go); everything else, other packages' tests included, gets 5s.
var backupFirstDefault = 5 * time.Second

func startPackaging(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	p := &packaging{c: c, bg: ctx, now: time.Now, kickCh: make(chan struct{}, 1),
		backupFirst: backupFirstDefault, backupEvery: 10 * time.Minute, retryAfter: time.Hour, licenseTick: 30 * time.Second}
	if c.cfg.BackupFirstCheck > 0 {
		p.backupFirst = c.cfg.BackupFirstCheck
	}
	keys, err := parseLicenseKeys(LicensePublicKeys)
	if err != nil {
		c.logf("licensing: %v (the build's embedded keys are unusable, so licensing is treated as not configured)", err)
		keys = nil
	}
	p.keys = keys
	home, _ := os.UserHomeDir()
	host, _ := os.Hostname()
	p.identity = scrub.NewIdentity(home, host, os.Getenv("USERNAME"), os.Getenv("USER"), c.cfg.User)
	packagingStates.Store(c, p)

	if raw, ok := p.getSetting(ctx, keyLicenseMaxSeen); ok {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			p.maxSeen = time.Unix(n, 0)
			p.savedMax = p.maxSeen
		}
	}
	// a token never reaches a log, an event or a bundle: teach the scanner its exact text
	if tk, ok := p.getSetting(ctx, keyLicenseToken); ok {
		_ = c.Scanner.RegisterSecret("license", tk)
	}
	if !p.configured() {
		c.logf("LICENSING IS NOT CONFIGURED IN THIS BUILD: this is a development or unlicensed build and every feature is allowed")
	}
	// An expired license must be effective before anything else starts work, including the interrupted-run recovery that
	// follows the extensions in Open: running work is parked now, so Recover finds nothing to restart.
	p.reconcile(ctx)

	if c.Engine != nil {
		c.Engine.UseOutermost("agent_task", p.nodeGate)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	safe.Go("core.startPackaging", func() { defer wg.Done(); p.licenseLoop(ctx) })
	safe.Go("core.startPackaging#2", func() { defer wg.Done(); p.backupLoop(ctx) })
	p.announceRestoreResult(ctx)

	m := map[string]Method{}
	p.licenseMethods(m)
	p.backupMethods(m)
	p.dataMethods(m)
	closeFn := func() {
		wg.Wait()
		packagingStates.Delete(c)
		appLogs.Delete(c)
	}
	return m, closeFn, nil
}

// ---- settings ----

func (p *packaging) getSetting(ctx context.Context, key string) (string, bool) {
	var v string
	if err := p.c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func (p *packaging) setSetting(ctx context.Context, key, value string) error {
	return p.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
		return err
	})
}

func (p *packaging) delSetting(ctx context.Context, key string) error {
	return p.c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key)
		return err
	})
}

// ---- application log ----

var appLogs sync.Map // *Core -> *applog.Writer

// fileLog appends one scrubbed line to <data>/logs/drydock.log (rotating) and returns the line as a log may show it, for the
// copy on stderr: scrubbed, or a notice when it could not be scanned. The scanner is needed to scrub, so lines logged
// before it exists (adapter detection at startup) stay on stderr only.
func (c *Core) fileLog(msg string) string {
	if c.Scanner == nil {
		return msg
	}
	shown, _, err := scrub.Text(context.Background(), c.Scanner, msg)
	if err != nil {
		shown = "[log line withheld: it could not be scanned for secrets]"
	}
	if c.cfg.DataDir == "" {
		return shown
	}
	v, ok := appLogs.Load(c)
	if !ok {
		sc := c.Scanner
		w := &applog.Writer{Dir: filepath.Join(c.cfg.DataDir, "logs"), Scrub: func(line string) (string, error) {
			out, _, err := scrub.Text(context.Background(), sc, line)
			return out, err
		}}
		v, _ = appLogs.LoadOrStore(c, w)
	}
	v.(*applog.Writer).Write(msg)
	return shown
}

// ---- notices: a SYSTEM message and, when the human must act, a Needs-you item, in every project's #alerts ----

// notify posts body as a SYSTEM message in each project's alerts channel. With needsYou it also posts a blocking question
// from the system, which the inbox shows until a human replies or dismisses it. idem makes a repeat a no-op.
func (p *packaging) notify(ctx context.Context, idem, body string, needsYou bool) {
	projects, err := p.c.Projects(ctx)
	if err != nil {
		return
	}
	for _, pr := range projects {
		p.c.alertsNotice(ctx, pr.ID, idem, body, needsYou)
	}
}

// alertsNotice posts body as a SYSTEM message in one project's alerts channel and, with needsYou, a blocking question from the
// system that the inbox shows until a human replies or dismisses it. idem makes a repeat a no-op. A project without an alerts
// channel gets nothing.
func (c *Core) alertsNotice(ctx context.Context, projectID, idem, body string, needsYou bool) {
	rooms, err := c.Room.ListRooms(ctx, projectID, false)
	if err != nil {
		return
	}
	target := ""
	for _, r := range rooms {
		if r.Kind == room.KindChannel && r.Name == "alerts" {
			target = r.ID
		}
	}
	if target == "" {
		return
	}
	if _, err := c.Room.Post(ctx, room.PostParams{RoomID: target, Type: room.TypeSystem, Author: room.System, Body: body, IdempotencyKey: idem}); err != nil {
		c.logf("notice: %v", err)
	}
	if needsYou {
		if _, err := c.Room.Post(ctx, room.PostParams{RoomID: target, Type: room.TypeQuestion, Author: room.System, Body: noticeTitle(body), IdempotencyKey: idem + ":needs-you",
			Payload: map[string]any{"blocking": true, "to": ""}}); err != nil {
			c.logf("needs-you item: %v", err)
		}
	}
}

// noticeTitle is the first sentence of a notice, for its Needs-you item: it ends at a full stop followed by a space or the
// end, or at a line break, so a dot inside a path, a file name or a version does not cut it short.
func noticeTitle(body string) string {
	if i := strings.IndexByte(body, '\n'); i > 0 {
		body = body[:i]
	}
	for i := 0; i < len(body); i++ {
		if body[i] == '.' && (i+1 == len(body) || body[i+1] == ' ') {
			return body[:i+1]
		}
	}
	return body
}

func (p *packaging) audit(ctx context.Context, action, subject string, detail any) {
	// the audit log is written through the existing security service; a failure to audit must not undo the action
	if err := auditAppend(ctx, p.c, action, subject, detail); err != nil {
		p.c.logf("audit %s: %v", action, err)
	}
}
