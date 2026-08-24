package cli

import "testing"

func TestInitFormatHelpMatchesSupportedOutput(t *testing.T) {
	cmd := newInitCommand()
	formatFlag := cmd.Flags().Lookup("format")
	if formatFlag == nil {
		t.Fatal("init format flag is missing")
	}
	if got, want := formatFlag.Usage, "Output format: json"; got != want {
		t.Fatalf("format help = %q, want %q", got, want)
	}
}
