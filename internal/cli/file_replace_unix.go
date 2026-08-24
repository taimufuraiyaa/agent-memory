//go:build !windows

package cli

import "os"

func atomicReplaceFile(src, dst string) error {
	return os.Rename(src, dst)
}
