package core

import "testing"

func TestNoteTitleFromBodyUsesFirstLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     string
		fallback string
		want     string
	}{
		{
			name:     "markdown heading",
			body:     "# Quarterly plan\n\nDetails",
			fallback: "Old title",
			want:     "Quarterly plan",
		},
		{
			name:     "plain text",
			body:     "Customer interview notes\n\nDetails",
			fallback: "Old title",
			want:     "Customer interview notes",
		},
		{
			name:     "plain text ending in hash",
			body:     "Status #\n\nDetails",
			fallback: "Old title",
			want:     "Status #",
		},
		{
			name:     "closing heading markers",
			body:     "## Roadmap review ##\n\nDetails",
			fallback: "Old title",
			want:     "Roadmap review",
		},
		{
			name:     "blank first line",
			body:     "\nDetails",
			fallback: "Old title",
			want:     "Old title",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := NoteTitleFromBody(test.body, test.fallback); got != test.want {
				t.Fatalf("NoteTitleFromBody() = %q, want %q", got, test.want)
			}
		})
	}
}
