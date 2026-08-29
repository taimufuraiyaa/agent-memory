package cli

import "testing"

func TestSkillLifecycleCommandsAreRegistered(t *testing.T) {
	root := NewRootCommand()
	skill, _, err := root.Find([]string{"skill"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list", "inspect", "propose", "evaluate", "approve", "canary", "promote", "resolve", "acknowledge", "complete", "disable", "pin", "rollback"} {
		child, _, findErr := skill.Find([]string{name})
		if findErr != nil || child.Name() != name {
			t.Fatalf("missing skill %s command", name)
		}
	}
}
