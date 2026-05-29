package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestRepoReportNeedsAttention(t *testing.T) {
	cases := []struct {
		name string
		r    repoReport
		want bool
	}{
		{"clean and pushed", repoReport{HasUpstream: true}, false},
		{"clean, upstream, in sync", repoReport{HasUpstream: true, Ahead: 0}, false},
		{"dirty", repoReport{Dirty: true, HasUpstream: true}, true},
		{"ahead", repoReport{Ahead: 3, HasUpstream: true}, true},
		{"no upstream but has commits", repoReport{HasUpstream: false, LocalCommits: 2}, true},
		{"no upstream, empty repo", repoReport{HasUpstream: false, LocalCommits: 0}, false},
		{"read error", repoReport{Err: errors.New("boom")}, true},
		{"fetch error", repoReport{HasUpstream: true, FetchErr: errors.New("offline")}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.needsAttention(); got != tc.want {
				t.Errorf("needsAttention() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRepoReportNotes(t *testing.T) {
	r := repoReport{
		Branch:      "feature",
		Dirty:       true,
		Ahead:       1,
		HasUpstream: true,
	}
	notes := strings.Join(r.notes(), "\n")
	if !strings.Contains(notes, "uncommitted changes") {
		t.Errorf("missing uncommitted note: %q", notes)
	}
	if !strings.Contains(notes, "1 unpushed commit on feature") {
		t.Errorf("missing/incorrect unpushed note: %q", notes)
	}
}

func TestRepoReportNotesErrorWins(t *testing.T) {
	r := repoReport{Dirty: true, Err: errors.New("boom")}
	notes := r.notes()
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "error:") {
		t.Errorf("error report should yield a single error note, got %v", notes)
	}
}

func TestRepoReportNoUpstreamNote(t *testing.T) {
	r := repoReport{Branch: "main", HasUpstream: false, LocalCommits: 5}
	notes := strings.Join(r.notes(), "\n")
	if !strings.Contains(notes, "no upstream") || !strings.Contains(notes, "5 local commits on main") {
		t.Errorf("missing no-upstream note: %q", notes)
	}
}

func TestPluralAndBranchOr(t *testing.T) {
	if plural(1) != "" || plural(0) != "s" || plural(2) != "s" {
		t.Error("plural")
	}
	if branchOr("") != "(detached HEAD)" || branchOr("main") != "main" {
		t.Error("branchOr")
	}
}
