package sandbox

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The workspace's TCP tables as the kernel writes them: a dev server on 0.0.0.0:3000 (inode 111, held by the background process
// "dev"), a database on [::]:5432, a port bound to the workspace's loopback only (the forwarder cannot reach it), a connection that
// is not listening, and a socket line naming a process the sandbox never started under that name.
const listeningSample = `#tcp
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 111 1 0000000000000000 100 0 0 10 0
   1: 0100007F:2328 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 222 1 0000000000000000 100 0 0 10 0
   2: 0200A8C0:AD9C 0100A8C0:0BB8 01 00000000:00000000 00:00000000 00000000  1000        0 333 1 0000000000000000 20 4 30 10 -1
#tcp6
  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:1538 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  999        0 444 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000001000000:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  999        0 555 1 0000000000000000 100 0 0 10 0
#sock dev [111]
#sock <b>evil</b> [444]
`

func TestListeningPortsAreReadFromTheWorkspaceTables(t *testing.T) {
	got := ParseListening(listeningSample)
	if len(got) != 2 || got[0] != (ListeningPort{Port: 3000, Name: "dev"}) || got[1] != (ListeningPort{Port: 5432}) {
		t.Fatalf("%+v", got)
	}
	if len(ParseListening("")) != 0 || len(ParseListening("garbage\n#tcp\nnot a table")) != 0 {
		t.Fatal("output that is no table listed ports")
	}
}

// The script runs for real against this machine's /proc: a port this test listens on, with this process named as a background
// process, is listed under that name.
func TestTheListeningScriptNamesTheBackgroundProcessThatListens(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the script reads Linux's /proc")
	}
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "web.pid"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(listeningScript, bgDir, dir)
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, lp := range ParseListening(string(out)) {
		if lp.Port == port {
			if lp.Name != "web" {
				t.Fatalf("port %d is listed without its process: %+v", port, lp)
			}
			return
		}
	}
	t.Fatalf("port %d is not listed:\n%s", port, out)
}

func TestListeningAsksTheTaskWorkspace(t *testing.T) {
	e, calls := fakeEngine(t, "t1", `case "$1" in
inspect) echo "true|t1|p1|drydock";;
exec) cat <<'EOF'
`+listeningSample+`EOF
;;
esac`)
	got, err := NewManager(e, "").Listening(context.Background(), "t1")
	if err != nil || len(got) != 2 || got[0].Port != 3000 {
		t.Fatalf("%+v %v", got, err)
	}
	b, _ := os.ReadFile(calls)
	if !strings.Contains(string(b), "exec "+BaseName("t1")+"-ws sh -c") {
		t.Fatalf("the workspace was not asked:\n%s", b)
	}
}
