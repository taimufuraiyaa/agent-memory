package cli

import "testing"

func TestTerminalOnlyCLIHasNoFrontendInstallOrLaunch(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.Name() == "dashboard" {
			t.Fatal("browser dashboard is still registered")
		}
	}
	for _, command := range []string{"install", "upgrade"} {
		found, _, err := root.Find([]string{command})
		if err != nil {
			t.Fatal(err)
		}
		for _, flag := range []string{"dashboard-dir", "dashboard-src", "no-dashboard"} {
			if found.Flags().Lookup(flag) != nil {
				t.Errorf("%s still has frontend flag %s", command, flag)
			}
		}
	}
	for _, component := range defaultInstallComponents(installDetection{}) {
		if string(component.ID) == "dashboard" {
			t.Error("installer still has frontend component")
		}
	}
}
