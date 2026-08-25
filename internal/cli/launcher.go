package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/pkg/browser"
)

// BrowserOpener opens a URL in the operator's browser.
type BrowserOpener func(url string) error

// ClipboardWriter puts text on the operator's clipboard. It takes a context
// because the real implementation runs another program, and an operator who
// interrupts the command should not be left waiting on a wedged one.
type ClipboardWriter func(ctx context.Context, text string) error

// TerminalCheck reports whether a command's output reaches a person.
type TerminalCheck func(writer io.Writer) bool

// Desktop reaches the machine the operator is sitting at: their browser, their
// clipboard, and whether anyone is watching the output at all.
//
// Every field is optional, and a nil field selects the real implementation. The
// three travel together because they answer one question — what this run can do
// beyond writing text — and a test that wants to observe one usually wants to
// fix the others too.
type Desktop struct {
	OpenBrowser     BrowserOpener
	CopyToClipboard ClipboardWriter
	IsTerminal      TerminalCheck
}

// ErrNoClipboard reports a machine with no clipboard command this build knows
// how to drive. It names the alternative, because piping the URL is what an
// operator would have done anyway and the command still printed it.
var ErrNoClipboard = errors.New(
	"no clipboard command was found on this machine; pipe the link instead, " +
		"for example `starport auth url | pbcopy`",
)

func browserOpener(deps Dependencies) BrowserOpener {
	if deps.Desktop.OpenBrowser != nil {
		return deps.Desktop.OpenBrowser
	}
	return browser.OpenURL
}

func clipboardWriter(deps Dependencies) ClipboardWriter {
	if deps.Desktop.CopyToClipboard != nil {
		return deps.Desktop.CopyToClipboard
	}
	return copyToClipboard
}

func terminalCheck(deps Dependencies) TerminalCheck {
	if deps.Desktop.IsTerminal != nil {
		return deps.Desktop.IsTerminal
	}
	return writerIsTerminal
}

// writerIsTerminal reports whether a command's own output goes to a terminal.
//
// It asks about the writer the command writes to rather than about os.Stdout,
// because those differ exactly where the answer matters: a redirected or piped
// run is the case a launch link must not be spent on a browser nobody is
// watching.
func writerIsTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd())
}

// clipboardCommands lists the clipboard writers this build knows, per platform,
// in the order to try them. The list is fixed at compile time: nothing a caller
// supplies reaches exec, and a machine with none of them gets ErrNoClipboard
// rather than a guess.
func clipboardCommands() [][]string {
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"pbcopy"}}
	case "windows":
		return [][]string{{"clip"}}
	default:
		// Wayland first: a Wayland session usually also answers to xclip through
		// XWayland, but wl-copy is the one that works when XWayland is absent.
		return [][]string{
			{"wl-copy"},
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
		}
	}
}

func copyToClipboard(ctx context.Context, text string) error {
	for _, candidate := range clipboardCommands() {
		path, err := exec.LookPath(candidate[0])
		if err != nil {
			continue
		}
		// #nosec G204 -- path comes from LookPath over the fixed table above,
		// and the text is written to stdin rather than passed as an argument.
		command := exec.CommandContext(ctx, path, candidate[1:]...)
		command.Stdin = strings.NewReader(text)
		if err := command.Run(); err != nil {
			return fmt.Errorf("copy to the clipboard with %s: %w", candidate[0], err)
		}
		return nil
	}
	return ErrNoClipboard
}
