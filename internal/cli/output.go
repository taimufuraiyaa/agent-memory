package cli

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

const (
	formatJSON    = "json"
	formatRaw     = "raw"
	envelopeMajor = "v1"
)

type envelope struct {
	OK      bool          `json:"ok"`
	Command string        `json:"command,omitempty"`
	Version string        `json:"version"`
	Data    any           `json:"data,omitempty"`
	Error   *errorPayload `json:"error,omitempty"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func normalizeFormat(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", formatJSON:
		return formatJSON
	case formatRaw:
		return formatRaw
	default:
		return ""
	}
}

func writeSuccessEnvelope(out io.Writer, command string, data any) error {
	return writeJSON(out, envelope{
		OK:      true,
		Command: command,
		Version: envelopeMajor,
		Data:    data,
	})
}

func writeErrorEnvelope(out io.Writer, command, message string) error {
	return writeJSON(out, envelope{
		OK:      false,
		Command: command,
		Version: envelopeMajor,
		Error: &errorPayload{
			Code:    exitCodeName(mapExitCode(message)),
			Message: message,
		},
	})
}

func writeJSON(out io.Writer, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = out.Write(b)
	return err
}

func outputFormatFromArgs(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--format=") {
			return normalizeFormat(strings.TrimPrefix(arg, "--format="))
		}
		if arg == "--format" || arg == "-f" {
			if i+1 < len(args) {
				return normalizeFormat(args[i+1])
			}
		}
	}
	return formatJSON
}

func commandFromArgs(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return strings.TrimSpace(arg)
	}
	return ""
}

func validateOutputFormat(value string, allowRaw bool) error {
	n := normalizeFormat(value)
	if n == "" {
		return errors.New("invalid format: allowed values are json|raw")
	}
	if !allowRaw && n == formatRaw {
		return errors.New("invalid format: raw is only supported by the recall command")
	}
	return nil
}

func currentOutputFormat() string {
	return outputFormatFromArgs(os.Args[1:])
}
