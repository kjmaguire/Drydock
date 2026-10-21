// Fixtures of internal/scan/rules/drydock/go.yaml: `ruleid:` marks a line the rule must find, `ok:` one it must not. This
// file is test data (Go never builds a testdata folder): it is insecure on purpose.
package fixtures

import (
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"fmt"
	"math/rand"
	"net/http"
	"os/exec"
)

func run(db *sql.DB, name string) {
	// ruleid: drydock.go.tls-certificate-check-off
	_ = &tls.Config{InsecureSkipVerify: true}
	// ok: drydock.go.tls-certificate-check-off
	_ = &tls.Config{MinVersion: tls.VersionTLS12}

	// ruleid: drydock.go.shell-command-built-from-data
	_ = exec.Command("sh", "-c", "git log "+name)
	// ok: drydock.go.shell-command-built-from-data
	_ = exec.Command("sh", "-c", "git status")
	// ok: drydock.go.shell-command-built-from-data
	_ = exec.Command("git", "log", name)

	// ruleid: drydock.go.sql-built-with-sprintf
	_, _ = db.Query(fmt.Sprintf("SELECT * FROM users WHERE name = '%s'", name))
	// ruleid: drydock.go.sql-built-with-sprintf
	_, _ = db.Exec("DELETE FROM users WHERE name = '" + name + "'")
	// ok: drydock.go.sql-built-with-sprintf
	_, _ = db.Query("SELECT * FROM users WHERE name = ?", name)

	// ruleid: drydock.go.weak-hash
	_ = md5.Sum([]byte(name))
	// ok: drydock.go.weak-hash
	_ = sha256.Sum256([]byte(name))

	// ruleid: drydock.go.math-rand-for-secrets
	sessionToken := fmt.Sprint(rand.Int63())
	// ok: drydock.go.math-rand-for-secrets
	jitter := rand.Intn(10)
	_, _ = sessionToken, jitter

	// ruleid: drydock.go.http-server-without-timeouts
	_ = http.ListenAndServe(":8080", nil)
	// ok: drydock.go.http-server-without-timeouts
	srv := &http.Server{Addr: ":8080", ReadHeaderTimeout: 5}
	_ = srv.ListenAndServe()
}
