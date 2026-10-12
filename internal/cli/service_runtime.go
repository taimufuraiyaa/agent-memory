package cli

import (
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func resolveServiceRuntime(flags commonFlags) (runtimeConfig, error) {
	base, err := defaultDBBaseDir()
	if err != nil {
		return runtimeConfig{}, err
	}
	dbPath := strings.TrimSpace(flags.dbPath)
	if dbPath == "" {
		dbPath = filepath.Join(base, ".serve-placeholder.db")
	}
	modelDir := strings.TrimSpace(flags.modelDir)
	if modelDir == "" {
		home, _ := os.UserHomeDir()
		modelDir = embeddings.DefaultModelDir(home)
	}
	return runtimeConfig{dbPath: dbPath, modelDir: modelDir, apiURL: resolveAPIURL(flags.apiURL)}, nil
}

func apiURLForListenerAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://localhost:3211"
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err == nil {
		return true
	}
	return false
}

func stopProcess(pid int) error {
	if pid <= 0 {
		return errors.New("pid is required")
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = p.Signal(syscall.SIGTERM)
		time.Sleep(250 * time.Millisecond)
		if isProcessAlive(pid) {
			_ = p.Kill()
		}
		return nil
	}
	if err := p.Kill(); err != nil {
		return err
	}
	return nil
}

func validateServeStartAddr(addr string) error {
	if err := validateLocalListenAddr(addr); err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") && len(addr) > 1 && addr != ":0" {
			return nil
		}
		_ = host
		return fmt.Errorf("invalid addr: %s", addr)
	}
	if port == "0" {
		return errors.New("addr cannot use port 0 with --start (pick a fixed port)")
	}
	return nil
}

func validateLocalListenAddr(addr string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return fmt.Errorf("invalid addr %q: %w", addr, err)
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("addr %q is not loopback; standalone services only listen on localhost", addr)
	}
	return nil
}
