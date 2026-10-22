package core

import "testing"

func TestAProjectKeyIsMadeFromItsName(t *testing.T) {
	for name, want := range map[string]string{
		"gocalc":                      "GOC",
		"Drydock":                     "DRY",
		"my web app":                  "MWA",
		"billing-service":             "BS",
		"the quick brown fox jumps x": "TQBFJ", // at most five
		"Go":                          "GO",
		"x":                           "PRJ", // fewer than two letters or digits
		"!!":                          "PRJ",
		"日本 app":                      "APP", // only ASCII letters and digits count
		"api v2":                      "AV",
		"3d printer":                  "3P",
	} {
		got, err := deriveProjectKey(name, nil)
		if err != nil || got != want || !projectKeyRE.MatchString(got) {
			t.Errorf("%q: %q (%v), want %q", name, got, err, want)
		}
	}
}

func TestAProjectKeyThatIsTakenGetsADigitOrALetter(t *testing.T) {
	taken := map[string]bool{}
	var got []string
	for range 4 {
		k, err := deriveProjectKey("gocalc", taken)
		if err != nil {
			t.Fatal(err)
		}
		taken[k] = true
		got = append(got, k)
	}
	if want := []string{"GOC", "GOC2", "GOC3", "GOC4"}; !equalStrings(got, want) {
		t.Fatalf("%q, want %q", got, want)
	}
	// a five-letter base changes its last letter
	if k, _ := deriveProjectKey("the quick brown fox jumps", map[string]bool{"TQBFJ": true}); k != "TQBF2" {
		t.Fatalf("five long: %q", k)
	}
	// past the single characters come two
	full := map[string]bool{"GOC": true}
	for _, c := range "23456789ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		full["GOC"+string(c)] = true
	}
	if k, _ := deriveProjectKey("gocalc", full); k != "GOC22" {
		t.Fatalf("two more: %q", k)
	}
	every := map[string]bool{}
	for _, k := range keyCandidates("GOC") {
		if !projectKeyRE.MatchString(k) {
			t.Fatalf("candidate %q is not a key", k)
		}
		every[k] = true
	}
	if _, err := deriveProjectKey("gocalc", every); err == nil {
		t.Fatal("a key was made when every candidate was taken")
	}
}

func TestAProjectKeyAPersonTypesIsChecked(t *testing.T) {
	for in, want := range map[string]string{"gx": "GX", " Goc1 ": "GOC1", "ABCDE": "ABCDE", "42": "42"} {
		if got, err := normalizeProjectKey(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "G", "TOOLONG", "G-1", "GÖC", "G C"} {
		if _, err := normalizeProjectKey(in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
