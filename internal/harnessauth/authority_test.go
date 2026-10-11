package harnessauth_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessauth"
)

type workspaces struct {
	mu    sync.Mutex
	roots map[string]string
}

func (w *workspaces) Root(name string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	root, ok := w.roots[name]
	if !ok {
		return "", errors.New("unknown workspace")
	}
	return root, nil
}

func (w *workspaces) set(name, root string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.roots[name] = root
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type env struct {
	t       *testing.T
	dir     string
	clients *clientprofile.Store
	ws      *workspaces
	clock   *clock
	auth    *harnessauth.Authority
	rootA   string
	rootB   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	clk := &clock{t: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	clients, err := clientprofile.Open(dir, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"claude-desktop", "codex-cli"} {
		if _, err := clients.Create(clientprofile.Input{ID: id, DisplayName: id, ClientKind: clientprofile.KindOther, ToolProfile: clientprofile.ProfileDefault}); err != nil {
			t.Fatal(err)
		}
	}
	rootA, rootB := t.TempDir(), t.TempDir()
	ws := &workspaces{roots: map[string]string{"project-a": rootA, "project-b": rootB}}
	auth, err := harnessauth.New(dir, harnessauth.Options{Clients: clients, Workspaces: ws, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, dir: dir, clients: clients, ws: ws, clock: clk, auth: auth, rootA: rootA, rootB: rootB}
}

func (e *env) enable() {
	e.t.Helper()
	if err := e.auth.SetEnabled(context.Background(), true); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) mint(client string, ops ...harnessauth.Operation) (string, harnessauth.GrantInfo) {
	e.t.Helper()
	if len(ops) == 0 {
		ops = []harnessauth.Operation{harnessauth.OpStart, harnessauth.OpStatus}
	}
	token, info, err := e.auth.Mint(context.Background(), harnessauth.MintRequest{ClientID: client, Workspaces: []string{"project-a"}, Operations: ops})
	if err != nil {
		e.t.Fatal(err)
	}
	return token, info
}

func (e *env) credentialFile() string {
	return filepath.Join(e.dir, "credentials", "harness-authority.json")
}

func request(op harnessauth.Operation) harnessauth.Request {
	return harnessauth.Request{Workspace: "project-a", Operation: op}
}

func TestMintRequiresOptInAndVerifyBindsEverything(t *testing.T) {
	e := newEnv(t)
	if _, _, err := e.auth.Mint(context.Background(), harnessauth.MintRequest{ClientID: "claude-desktop", Workspaces: []string{"project-a"}, Operations: []harnessauth.Operation{harnessauth.OpStart}}); !errors.Is(err, harnessauth.ErrDisabled) {
		t.Fatalf("mint before opt-in = %v", err)
	}
	e.enable()
	token, info := e.mint("claude-desktop")
	if info.Status != "active" || info.Revision != 1 || info.ClientID != "claude-desktop" || len(info.Operations) != 2 {
		t.Fatalf("info = %+v", info)
	}
	principal, err := e.auth.Verify(token, request(harnessauth.OpStart))
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(e.rootA)
	if principal.ClientID != "claude-desktop" || principal.Workspace != "project-a" || principal.Root != canonical ||
		principal.GrantRevision != 1 || principal.Operation != harnessauth.OpStart || principal.GrantID != info.ID {
		t.Fatalf("principal = %+v", principal)
	}
	if _, err := e.auth.Verify(token, harnessauth.Request{ClientID: "claude-desktop", Workspace: "project-a", Operation: harnessauth.OpStatus}); err != nil {
		t.Fatalf("matching client claim = %v", err)
	}
}

func TestEveryFailureIsTheSameDenial(t *testing.T) {
	e := newEnv(t)
	e.enable()
	token, info := e.mint("claude-desktop")
	parts := strings.Split(token, ".")
	flip := func(s string) string {
		if s[0] == 'a' {
			return "b" + s[1:]
		}
		return "a" + s[1:]
	}
	cases := map[string]string{
		"empty":            "",
		"garbage":          "not-a-token",
		"missing secret":   parts[0] + "." + parts[1],
		"extra part":       token + ".x",
		"wrong prefix":     "hcap2." + parts[1] + "." + parts[2],
		"unknown grant":    parts[0] + "." + strings.Repeat("0", 32) + "." + parts[2],
		"wrong secret":     parts[0] + "." + parts[1] + "." + flip(parts[2]),
		"short secret":     parts[0] + "." + parts[1] + "." + parts[2][:20],
		"id as secret":     parts[0] + "." + parts[1] + "." + parts[1],
		"secret plus junk": token + "!",
	}
	var firstMessage string
	for name, candidate := range cases {
		_, err := e.auth.Verify(candidate, request(harnessauth.OpStart))
		if !errors.Is(err, harnessauth.ErrDenied) {
			t.Errorf("%s = %v", name, err)
			continue
		}
		if firstMessage == "" {
			firstMessage = err.Error()
		}
		if err.Error() != firstMessage {
			t.Errorf("%s produced a distinguishable error %q", name, err)
		}
	}
	// The real token still works, and its failures are denials too.
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.Revoke(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); !errors.Is(err, harnessauth.ErrDenied) || err.Error() != firstMessage {
		t.Fatalf("revoked = %v", err)
	}
}

func TestCrossClientOperationAndWorkspaceScoping(t *testing.T) {
	e := newEnv(t)
	e.enable()
	token, _ := e.mint("claude-desktop", harnessauth.OpStatus)
	for name, req := range map[string]harnessauth.Request{
		"another client's claim": {ClientID: "codex-cli", Workspace: "project-a", Operation: harnessauth.OpStatus},
		"ungranted operation":    {Workspace: "project-a", Operation: harnessauth.OpCancel},
		"unknown operation":      {Workspace: "project-a", Operation: "approve"},
		"unbound workspace":      {Workspace: "project-b", Operation: harnessauth.OpStatus},
		"unregistered workspace": {Workspace: "nowhere", Operation: harnessauth.OpStatus},
		"empty workspace":        {Operation: harnessauth.OpStatus},
		"empty operation":        {Workspace: "project-a"},
	} {
		if _, err := e.auth.Verify(token, req); !errors.Is(err, harnessauth.ErrDenied) {
			t.Errorf("%s = %v", name, err)
		}
	}
	// Two clients hold independent authority.
	other, _ := e.mint("codex-cli", harnessauth.OpStart)
	if _, err := e.auth.Verify(other, request(harnessauth.OpStatus)); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("second client inherited status: %v", err)
	}
	if _, err := e.auth.Verify(other, harnessauth.Request{ClientID: "claude-desktop", Workspace: "project-a", Operation: harnessauth.OpStart}); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("second client impersonated the first: %v", err)
	}
}

func TestApprovalCanNeverBeGranted(t *testing.T) {
	e := newEnv(t)
	e.enable()
	for _, op := range []harnessauth.Operation{"approve", "admin", "", "START"} {
		_, _, err := e.auth.Mint(context.Background(), harnessauth.MintRequest{ClientID: "claude-desktop", Workspaces: []string{"project-a"}, Operations: []harnessauth.Operation{op}})
		if !errors.Is(err, harnessauth.ErrInvalid) {
			t.Errorf("operation %q = %v", op, err)
		}
	}
	for _, op := range harnessauth.AllOperations() {
		if op == "approve" {
			t.Fatal("approve is grantable")
		}
	}
}

func TestMintValidation(t *testing.T) {
	e := newEnv(t)
	e.enable()
	base := harnessauth.MintRequest{ClientID: "claude-desktop", Workspaces: []string{"project-a"}, Operations: []harnessauth.Operation{harnessauth.OpStart}}
	mutate := func(f func(*harnessauth.MintRequest)) harnessauth.MintRequest { r := base; f(&r); return r }
	for name, req := range map[string]harnessauth.MintRequest{
		"unregistered client": mutate(func(r *harnessauth.MintRequest) { r.ClientID = "ghost" }),
		"bad client ID":       mutate(func(r *harnessauth.MintRequest) { r.ClientID = "Bad ID" }),
		"no workspaces":       mutate(func(r *harnessauth.MintRequest) { r.Workspaces = nil }),
		"unregistered ws":     mutate(func(r *harnessauth.MintRequest) { r.Workspaces = []string{"ghost"} }),
		"duplicate ws":        mutate(func(r *harnessauth.MintRequest) { r.Workspaces = []string{"project-a", "project-a"} }),
		"no operations":       mutate(func(r *harnessauth.MintRequest) { r.Operations = nil }),
		"ttl too short":       mutate(func(r *harnessauth.MintRequest) { r.TTL = time.Second }),
		"ttl too long":        mutate(func(r *harnessauth.MintRequest) { r.TTL = harnessauth.MaxTTL + time.Hour }),
	} {
		if _, _, err := e.auth.Mint(context.Background(), req); !errors.Is(err, harnessauth.ErrInvalid) {
			t.Errorf("%s = %v", name, err)
		}
	}
	e.ws.set("gone", filepath.Join(e.dir, "does-not-exist"))
	if _, _, err := e.auth.Mint(context.Background(), mutate(func(r *harnessauth.MintRequest) { r.Workspaces = []string{"gone"} })); !errors.Is(err, harnessauth.ErrInvalid) {
		t.Errorf("missing root = %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, _, err := e.auth.Mint(context.Background(), base); err != nil {
			t.Fatalf("grant %d: %v", i, err)
		}
	}
	if _, _, err := e.auth.Mint(context.Background(), base); !errors.Is(err, harnessauth.ErrInvalid) {
		t.Fatalf("per-client grant cap = %v", err)
	}
}

func TestExpiry(t *testing.T) {
	e := newEnv(t)
	e.enable()
	token, _, err := e.auth.Mint(context.Background(), harnessauth.MintRequest{ClientID: "claude-desktop", Workspaces: []string{"project-a"}, Operations: []harnessauth.Operation{harnessauth.OpStart}, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	e.clock.advance(time.Hour - time.Second)
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); err != nil {
		t.Fatalf("one second before expiry = %v", err)
	}
	e.clock.advance(time.Second)
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("at expiry = %v", err)
	}
	infos, _ := e.auth.List()
	if len(infos) != 1 || infos[0].Status != "expired" {
		t.Fatalf("list = %+v", infos)
	}
	if _, _, err := e.auth.Rotate(context.Background(), infos[0].ID, 0); !errors.Is(err, harnessauth.ErrInvalid) {
		t.Fatalf("rotating an expired grant = %v", err)
	}
}

func TestRotateReplacesSecretAndRevokeIsFinal(t *testing.T) {
	e := newEnv(t)
	e.enable()
	old, info := e.mint("claude-desktop", harnessauth.OpStart)
	fresh, rotated, err := e.auth.Rotate(context.Background(), info.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Revision != 2 || !rotated.ExpiresAt.Equal(info.ExpiresAt) || rotated.ID != info.ID {
		t.Fatalf("rotated = %+v original = %+v", rotated, info)
	}
	if _, err := e.auth.Verify(old, request(harnessauth.OpStart)); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("old token after rotation = %v", err)
	}
	principal, err := e.auth.Verify(fresh, request(harnessauth.OpStart))
	if err != nil || principal.GrantRevision != 2 {
		t.Fatalf("new token = %+v, %v", principal, err)
	}
	if _, _, err := e.auth.Rotate(context.Background(), info.ID, 48*time.Hour); err != nil {
		t.Fatal(err)
	}
	revoked, err := e.auth.Revoke(context.Background(), info.ID)
	if err != nil || revoked.Status != "revoked" {
		t.Fatalf("revoke = %+v, %v", revoked, err)
	}
	again, err := e.auth.Revoke(context.Background(), info.ID)
	if err != nil || again.Revision != revoked.Revision {
		t.Fatalf("revoke is not idempotent: %+v, %v", again, err)
	}
	if _, _, err := e.auth.Rotate(context.Background(), info.ID, 0); !errors.Is(err, harnessauth.ErrInvalid) {
		t.Fatalf("rotating a revoked grant = %v", err)
	}
	if _, err := e.auth.Revoke(context.Background(), strings.Repeat("0", 32)); !errors.Is(err, harnessauth.ErrNotFound) {
		t.Fatalf("unknown revoke = %v", err)
	}
	if _, err := e.auth.Revoke(context.Background(), "nope"); !errors.Is(err, harnessauth.ErrNotFound) {
		t.Fatalf("malformed revoke = %v", err)
	}
}

func TestDisableDeniesEverythingButRevokeStillWorks(t *testing.T) {
	e := newEnv(t)
	e.enable()
	token, info := e.mint("claude-desktop", harnessauth.OpStart)
	if err := e.auth.SetEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("verify while disabled = %v", err)
	}
	if _, _, err := e.auth.Rotate(context.Background(), info.ID, 0); !errors.Is(err, harnessauth.ErrDisabled) {
		t.Fatalf("rotate while disabled = %v", err)
	}
	if _, err := e.auth.Revoke(context.Background(), info.ID); err != nil {
		t.Fatalf("revoke while disabled = %v", err)
	}
	e.enable()
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("revoked grant came back after re-enable: %v", err)
	}
}

func TestWrongRootAndSymlinkSwapAreDenied(t *testing.T) {
	e := newEnv(t)
	e.enable()
	token, _ := e.mint("claude-desktop", harnessauth.OpStart)
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); err != nil {
		t.Fatal(err)
	}

	e.ws.set("project-a", e.rootB) // registry now points the workspace somewhere else
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("re-pointed workspace = %v", err)
	}

	// A path that resolves to the original root through a symlink is the same root.
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(e.rootA, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	e.ws.set("project-a", link)
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); err != nil {
		t.Fatalf("alias of the same root = %v", err)
	}

	// Swapping the registered directory for a link to another tree is not.
	swapped := filepath.Join(t.TempDir(), "swap")
	if err := os.Symlink(e.rootB, swapped); err != nil {
		t.Fatal(err)
	}
	e.ws.set("project-a", swapped)
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("symlink to another root = %v", err)
	}

	e.ws.set("project-a", e.rootA)
	if err := os.RemoveAll(e.rootA); err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("deleted root = %v", err)
	}
}

func TestUnregisteredClientAndWorkspaceInvalidateExistingGrants(t *testing.T) {
	e := newEnv(t)
	e.enable()
	token, _ := e.mint("claude-desktop", harnessauth.OpStart)
	profile, err := e.clients.Get("claude-desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.clients.Delete("claude-desktop", profile.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("deleted client profile = %v", err)
	}
}

func TestStoreFailsClosedOnUnsafeOrUnknownState(t *testing.T) {
	e := newEnv(t)
	e.enable()
	token, _ := e.mint("claude-desktop", harnessauth.OpStart)
	path := e.credentialFile()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	denied := func(name string) {
		t.Helper()
		if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); !errors.Is(err, harnessauth.ErrDenied) {
			t.Errorf("%s verify = %v", name, err)
		}
		if _, err := e.auth.Status(); !errors.Is(err, harnessauth.ErrStorage) {
			t.Errorf("%s status = %v", name, err)
		}
		if _, _, err := e.auth.Mint(context.Background(), harnessauth.MintRequest{ClientID: "claude-desktop", Workspaces: []string{"project-a"}, Operations: []harnessauth.Operation{harnessauth.OpStart}}); !errors.Is(err, harnessauth.ErrStorage) {
			t.Errorf("%s mint = %v", name, err)
		}
	}
	restore := func() {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	denied("loose permissions")
	restore()

	for name, content := range map[string]string{
		"future schema":  strings.Replace(string(original), `"schema_version": 1`, `"schema_version": 2`, 1),
		"unknown field":  strings.Replace(string(original), `"enabled": true`, `"enabled": true, "extra": 1`, 1),
		"truncated JSON": string(original[:len(original)/2]),
		"empty":          "",
	} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		denied(name)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(elsewhere, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, path); err != nil {
		t.Skip("symlinks unavailable")
	}
	denied("symlinked file")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	restore()

	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	denied("loose directory")
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.Verify(token, request(harnessauth.OpStart)); err != nil {
		t.Fatalf("restored state = %v", err)
	}

	moved := dir + ".real"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, dir); err != nil {
		t.Fatal(err)
	}
	denied("symlinked credential directory")
}

func TestCredentialFileHoldsNoSecretAndIsPrivate(t *testing.T) {
	e := newEnv(t)
	e.enable()
	token, info := e.mint("claude-desktop", harnessauth.OpStart)
	secret := strings.Split(token, ".")[2]
	content, err := os.ReadFile(e.credentialFile())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, []byte(secret)) || bytes.Contains(content, []byte(token)) {
		t.Fatal("the bearer secret was persisted")
	}
	if !bytes.Contains(content, []byte(info.ID)) || !bytes.Contains(content, []byte("secret_sha256")) {
		t.Fatalf("file lacks the grant record: %s", content)
	}
	fileInfo, _ := os.Stat(e.credentialFile())
	dirInfo, _ := os.Stat(filepath.Dir(e.credentialFile()))
	if fileInfo.Mode().Perm() != 0o600 || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("modes = %v %v", fileInfo.Mode().Perm(), dirInfo.Mode().Perm())
	}
	listed, _ := e.auth.List()
	for _, item := range listed {
		if strings.Contains(strings.Join(item.Workspaces, ","), secret) {
			t.Fatal("list leaked a secret")
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(e.credentialFile()))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") || strings.HasSuffix(entry.Name(), ".lock") {
			t.Fatalf("left behind %s", entry.Name())
		}
	}
}

func TestExistingClientProfilesAreUntouchedAndHoldNoHarnessAuthority(t *testing.T) {
	e := newEnv(t)
	profilesPath := filepath.Join(e.dir, clientprofile.RegistryFilename)
	before, err := os.ReadFile(profilesPath)
	if err != nil {
		t.Fatal(err)
	}
	// A registered client with the expanded tool profile still has no harness authority.
	if _, err := e.clients.Create(clientprofile.Input{ID: "power-user", DisplayName: "power", ClientKind: clientprofile.KindCodex, ToolProfile: clientprofile.ProfileExpanded}); err != nil {
		t.Fatal(err)
	}
	before, _ = os.ReadFile(profilesPath)
	e.enable()
	token, _ := e.mint("claude-desktop", harnessauth.OpStart)
	if _, err := e.auth.Verify(token, harnessauth.Request{ClientID: "power-user", Workspace: "project-a", Operation: harnessauth.OpStart}); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("expanded profile gained authority: %v", err)
	}
	if _, err := e.auth.Verify("", harnessauth.Request{ClientID: "power-user", Workspace: "project-a", Operation: harnessauth.OpStart}); !errors.Is(err, harnessauth.ErrDenied) {
		t.Fatalf("profile ID alone was accepted: %v", err)
	}
	after, _ := os.ReadFile(profilesPath)
	if !bytes.Equal(before, after) {
		t.Fatal("minting grants rewrote the client profile registry")
	}
	if status, err := e.auth.Status(); err != nil || !status.Enabled || status.Grants != 1 || status.Active != 1 {
		t.Fatalf("status = %+v, %v", status, err)
	}
}

func TestFreshDataDirectoryIsDisabledAndEmpty(t *testing.T) {
	e := newEnv(t)
	status, err := e.auth.Status()
	if err != nil || status.Enabled || status.Grants != 0 {
		t.Fatalf("status = %+v, %v", status, err)
	}
	if infos, err := e.auth.List(); err != nil || len(infos) != 0 {
		t.Fatalf("list = %v, %v", infos, err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "credentials")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only calls created the credential directory: %v", err)
	}
}

func TestConcurrentMutationsAreNotLost(t *testing.T) {
	e := newEnv(t)
	e.enable()
	if _, err := e.clients.Create(clientprofile.Input{ID: "third", DisplayName: "third", ClientKind: clientprofile.KindOther, ToolProfile: clientprofile.ProfileDefault}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	ids := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// A second Authority over the same directory models a second process.
			other, err := harnessauth.New(e.dir, harnessauth.Options{Clients: e.clients, Workspaces: e.ws, Now: e.clock.now})
			if err != nil {
				errs <- err
				return
			}
			client := []string{"claude-desktop", "codex-cli", "third"}[i%3]
			_, info, err := other.Mint(context.Background(), harnessauth.MintRequest{ClientID: client, Workspaces: []string{"project-a"}, Operations: []harnessauth.Operation{harnessauth.OpStart}})
			errs <- err
			if err == nil {
				ids <- info.ID
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	infos, err := e.auth.List()
	if err != nil || len(infos) != 12 {
		t.Fatalf("grants after concurrent mint = %d, %v", len(infos), err)
	}
	// A revoke racing mints must survive.
	victim := <-ids
	var race sync.WaitGroup
	for i := 0; i < 4; i++ {
		race.Add(1)
		go func(i int) {
			defer race.Done()
			other, _ := harnessauth.New(e.dir, harnessauth.Options{Clients: e.clients, Workspaces: e.ws, Now: e.clock.now})
			if i == 0 {
				_, _ = other.Revoke(context.Background(), victim)
				return
			}
			_, _, _ = other.Mint(context.Background(), harnessauth.MintRequest{ClientID: "third", Workspaces: []string{"project-a"}, Operations: []harnessauth.Operation{harnessauth.OpStatus}})
		}(i)
	}
	race.Wait()
	infos, _ = e.auth.List()
	for _, info := range infos {
		if info.ID == victim && info.Status != "revoked" {
			t.Fatal("a concurrent write lost the revocation")
		}
	}
}

func TestStaleLockIsReclaimedAndLiveLockBlocks(t *testing.T) {
	e := newEnv(t)
	e.enable()
	lock := filepath.Join(e.dir, "credentials", "harness-authority.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if err := e.auth.SetEnabled(context.Background(), true); err != nil {
		t.Fatalf("stale lock was not reclaimed: %v", err)
	}
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := e.auth.SetEnabled(ctx, true); err == nil {
		t.Fatal("a live lock did not block the writer")
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	if _, err := harnessauth.New("", harnessauth.Options{}); !errors.Is(err, harnessauth.ErrInvalid) {
		t.Fatalf("empty options = %v", err)
	}
}

func TestPrincipalListsGrantedOperationsAndActiveTracksTheGrant(t *testing.T) {
	e := newEnv(t)
	e.enable()
	token, info := e.mint("claude-desktop", harnessauth.OpStart, harnessauth.OpStatus)
	principal, err := e.auth.Verify(token, request(harnessauth.OpStatus))
	if err != nil || len(principal.Operations) != 2 || principal.Operations[0] != harnessauth.OpStart || principal.Operations[1] != harnessauth.OpStatus {
		t.Fatalf("principal = %+v, %v", principal, err)
	}
	principal.Operations[0] = "tampered"
	again, _ := e.auth.Verify(token, request(harnessauth.OpStatus))
	if again.Operations[0] != harnessauth.OpStart {
		t.Fatal("a caller mutated the stored operation list")
	}

	if !e.auth.Active(info.ID, "project-a") {
		t.Fatal("a live grant reported inactive")
	}
	if e.auth.Active(info.ID, "project-b") || e.auth.Active(info.ID, "") || e.auth.Active("nope", "project-a") || e.auth.Active(strings.Repeat("0", 32), "project-a") {
		t.Fatal("Active accepted an unbound workspace or unknown grant")
	}
	// Rotation keeps the same grant alive: running work must not be killed by it.
	if _, _, err := e.auth.Rotate(context.Background(), info.ID, 0); err != nil {
		t.Fatal(err)
	}
	if !e.auth.Active(info.ID, "project-a") {
		t.Fatal("rotation ended a running grant")
	}
	e.ws.set("project-a", e.rootB)
	if e.auth.Active(info.ID, "project-a") {
		t.Fatal("a changed root left the grant active")
	}
	e.ws.set("project-a", e.rootA)
	if !e.auth.Active(info.ID, "project-a") {
		t.Fatal("restoring the root did not restore the grant")
	}
	if err := e.auth.SetEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if e.auth.Active(info.ID, "project-a") {
		t.Fatal("a disabled pack left the grant active")
	}
	e.enable()
	if _, err := e.auth.Revoke(context.Background(), info.ID); err != nil {
		t.Fatal(err)
	}
	if e.auth.Active(info.ID, "project-a") {
		t.Fatal("a revoked grant is active")
	}
	other, otherInfo := e.mint("codex-cli", harnessauth.OpStart)
	_ = other
	profile, _ := e.clients.Get("codex-cli")
	if err := e.clients.Delete("codex-cli", profile.Revision); err != nil {
		t.Fatal(err)
	}
	if e.auth.Active(otherInfo.ID, "project-a") {
		t.Fatal("a deleted client profile left the grant active")
	}
	e.clock.advance(48 * time.Hour)
	third, thirdInfo := e.mint("claude-desktop", harnessauth.OpStart)
	_ = third
	e.clock.advance(harnessauth.DefaultTTL + time.Second)
	if e.auth.Active(thirdInfo.ID, "project-a") {
		t.Fatal("an expired grant is active")
	}
}
