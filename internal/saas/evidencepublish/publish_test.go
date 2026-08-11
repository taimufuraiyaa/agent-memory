package evidencepublish

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestJSONPublishesCreateOnlyMode0600Receipt(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "receipt.json")
	value := struct {
		Status string `json:"status"`
	}{Status: "ready"}
	if err := JSON(path, value, ".receipt-*"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode = %v, want regular 0600", info.Mode())
	}
	var decoded struct {
		Status string `json:"status"`
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &decoded); err != nil || decoded.Status != "ready" {
		t.Fatalf("decoded receipt = %+v, err = %v", decoded, err)
	}
	if err := JSON(path, value, ".receipt-*"); err == nil {
		t.Fatal("existing receipt was overwritten")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "receipt.json" {
		t.Fatalf("publication leftovers = %v", entries)
	}
}

func TestJSONRejectsSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkDirectory := filepath.Join(root, "linked")
	if err := os.Symlink(realDirectory, symlinkDirectory); err != nil {
		t.Fatal(err)
	}
	if err := JSON(filepath.Join(symlinkDirectory, "receipt.json"), struct{}{}, ".receipt-*"); err == nil {
		t.Fatal("symlinked parent directory was accepted")
	}
	if _, err := os.Lstat(filepath.Join(realDirectory, "receipt.json")); !os.IsNotExist(err) {
		t.Fatalf("receipt was redirected through symlink: %v", err)
	}
}

func TestJSONRejectsParentReplacementAfterRootOpen(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "receipts")
	redirect := filepath.Join(root, "redirect")
	moved := filepath.Join(root, "opened")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(redirect, 0o700); err != nil {
		t.Fatal(err)
	}
	err := jsonWithHooks(filepath.Join(directory, "receipt.json"), struct{}{}, ".receipt-*", func() {
		if err := os.Rename(directory, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(redirect, directory); err != nil {
			t.Fatal(err)
		}
	}, nil)
	if err == nil {
		t.Fatal("replaced parent directory was accepted")
	}
	for _, path := range []string{filepath.Join(redirect, "receipt.json"), filepath.Join(moved, "receipt.json")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("receipt or leftover exists at %s: %v", path, err)
		}
	}
}

func TestJSONCleansLinkedReceiptWhenParentChangesBeforeSuccess(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "receipts")
	redirect := filepath.Join(root, "redirect")
	moved := filepath.Join(root, "opened")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(redirect, 0o700); err != nil {
		t.Fatal(err)
	}
	err := jsonWithHooks(filepath.Join(directory, "receipt.json"), struct{}{}, ".receipt-*", nil, func() {
		if err := os.Rename(directory, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(redirect, directory); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("parent replacement after linking was accepted")
	}
	for _, target := range []string{redirect, moved} {
		entries, err := os.ReadDir(target)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("publication leftovers in %s: %v", target, entries)
		}
	}
}
