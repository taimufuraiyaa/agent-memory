package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
)

type jevCredentialStore interface {
	Configured(context.Context) (bool, error)
	Save(context.Context, string) error
}

func shouldPromptJevSetup(inputTerminal, outputTerminal bool, format string, dryRun, hooksOnly bool) bool {
	return inputTerminal && outputTerminal && !dryRun && !hooksOnly && format != "json"
}

func promptJevSetup(ctx context.Context, store jevCredentialStore, input io.Reader, output io.Writer, readSecret func() (string, error)) error {
	configured, err := store.Configured(ctx)
	if err != nil {
		return errors.New("cannot inspect Jev credential")
	}
	if configured {
		return nil
	}
	_, _ = fmt.Fprint(output, "Set up Jev access token now? [y/N]: ")
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return errors.New("cannot read Jev setup choice")
	}
	choice := strings.TrimSpace(answer)
	if choice != "y" && !strings.EqualFold(choice, "yes") {
		_, _ = fmt.Fprintln(output, "Jev setup skipped; use the TUI later if needed.")
		return nil
	}
	_, _ = fmt.Fprint(output, "Jev access token (hidden): ")
	token, err := readSecret()
	_, _ = fmt.Fprintln(output)
	if err != nil {
		return errors.New("cannot read Jev access token")
	}
	if err := store.Save(ctx, token); err != nil {
		return errors.New("cannot store Jev access token")
	}
	_, _ = fmt.Fprintln(output, "Jev token stored locally (connectivity not verified).")
	return nil
}

func runInteractiveJevSetup(ctx context.Context, dataDir string, input *os.File, output io.Writer) error {
	return promptJevSetup(ctx, jevconfig.NewTokenStore(dataDir), input, output, func() (string, error) {
		secret, err := term.ReadPassword(input.Fd())
		if err != nil {
			return "", err
		}
		token := string(secret)
		for i := range secret {
			secret[i] = 0
		}
		return token, nil
	})
}
