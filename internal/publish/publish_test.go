package publish

import "testing"

func TestBranchFor(t *testing.T) {
	for _, tt := range []struct{ title, branch string }{
		{"ui/library: artist name shows up properly in small windows", "ui-library"},
		{"ui/library: another change: with a colon", "ui-library"},
		{"fix(player): avoid crash", "fix-player"},
		{"fix: avoid crash", "fix"},
		{" UI/Library :", "ui-library"},
		{"Update library", "chore/update-library"},
		{"", "chore/update"},
	} {
		t.Run(tt.title, func(t *testing.T) {
			if got := BranchFor(tt.title); got != tt.branch {
				t.Errorf("BranchFor(%q) = %q, want %q", tt.title, got, tt.branch)
			}
		})
	}
}
