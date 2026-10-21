package pipeline

import "testing"

// An answer from a person counts for the branch as it is only when it is at the commit they were asked about and at the one the
// reviewer saw; a commit that was never recorded cannot be compared and does not block (audit 2026-10-16, L15).
func TestApprovalMismatch(t *testing.T) {
	for _, c := range []struct {
		name                      string
		requested, reviewed, head string
		wantWhy, wantCommit       string
	}{
		{"everything at one commit", "c1", "c1", "c1", "", ""},
		{"nothing recorded", "", "", "c1", "", ""},
		{"the person was shown an older commit", "c0", "c1", "c1", "request", "c0"},
		{"the reviewer saw an older commit", "c1", "c0", "c1", "review", "c0"},
		{"only the request is recorded and it is current", "c1", "", "c1", "", ""},
		{"only the review is recorded and it is old", "", "c0", "c1", "review", "c0"},
		{"the branch is not known", "c0", "c0", "", "", ""},
	} {
		why, commit := approvalMismatch(c.requested, c.reviewed, c.head)
		if why != c.wantWhy || commit != c.wantCommit {
			t.Errorf("%s: got %q %q, want %q %q", c.name, why, commit, c.wantWhy, c.wantCommit)
		}
	}
}
