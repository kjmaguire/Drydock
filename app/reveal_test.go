package app

import "testing"

func TestRevealCmdLineSelectsTheFile(t *testing.T) {
	got, err := revealCmdLine(`C:\Windows\explorer.exe`, `C:\Users\Kyle Doe\support\drydock-support.zip`)
	if err != nil || got != `"C:\Windows\explorer.exe" /select,"C:\Users\Kyle Doe\support\drydock-support.zip"` {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"", `C:\a"b.zip`, "C:\\a\nb.zip"} {
		if _, err := revealCmdLine(`C:\Windows\explorer.exe`, bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
}
