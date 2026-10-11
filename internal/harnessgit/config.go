// Package harnessgit is the harness's only way to run Git. Git runs the programs that a
// repository's own configuration names, so merely reading an untrusted repository can run
// code: a filesystem monitor, an external diff, a text conversion, a pager, hooks and,
// without any way to switch it off, a clean filter. This package therefore does two
// things. It refuses a repository whose configuration it cannot account for, by reading the
// configuration itself rather than asking Git; and it runs Git from a built environment
// with every remaining program-starting setting overridden on the command line.
package harnessgit

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Refusal is why a repository was not accepted. Reason is a fixed code; Setting names the
// offending section and key, never a value, so it can be shown without leaking content.
type Refusal struct {
	Reason  string
	Setting string
}

func (r *Refusal) Error() string {
	if r.Setting != "" {
		return "repository refused: " + r.Reason + " (" + r.Setting + ")"
	}
	return "repository refused: " + r.Reason
}

func refuse(reason, setting string) error { return &Refusal{Reason: reason, Setting: setting} }

// IsRefusal reports the refusal an error carries, if any.
func IsRefusal(err error) (*Refusal, bool) {
	var r *Refusal
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}

const maxConfigBytes = 64 << 10

// entry is one setting as written: lower-cased section and key, the subsection exactly as
// written, and the raw value text.
type entry struct {
	section    string
	subsection string
	hasSub     bool
	key        string
	value      string
}

// qualified is the setting's name in the form Git prints it: section and key lower-cased, the
// subsection as written.
func (e entry) qualified() string {
	if e.hasSub {
		return e.section + "." + e.subsection + "." + e.key
	}
	return e.section + "." + e.key
}

func (e entry) name() string {
	if e.hasSub {
		return e.section + ".<name>." + e.key
	}
	return e.section + "." + e.key
}

// parseConfig reads Git's configuration syntax far enough to know every setting that is
// present. It is strict on purpose: anything it cannot read with certainty is an error,
// because a file it reads differently from Git is how a hostile setting would get through.
func parseConfig(data []byte) ([]entry, error) {
	if len(data) > maxConfigBytes {
		return nil, refuse("config_too_large", "")
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return nil, refuse("config_unreadable", "")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	var out []entry
	section, subsection, hasSub := "", "", false
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		// A backslash at the end of a line continues it; the continuation is part of the value.
		for strings.HasSuffix(line, "\\") && i+1 < len(lines) {
			i++
			line = strings.TrimSuffix(line, "\\") + lines[i]
		}
		if strings.HasSuffix(line, "\\") {
			return nil, refuse("config_unreadable", "")
		}
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
			continue
		}
		if trimmed[0] == '[' {
			sec, sub, withSub, rest, err := parseHeader(trimmed)
			if err != nil {
				return nil, err
			}
			section, subsection, hasSub = sec, sub, withSub
			trimmed = strings.TrimLeft(rest, " \t")
			if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
				continue
			}
		}
		if section == "" {
			return nil, refuse("config_unreadable", "")
		}
		key, value, err := parseKeyValue(trimmed)
		if err != nil {
			return nil, err
		}
		out = append(out, entry{section: section, subsection: subsection, hasSub: hasSub, key: key, value: value})
	}
	return out, nil
}

func isNameChar(r byte) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.'
}

// parseHeader reads "[section]", "[section "sub"]" or the older "[section.sub]" and returns
// the rest of the line, which may hold a setting.
func parseHeader(line string) (section, sub string, hasSub bool, rest string, err error) {
	i := 1
	start := i
	for i < len(line) && isNameChar(line[i]) {
		i++
	}
	section = strings.ToLower(line[start:i])
	if section == "" {
		return "", "", false, "", refuse("config_unreadable", "")
	}
	if strings.Contains(section, ".") { // the older "[section.sub]" form: the subsection is lower-cased text
		parts := strings.SplitN(section, ".", 2)
		section, sub, hasSub = parts[0], parts[1], true
		if section == "" || sub == "" {
			return "", "", false, "", refuse("config_unreadable", "")
		}
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i < len(line) && line[i] == '"' {
		if hasSub {
			return "", "", false, "", refuse("config_unreadable", "")
		}
		i++
		var b strings.Builder
		for {
			if i >= len(line) {
				return "", "", false, "", refuse("config_unreadable", "")
			}
			c := line[i]
			if c == '\\' && i+1 < len(line) {
				b.WriteByte(line[i+1])
				i += 2
				continue
			}
			if c == '"' {
				i++
				break
			}
			b.WriteByte(c)
			i++
		}
		sub, hasSub = b.String(), true
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
	}
	if i >= len(line) || line[i] != ']' {
		return "", "", false, "", refuse("config_unreadable", "")
	}
	return section, sub, hasSub, line[i+1:], nil
}

// parseKeyValue reads "name = value", "name" (an implicit true) and quoted or commented values.
func parseKeyValue(line string) (key, value string, err error) {
	i := 0
	for i < len(line) && isNameChar(line[i]) && line[i] != '.' {
		i++
	}
	key = strings.ToLower(line[:i])
	if key == "" || !(key[0] >= 'a' && key[0] <= 'z') {
		return "", "", refuse("config_unreadable", "")
	}
	rest := strings.TrimLeft(line[i:], " \t")
	switch {
	case rest == "" || rest[0] == '#' || rest[0] == ';':
		return key, "", nil
	case rest[0] != '=':
		return "", "", refuse("config_unreadable", "")
	}
	// The value ends at the first '#' or ';' that is not inside quotes.
	raw := rest[1:]
	end, inQuotes := len(raw), false
	for j := 0; j < len(raw); j++ {
		switch c := raw[j]; {
		case c == '\\':
			j++
		case c == '"':
			inQuotes = !inQuotes
		case (c == '#' || c == ';') && !inQuotes:
			end = j
			j = len(raw)
		}
	}
	if inQuotes {
		return "", "", refuse("config_unreadable", "")
	}
	return key, strings.TrimSpace(raw[:end]), nil
}

// allowed lists, for each section a repository may have, the keys it may set. A section that
// is not here is refused, and so is a key that is not listed for a section that has a list.
// A nil list means every key of that section is harmless. This is an allowlist because the
// alternative, a list of dangerous names, fails open the day Git adds one.
var allowed = map[string]map[string]bool{
	"core": set("repositoryformatversion", "filemode", "bare", "logallrefupdates", "ignorecase", "precomposeunicode", "symlinks", "autocrlf", "eol", "safecrlf",
		"quotepath", "untrackedcache", "trustctime", "checkstat", "sparsecheckout", "sparsecheckoutcone", "longpaths", "protectntfs", "protecthfs", "whitespace",
		"abbrev", "commentchar", "compression", "loosecompression", "preloadindex", "ignorestat", "warnambiguousrefs", "bigfilethreshold", "excludesfile"),
	"extensions": set("objectformat"),
	"user":       nil, "author": nil, "committer": nil,
	"branch": nil, "tag": nil, "init": set("defaultbranch"), "push": nil, "pull": nil, "fetch": nil, "rebase": set("autosquash", "autostash", "updaterefs", "missingcommitscheck"),
	"color": nil, "column": nil, "advice": nil, "i18n": nil, "index": nil, "pack": nil, "format": nil, "status": nil, "gc": nil, "lfs": nil, "protocol": nil,
	"log":    set("abbrevcommit", "date", "decorate", "follow", "mailmap", "graphcolors", "excludedecoration"),
	"commit": set("gpgsign", "template", "cleanup", "verbose", "status"),
	"diff": set("renames", "algorithm", "colormoved", "colormovedws", "context", "interhunkcontext", "ignoresubmodules", "mnemonicprefix", "noprefix", "relative",
		"statgraphwidth", "submodule", "wserrorhighlight", "suppressblankempty", "srcprefix", "dstprefix", "orderfile"),
	"merge":     set("ff", "conflictstyle", "renormalize", "verbosity", "log", "stat", "renames", "directoryrenames"),
	"remote":    set("url", "pushurl", "fetch", "push", "tagopt", "prune", "mirror", "skipdefaultupdate", "skipfetchall"),
	"url":       set("insteadof", "pushinsteadof"),
	"submodule": set("url", "active", "branch", "fetchrecursesubmodules", "path"),
}

func set(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// subsectionAllowed says which sections may have named subsections ([remote "origin"],
// [branch "main"]). A diff or merge subsection names a driver, which is a program, so those
// are never allowed.
var subsectionAllowed = map[string]bool{"remote": true, "branch": true, "url": true, "submodule": true, "lfs": true, "tag": false}

// vet decides whether every setting is one a repository may carry.
func vet(entries []entry) error {
	for _, e := range entries {
		keys, known := allowed[e.section]
		switch {
		case !known:
			return refuse("config_risky", e.name())
		case e.hasSub && !subsectionAllowed[e.section]:
			return refuse("config_risky", e.section+".<name>."+e.key)
		case !e.hasSub && e.section == "remote" || !e.hasSub && e.section == "url" || !e.hasSub && e.section == "submodule":
			return refuse("config_risky", e.section+"."+e.key) // these only make sense with a name
		case keys != nil && !keys[e.key]:
			return refuse("config_risky", e.name())
		}
		if e.section == "core" && e.key == "bare" && !boolFalse(e.value) {
			return refuse("config_risky", "core.bare")
		}
		if e.section == "core" && e.key == "repositoryformatversion" && e.value != "0" && e.value != "1" {
			return refuse("config_risky", "core.repositoryformatversion")
		}
	}
	return nil
}

func boolFalse(v string) bool {
	switch strings.ToLower(strings.Trim(v, `" `)) {
	case "false", "no", "off", "0":
		return true
	}
	return false
}

// ScanConfig parses a repository's configuration and refuses it unless every setting is one
// that cannot start a program or reach outside the project.
func ScanConfig(data []byte) ([]string, error) {
	entries, err := parseConfig(data)
	if err != nil {
		return nil, err
	}
	if err := vet(entries); err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.qualified()
	}
	return names, nil
}
