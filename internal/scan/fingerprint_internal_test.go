package scan

import "testing"

// Two findings with the same fingerprint in one report (the same rule on two identical lines of a file) are told apart by a
// suffix in report order, and a suffixed fingerprint is still a fingerprint.
func TestTheSameFingerprintTwiceInAReportGetsASuffix(t *testing.T) {
	fp := Fingerprint(SAST, "r", "a.go", "x := rand.Int()", 3)
	fs := []Finding{{Fingerprint: fp}, {Fingerprint: "ffffffffffffffffffffffff"}, {Fingerprint: fp}, {Fingerprint: fp}, {}}
	numberFingerprints(fs)
	if fs[0].Fingerprint != fp || fs[2].Fingerprint != fp+"-2" || fs[3].Fingerprint != fp+"-3" || fs[1].Fingerprint != "ffffffffffffffffffffffff" || fs[4].Fingerprint != "" {
		t.Fatalf("%+v", fs)
	}
	if !ValidFingerprint(fs[3].Fingerprint) || BaseFingerprint(fs[3].Fingerprint) != fp || ValidFingerprint("../x") || ValidFingerprint(fp+"x") {
		t.Fatal("ValidFingerprint")
	}
	// indentation and line endings do not change it; another text does, and so does another line for a finding without text
	if Fingerprint(SAST, "r", "a.go", "\t x :=   rand.Int()\r", 9) != fp || Fingerprint(SAST, "r", "a.go", "y := 1", 3) == fp ||
		Fingerprint(Deps, "Go", "go.mod", "", 1) == Fingerprint(Deps, "Go", "go.mod", "", 2) {
		t.Fatal("normalization")
	}
	// a built-in rule keeps its fingerprint under any folder Opengrep read it from
	a := Fingerprint(SAST, "home.a.rules.builtin-111111.drydock.drydock.go.weak-hash", "a.go", "md5.New()", 1)
	b := Fingerprint(SAST, "C:.Users.b.rules.builtin-222222.drydock.drydock.go.weak-hash", "a.go", "md5.New()", 1)
	if a != b {
		t.Fatal("the rules folder changed a built-in rule's fingerprint")
	}
}
