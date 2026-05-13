package cli

import "testing"

func TestOutputFormatFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "default json", args: []string{"write", "--db", "a.db"}, want: formatJSON},
		{name: "long form", args: []string{"write", "--format", "raw"}, want: formatRaw},
		{name: "equals form", args: []string{"write", "--format=json"}, want: formatJSON},
		{name: "short form", args: []string{"write", "-f", "raw"}, want: formatRaw},
		{name: "invalid collapses empty", args: []string{"write", "--format", "xml"}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := outputFormatFromArgs(tc.args)
			if got != tc.want {
				t.Fatalf("outputFormatFromArgs()=%q want %q", got, tc.want)
			}
		})
	}
}

func TestValidateOutputFormat(t *testing.T) {
	if err := validateOutputFormat(formatJSON, false); err != nil {
		t.Fatalf("json should be valid: %v", err)
	}
	if err := validateOutputFormat(formatRaw, true); err != nil {
		t.Fatalf("raw should be valid when allowed: %v", err)
	}
	if err := validateOutputFormat(formatRaw, false); err == nil {
		t.Fatalf("raw should be invalid for non-recall commands")
	}
}
