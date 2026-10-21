package security

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPathVerdicts(t *testing.T) {
	tests := []struct {
		shell   ShellKind
		cmd     string
		outside bool
		cwd     string
	}{
		{bash, "cd src && cat a", false, ""},
		{bash, "cd .. && cat a", true, ""},
		{bash, "cd src; cd .. ; cat a", false, ""},
		{bash, "cd src/.. && cat a", false, ""},
		{bash, "(cd src); cat a", false, ""},
		{bash, "(cd ..); cat a", true, ""},
		{bash, "cd $X; cat a", true, ""},
		{bash, "cd; cat a", true, ""},
		{bash, "cd -; cat a", true, ""},
		{bash, "cd ~; cat a", true, ""},
		{bash, "if true; then cd ..; fi; cat a", true, ""},
		{bash, "true || cd ..; cat a", true, ""},
		{bash, "cd src | cat a", false, ""},
		{bash, "pushd src && cat a", false, ""},
		{bash, "pushd src && popd && cat a", true, ""},
		{bash, "cat a", false, "/work/repo/deep/er"},
		{bash, "cat ../../../a", true, "/work/repo/deep/er"},
		{bash, "cat /dev/null", false, ""},
		{bash, "echo hi > /dev/null", false, ""},
		{bash, "echo hi 2>&1", false, ""},
		{bash, "ls 2>/dev/null >/dev/null", false, ""},
		{bash, "cat https://example.com/a", false, ""},
		{bash, "git clone git@github.com:org/repo.git", false, ""},
		{bash, "git clone git@github.com:org/repo.git ../other", true, ""},
		{bash, "curl -o ../x https://example.com", true, ""},
		{bash, "mytool --out=../x", true, ""},
		{bash, "mytool --out=x", false, ""},
		{bash, "dd of=/dev/sda", true, ""},
		{psh, "Set-Location ..; Get-ChildItem x", true, ""},
		{psh, "Set-Location src; Get-ChildItem x", false, ""},
		{psh, "cd ..; ls", true, ""},
		{psh, "Push-Location ..; ls", true, ""},
		{psh, "Pop-Location; Get-ChildItem x", true, ""},
		{psh, "Get-ChildItem -Path C:\\Windows", true, ""},
		{psh, "Get-ChildItem -Path:C:\\Windows", true, ""},
		{psh, "Get-ChildItem -Path:$p", true, ""},
		{psh, "Get-ChildItem -Recurse:$true", false, ""},
		{psh, "Get-ChildItem \\\\server\\share", true, ""},
		{psh, "Get-ChildItem \\\\?\\C:\\work\\repo", true, ""},
		{psh, "Get-Content con", true, ""},
		{psh, "Get-Content nul", false, ""},
		{psh, "Get-Content file.txt:secret", true, ""},
		{psh, "Get-Content PROGRA~1", false, ""},
		{psh, "Get-Content .\\a\\..\\b", false, ""},
		{psh, "Get-Content C:\\work\\repo\\a", false, ""},
		{psh, "Get-Content c:/work/repo/a", false, ""},
		{psh, "Get-Content C:\\work\\repo2\\a", true, ""},
		{psh, "Get-Content -Encoding UTF8 a.txt", false, ""},
		{psh, "Set-Content a.txt -Value 'C:\\Windows\\x'", false, ""},
		{psh, "Write-Output C:\\Windows", false, ""},
		{psh, "Get-ChildItem > ..\\out.txt", true, ""},
		{psh, "Get-ChildItem *> $null", false, ""},
		{cmdx, "cd .. & dir x", true, ""},
		{cmdx, "cd src & dir x", false, ""},
		{cmdx, "cd /d C:\\Windows & dir", true, ""},
		{cmdx, "cd /d C:\\work\\repo\\src & dir x", false, ""},
		{cmdx, "pushd .. & dir", true, ""},
		{cmdx, "dir D:\\", true, ""},
		{cmdx, "dir C:foo", true, ""},
		{cmdx, "dir \\\\server\\share", true, ""},
		{cmdx, "dir CON", true, ""},
		{cmdx, "dir > nul", false, ""},
		{cmdx, "dir > out.txt", false, ""},
		{cmdx, "dir > ..\\out.txt", true, ""},
		{cmdx, "type a.txt:stream", true, ""},
		{cmdx, "type a.txt.", true, ""},
		{cmdx, "copy a b", false, ""},
		{cmdx, "copy a C:\\x", true, ""},
		{cmdx, "if exist ..\\x del y", true, ""},
		{cmdx, "if exist x del y", false, ""},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(string(tc.shell)+"_"+shorten(tc.cmd), func(t *testing.T) {
			an, root := posixAn, posixRoot
			if tc.shell != bash {
				an, root = winAn, winRoot
			}
			cwd := tc.cwd
			rep, err := an.Analyze(context.Background(), CommandInput{Raw: tc.cmd, Shell: tc.shell, Root: root, Cwd: cwd})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Confidence == Opaque {
				t.Fatalf("unexpectedly opaque: %v", rep.Reasons)
			}
			if got := !rep.AllInside(); got != tc.outside {
				t.Fatalf("outside = %v, want %v; paths=%+v reasons=%v", got, tc.outside, rep.Paths, rep.Reasons)
			}
		})
	}
}

// With a real filesystem the analyzer uses the guard's symlink resolution.
func TestAnalyzerSeesSymlinkEscapes(t *testing.T) {
	root := realRoot(t)
	outside := realRoot(t)
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outside, filepath.Join(root, "link"))
	mustSymlink(t, filepath.Join(root, "real"), filepath.Join(root, "inlink"))
	an, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	check := func(cmd string, wantOutside bool) {
		t.Helper()
		rep, err := an.Analyze(context.Background(), CommandInput{Raw: cmd, Shell: bash, Root: root})
		if err != nil {
			t.Fatal(err)
		}
		if got := !rep.AllInside(); got != wantOutside {
			t.Errorf("%q: outside = %v, want %v (%+v)", cmd, got, wantOutside, rep.Paths)
		}
		if d := Decide(rep); wantOutside && d.Verdict == VerdictAllow {
			t.Errorf("%q allowed", cmd)
		}
	}
	check("cat link/secret.txt", true)
	check("cat link", true)
	check("echo x > link/new.txt", true)
	check("cd link && cat secret.txt", true)
	check("cat inlink/f", false)
	check("cat real/f", false)
	check("cat ./newfile", false)
}
