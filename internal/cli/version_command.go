package cli

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

const productVersion = "0.7"

type versionInfo struct {
	Binary   string `json:"binary"`
	Version  string `json:"version"`
	Module   string `json:"module"`
	Revision string `json:"revision,omitempty"`
	Time     string `json:"time,omitempty"`
	Modified bool   `json:"modified"`
	Go       string `json:"go"`
	Platform string `json:"platform"`
	Path     string `json:"path,omitempty"`
}

func collectVersionInfo() versionInfo {
	info := versionInfo{
		Binary:   "agent-memory",
		Version:  productVersion,
		Go:       runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	if exe, err := os.Executable(); err == nil {
		info.Path = exe
	}

	if bi, ok := debug.ReadBuildInfo(); ok && bi != nil {
		if strings.TrimSpace(bi.Main.Path) != "" {
			info.Module = bi.Main.Path
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				info.Revision = s.Value
			case "vcs.time":
				info.Time = s.Value
			case "vcs.modified":
				info.Modified = strings.EqualFold(s.Value, "true")
			}
		}
	}

	return info
}

func validateVersionFormat(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "text":
		return "text", nil
	case "json":
		return "json", nil
	default:
		return "", fmt.Errorf("invalid format: allowed values are json|text")
	}
}

func newVersionCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version/build information",
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := validateVersionFormat(format)
			if err != nil {
				return err
			}
			v := collectVersionInfo()
			if f == "json" {
				return writeSuccessEnvelope(cmd.OutOrStdout(), "version", v)
			}
			w := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(w, "%s %s\n", v.Binary, v.Version)
			if v.Module != "" {
				_, _ = fmt.Fprintf(w, "module: %s\n", v.Module)
			}
			if v.Revision != "" {
				_, _ = fmt.Fprintf(w, "revision: %s\n", v.Revision)
			}
			if v.Time != "" {
				_, _ = fmt.Fprintf(w, "time: %s\n", v.Time)
			}
			_, _ = fmt.Fprintf(w, "modified: %v\n", v.Modified)
			_, _ = fmt.Fprintf(w, "go: %s\n", v.Go)
			_, _ = fmt.Fprintf(w, "platform: %s\n", v.Platform)
			if v.Path != "" {
				_, _ = fmt.Fprintf(w, "path: %s\n", v.Path)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: json|text")
	return cmd
}
