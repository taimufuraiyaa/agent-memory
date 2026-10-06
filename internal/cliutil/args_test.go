package cliutil

import "testing"

func TestAnyBlank(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   bool
	}{
		{name: "no values", want: false},
		{name: "all non-empty", values: []string{"inventory", "plan"}, want: false},
		{name: "empty value", values: []string{"inventory", ""}, want: true},
		{name: "whitespace value", values: []string{"inventory", " \t\n"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AnyBlank(tt.values...); got != tt.want {
				t.Fatalf("AnyBlank(%q) = %v, want %v", tt.values, got, tt.want)
			}
		})
	}
}
