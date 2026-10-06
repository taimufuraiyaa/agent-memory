//go:build unix

package harnessexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The tests run this very test binary as the command: with HARNESS_HELPER set it behaves as
// the program described by HARNESS_MODE instead of running tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("HARNESS_HELPER"); mode != "" {
		helper(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helper(mode string) {
	switch mode {
	case "echo":
		fmt.Println("hello out")
		fmt.Fprintln(os.Stderr, "hello err")
	case "exit3":
		fmt.Println("about to fail")
		os.Exit(3)
	case "sleep":
		time.Sleep(time.Minute)
	case "env":
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "HARNESS_") {
				fmt.Println(e)
			}
		}
	case "pwd":
		wd, _ := os.Getwd()
		fmt.Println(wd)
	case "stdin":
		buf := make([]byte, 16)
		n, err := os.Stdin.Read(buf)
		fmt.Printf("read %d bytes, err=%v\n", n, err)
	case "flood":
		line := strings.Repeat("x", 99) + "\n"
		for i := 0; i < 20000; i++ {
			fmt.Print(line)
		}
		fmt.Println("THE END")
	case "headtail":
		fmt.Println("FIRST LINE")
		for i := 0; i < 5000; i++ {
			fmt.Println("filler filler filler filler")
		}
		fmt.Println("LAST LINE")
	case "control":
		fmt.Print("plain \x1b[31mred\x1b[0m \x07bell \x1b]0;title\x07 back\rspace ‮ rtl \xff\xfe bad\x00nul\n")
	case "child":
		// Start a child that outlives this process unless the group is killed, and report its pid.
		child := exec.Command(os.Args[0])
		child.Env = []string{"HARNESS_HELPER=sleep"}
		if err := child.Start(); err != nil {
			fmt.Println("child failed:", err)
			os.Exit(9)
		}
		fmt.Println("child", child.Process.Pid)
	case "child-and-hang":
		child := exec.Command(os.Args[0])
		child.Env = []string{"HARNESS_HELPER=sleep"}
		_ = child.Start()
		fmt.Println("child", child.Process.Pid)
		time.Sleep(time.Minute)
	case "ignore-term":
		signalIgnore()
		fmt.Println("ignoring")
		time.Sleep(time.Minute)
	case "term-handler":
		trap := make(chan os.Signal, 1)
		signal.Notify(trap, syscall.SIGTERM)
		fmt.Println("ready")
		<-trap
		fmt.Println("terminated gracefully")
		os.Exit(5)
	case "self-kill":
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	}
}

func signalIgnore() {
	ignoreTerm()
}

func program(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func realDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func spec(t *testing.T, mode string, extra ...string) Spec {
	t.Helper()
	return Spec{Program: program(t), Dir: realDir(t), Env: append([]string{"HARNESS_HELPER=" + mode}, extra...), Timeout: 10 * time.Second, MaxOutput: 8 << 10}
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func childPID(t *testing.T, output string) int {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "child ") {
			pid, err := strconv.Atoi(strings.TrimPrefix(line, "child "))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
	}
	t.Fatalf("no child pid in %q", output)
	return 0
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		// A killed child is reparented and reaped by init, but may linger as a zombie briefly.
		if !alive(pid) {
			return
		}
		if out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output(); strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			return
		}
	}
	t.Fatalf("process %d is still alive", pid)
}

func TestARunReturnsOutputAndTheExitStatus(t *testing.T) {
	res, err := Run(context.Background(), spec(t, "echo"))
	if err != nil || !res.Started || res.ExitCode != 0 || res.TimedOut || res.Cancelled || res.Signal != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(res.Output, "hello out") || !strings.Contains(res.Output, "hello err") || res.Duration <= 0 {
		t.Fatalf("output = %q", res.Output)
	}
	// A failing command is a result, not an error.
	res, err = Run(context.Background(), spec(t, "exit3"))
	if err != nil || res.ExitCode != 3 || !strings.Contains(res.Output, "about to fail") {
		t.Fatalf("%+v %v", res, err)
	}
	// Death by a signal is reported as one.
	res, err = Run(context.Background(), spec(t, "self-kill"))
	if err != nil || res.ExitCode != -1 || res.Signal != "killed" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestOnlyTheGivenEnvironmentReachesTheProcessAndItHasNoInput(t *testing.T) {
	t.Setenv("SECRET_API_TOKEN", "do-not-leak")
	t.Setenv("OPENAI_API_KEY", "sk-do-not-leak")
	res, err := Run(context.Background(), spec(t, "env", "ONLY_THIS=1", "PATH=/usr/bin:/bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"SECRET_API_TOKEN", "OPENAI_API_KEY", "do-not-leak", "HOME=", "USER="} {
		if strings.Contains(res.Output, leaked) {
			t.Errorf("the environment leaked %q: %q", leaked, res.Output)
		}
	}
	if !strings.Contains(res.Output, "ONLY_THIS=1") || !strings.Contains(res.Output, "PATH=/usr/bin:/bin") {
		t.Fatalf("the given environment is missing: %q", res.Output)
	}
	// Standard input is the null device: a read ends at once, it never waits for a person.
	in, err := Run(context.Background(), spec(t, "stdin"))
	if err != nil || !strings.Contains(in.Output, "read 0 bytes, err=EOF") {
		t.Fatalf("stdin = %q %v", in.Output, err)
	}
}

func TestTheProcessRunsInTheGivenDirectory(t *testing.T) {
	s := spec(t, "pwd")
	res, err := Run(context.Background(), s)
	if err != nil || strings.TrimSpace(res.Output) != s.Dir {
		t.Fatalf("pwd = %q (%v), want %q", res.Output, err, s.Dir)
	}
}

func TestATimeoutStopsTheWholeGroupAndSaysSo(t *testing.T) {
	s := spec(t, "child-and-hang")
	s.Timeout = 400 * time.Millisecond
	began := time.Now()
	res, err := Run(context.Background(), s)
	if err != nil || !res.TimedOut || res.Cancelled || time.Since(began) > 5*time.Second {
		t.Fatalf("%+v %v after %v", res, err, time.Since(began))
	}
	waitDead(t, childPID(t, res.Output))
}

func TestAProcessThatIgnoresTerminateIsKilledAfterTheGrace(t *testing.T) {
	s := spec(t, "ignore-term")
	s.Timeout, s.Grace = 300*time.Millisecond, 300*time.Millisecond
	began := time.Now()
	res, err := Run(context.Background(), s)
	if err != nil || !res.TimedOut || res.Signal != "killed" || time.Since(began) > 5*time.Second {
		t.Fatalf("%+v %v after %v", res, err, time.Since(began))
	}
}

func TestCancellationStopsTheCommandAndIsNotATimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	s := spec(t, "sleep")
	began := time.Now()
	res, err := Run(ctx, s)
	if err != nil || !res.Cancelled || res.TimedOut || time.Since(began) > 5*time.Second {
		t.Fatalf("%+v %v after %v", res, err, time.Since(began))
	}
	// Already cancelled before it starts: it must not run to completion.
	done, stop := context.WithCancel(context.Background())
	stop()
	began = time.Now()
	res, err = Run(done, spec(t, "sleep"))
	if err != nil || !res.Cancelled || res.Started || time.Since(began) > time.Second {
		t.Fatalf("pre-cancelled: %+v %v after %v", res, err, time.Since(began))
	}
}

func TestAChildLeftBehindByAFinishedCommandIsKilled(t *testing.T) {
	res, err := Run(context.Background(), spec(t, "child"))
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	waitDead(t, childPID(t, res.Output))
}

func TestOutputIsBoundedKeepsBothEndsAndCountsWhatItDropped(t *testing.T) {
	s := spec(t, "headtail")
	res, err := Run(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Output) > s.MaxOutput+100 || res.OmittedBytes <= 0 || !strings.Contains(res.Output, "FIRST LINE") || !strings.Contains(res.Output, "LAST LINE") ||
		!strings.Contains(res.Output, fmt.Sprintf("…[%d bytes omitted]…", res.OmittedBytes)) {
		t.Fatalf("%d bytes kept, %d omitted; head/tail present: %v %v", len(res.Output), res.OmittedBytes, strings.Contains(res.Output, "FIRST LINE"), strings.Contains(res.Output, "LAST LINE"))
	}
	// A flood is cut, quickly, and the end survives.
	flood := spec(t, "flood")
	began := time.Now()
	res, err = Run(context.Background(), flood)
	if err != nil || res.OmittedBytes < 1_900_000 || !strings.Contains(res.Output, "THE END") || len(res.Output) > flood.MaxOutput+100 || time.Since(began) > 5*time.Second {
		t.Fatalf("flood: %d kept, %d omitted, %v", len(res.Output), res.OmittedBytes, time.Since(began))
	}
	// Output that fits is returned whole with nothing omitted.
	small, _ := Run(context.Background(), spec(t, "echo"))
	if small.OmittedBytes != 0 || strings.Contains(small.Output, "omitted") {
		t.Fatalf("small output = %+v", small)
	}
}

func TestOutputCarriesNoControlCharactersOrEscapeSequences(t *testing.T) {
	res, err := Run(context.Background(), spec(t, "control"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"\x1b", "\x07", "\r", "\x00", "‮", "\xff"} {
		if strings.Contains(res.Output, bad) {
			t.Errorf("output still carries %q: %q", bad, res.Output)
		}
	}
	if !strings.Contains(res.Output, "plain red ") || !strings.Contains(res.Output, "⟨U+202E⟩") || strings.Contains(res.Output, "[31m") {
		t.Fatalf("output = %q", res.Output)
	}
}

func TestSpecsThatCouldEscapeTheEnvelopeAreRefusedBeforeAnythingStarts(t *testing.T) {
	good := spec(t, "echo")
	link := filepath.Join(realDir(t), "link")
	if err := os.Symlink(good.Dir, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(good.Dir, "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	many := make([]string, maxArgs+1)
	manyEnv := make([]string, maxEnv+1)
	for i := range manyEnv {
		manyEnv[i] = fmt.Sprintf("K%d=v", i)
	}
	mutate := func(change func(*Spec)) Spec {
		s := good
		s.Env = append([]string(nil), good.Env...)
		change(&s)
		return s
	}
	for name, s := range map[string]Spec{
		"a relative program": mutate(func(s *Spec) { s.Program = "echo" }),
		"an unclean program": mutate(func(s *Spec) {
			s.Program = filepath.Dir(s.Program) + "/../" + filepath.Base(filepath.Dir(s.Program)) + "/" + filepath.Base(s.Program)
		}),
		"a missing program":      mutate(func(s *Spec) { s.Program = "/nonexistent/program" }),
		"a non-executable file":  mutate(func(s *Spec) { s.Program = file }),
		"a directory as program": mutate(func(s *Spec) { s.Program = good.Dir }),
		"a relative directory":   mutate(func(s *Spec) { s.Dir = "." }),
		"a linked directory":     mutate(func(s *Spec) { s.Dir = link }),
		"a file as directory":    mutate(func(s *Spec) { s.Dir = file }),
		"a missing directory":    mutate(func(s *Spec) { s.Dir = filepath.Join(s.Dir, "nope") }),
		"no timeout":             mutate(func(s *Spec) { s.Timeout = 0 }),
		"too long a timeout":     mutate(func(s *Spec) { s.Timeout = MaxTimeout + time.Second }),
		"a tiny output cap":      mutate(func(s *Spec) { s.MaxOutput = 10 }),
		"a huge output cap":      mutate(func(s *Spec) { s.MaxOutput = MaxOutputCap + 1 }),
		"too many arguments":     mutate(func(s *Spec) { s.Args = many }),
		"NUL in an argument":     mutate(func(s *Spec) { s.Args = []string{"a\x00b"} }),
		"too many bytes of arguments": mutate(func(s *Spec) {
			s.Args = []string{strings.Repeat("x", maxArgBytes/2+1), strings.Repeat("y", maxArgBytes/2+1)}
		}),
		"too many env entries":  mutate(func(s *Spec) { s.Env = manyEnv }),
		"a malformed env entry": mutate(func(s *Spec) { s.Env = []string{"NOEQUALS"} }),
		"an env entry with NUL": mutate(func(s *Spec) { s.Env = []string{"A=b\x00c"} }),
		"an env entry no name":  mutate(func(s *Spec) { s.Env = []string{"=x"} }),
		"a huge environment":    mutate(func(s *Spec) { s.Env = []string{"A=" + strings.Repeat("x", maxEnvBytes)} }),
	} {
		if res, err := Run(context.Background(), s); !errors.Is(err, ErrInvalid) || res.Started {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
}

func TestAProgramThatCannotStartIsAnErrorWithNoPathInIt(t *testing.T) {
	dir := realDir(t)
	broken := filepath.Join(dir, "broken")
	if err := os.WriteFile(broken, []byte("#!/nonexistent/interpreter\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), Spec{Program: broken, Dir: dir, Env: []string{"A=b"}, Timeout: time.Second, MaxOutput: 4096})
	if !errors.Is(err, ErrStart) || res.Started || strings.Contains(err.Error(), dir) {
		t.Fatalf("%+v %v", res, err)
	}
}

// A timeout asks politely first: the process gets a terminate signal and a chance to exit and
// report, and is killed only if it does not.
func TestATimeoutSendsTerminateFirstSoTheProcessCanExitGracefully(t *testing.T) {
	s := spec(t, "term-handler")
	s.Timeout = 500 * time.Millisecond
	res, err := Run(context.Background(), s)
	if err != nil || !res.TimedOut || res.ExitCode != 5 || res.Signal != "" || !strings.Contains(res.Output, "terminated gracefully") {
		t.Fatalf("%+v %v", res, err)
	}
}

// A relative program path is refused even when it names a real file in the working directory
// of the caller: nothing is ever looked up relative to wherever the process happens to be.
func TestARelativeProgramIsRefusedEvenWhenItExistsHere(t *testing.T) {
	dir := realDir(t)
	copied := filepath.Join(dir, "prog")
	data, err := os.ReadFile(program(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	s := spec(t, "echo")
	s.Program = "prog"
	if res, err := Run(context.Background(), s); !errors.Is(err, ErrInvalid) || res.Started {
		t.Fatalf("%+v %v", res, err)
	}
	s.Program = "./prog"
	if _, err := Run(context.Background(), s); !errors.Is(err, ErrInvalid) {
		t.Fatalf("./prog = %v", err)
	}
}

func TestACaptureKeepsTheFirstThirdAndTheLastTwoThirdsWhateverTheWriteSizes(t *testing.T) {
	data := make([]byte, 3000)
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	for name, chunks := range map[string][]int{"one big write": {3000}, "many small writes": {7, 7, 7, 7, 7, 7, 7, 7}, "mixed": {500, 1, 2000, 3, 496}} {
		c := newCapture(900) // head 300, tail 600
		offset, total := 0, 0
		for _, n := range chunks {
			if total+n > len(data) {
				n = len(data) - total
			}
			if written, err := c.Write(data[offset : offset+n]); err != nil || written != n {
				t.Fatal(written, err)
			}
			offset, total = offset+n, total+n
		}
		kept, omitted := c.result()
		wantHead, wantTail := string(data[:min(300, total)]), string(data[max(0, total-600):total])
		if total <= 900 {
			if string(kept) != string(data[:total]) || omitted != 0 {
				t.Errorf("%s: small total altered: %d kept, %d omitted", name, len(kept), omitted)
			}
			continue
		}
		if omitted != int64(total-900) || !strings.HasPrefix(string(kept), wantHead) || !strings.HasSuffix(string(kept), wantTail) {
			t.Errorf("%s: omitted %d (want %d); head ok %v tail ok %v", name, omitted, total-900, strings.HasPrefix(string(kept), wantHead), strings.HasSuffix(string(kept), wantTail))
		}
	}
}
