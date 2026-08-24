package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/localevidence"
)

const maximumJSONBytes = 1 << 20

type metadataFile struct {
	RunID       string                `json:"run_id"`
	Profile     string                `json:"profile"`
	GitCommit   string                `json:"git_commit"`
	GitDirty    bool                  `json:"git_dirty"`
	StartedAt   time.Time             `json:"started_at"`
	CompletedAt time.Time             `json:"completed_at"`
	Checks      []localevidence.Check `json:"checks"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("agent-memory-local-evidence", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", "", "Root directory containing the local evidence receipts")
	metadataPath := flags.String("metadata", "", "Metadata JSON used to build a manifest")
	verifyPath := flags.String("verify", "", "Existing manifest JSON to verify")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *root == "" || (*metadataPath == "") == (*verifyPath == "") {
		fmt.Fprintln(stderr, "root and exactly one of metadata or verify are required")
		return 2
	}
	if err := validateRoot(*root); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *metadataPath != "" {
		var input metadataFile
		if err := readRegularJSON(*metadataPath, &input); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		manifest, err := localevidence.Build(*root, localevidence.Metadata{
			RunID: input.RunID, Profile: input.Profile, GitCommit: input.GitCommit,
			GitDirty: input.GitDirty, StartedAt: input.StartedAt,
			CompletedAt: input.CompletedAt, Checks: input.Checks,
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(manifest); err != nil {
			fmt.Fprintln(stderr, "encode local evidence manifest")
			return 1
		}
		return 0
	}
	var manifest localevidence.Manifest
	if err := readRegularJSON(*verifyPath, &manifest); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := localevidence.Validate(*root, manifest); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "local evidence manifest verified")
	return 0
}

func validateRoot(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("evidence root must be a directory and not a symlink")
	}
	return nil
}

func readRegularJSON(path string, destination any) error {
	return readRegularJSONWithHook(path, destination, nil)
}

func readRegularJSONWithHook(path string, destination any, afterOpen func()) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 2 || info.Size() > maximumJSONBytes {
		return errors.New("JSON input must be a bounded regular non-symlink file")
	}
	handle, err := os.Open(filepath.Clean(path))
	if err != nil {
		return errors.New("open JSON input")
	}
	defer handle.Close()
	opened, err := handle.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() != info.Size() || !opened.ModTime().Equal(info.ModTime()) {
		return errors.New("JSON input changed before it was opened")
	}
	if afterOpen != nil {
		afterOpen()
	}
	data, err := io.ReadAll(io.LimitReader(handle, maximumJSONBytes+1))
	if err != nil || int64(len(data)) != opened.Size() || len(data) > maximumJSONBytes {
		return errors.New("read JSON input")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("decode JSON input")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("JSON input has trailing data")
	}
	openedAfterRead, err := handle.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return errors.New("JSON input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return errors.New("JSON input changed while reading")
	}
	return nil
}
