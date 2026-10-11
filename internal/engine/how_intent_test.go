package engine

import "testing"

func TestIsHowOrientedTask(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		task string
		want bool
	}{
		{"How do I verify the release?", true},
		{"show the steps used last time", true},
		{"recall the deployment workflow", true},
		{"what is the deployment port", false},
		{"who owns authentication", false},
		{"the somehow field is broken", false},
	} {
		if got := IsHowOrientedTask(test.task); got != test.want {
			t.Errorf("IsHowOrientedTask(%q) = %v, want %v", test.task, got, test.want)
		}
	}
}
