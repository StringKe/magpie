package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"

	"github.com/yetone/magpie/internal/provider"
)

const accountsRunUsage = "usage: magpie accounts run claude <account> [--cwd <dir>] [-- <claude arguments>…]"

// accountsRunCmd: `magpie accounts run claude <account> [--cwd <dir>] [--
// <claude arguments>…]` starts Claude Code on a saved Claude account
// beside the Claude Code signed in to the user's own (#992), in the
// account's own config directory (provider.RunClaudeAccount), and ends
// with Claude Code's exit code. What follows -- is Claude Code's, as it
// came.
func accountsRunCmd(args []string) error {
	var pass []string
	if i := slices.Index(args, "--"); i >= 0 {
		args, pass = args[:i], args[i+1:]
	}
	cwd, hasCwd := "", false
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--cwd":
			if i+1 == len(args) {
				return fmt.Errorf("--cwd needs a folder\n%s", accountsRunUsage)
			}
			cwd, hasCwd = args[i+1], true
			i++
		case strings.HasPrefix(a, "--cwd="):
			cwd, hasCwd = strings.TrimPrefix(a, "--cwd="), true
		case strings.HasPrefix(a, "-"):
			// never taken for an account: Claude Code's own go after --
			return fmt.Errorf("unknown %s · Claude Code's own arguments go after --\n%s", a, accountsRunUsage)
		default:
			words = append(words, a)
		}
	}
	if len(words) != 2 {
		return fmt.Errorf("%s", accountsRunUsage)
	}
	switch strings.ToLower(words[0]) {
	case "claude", "cc":
	default:
		return fmt.Errorf("%q: only Claude Code is run on a saved account for now\n%s", words[0], accountsRunUsage)
	}
	if hasCwd {
		st, err := os.Stat(cwd)
		switch {
		case cwd == "":
			return fmt.Errorf("--cwd needs a folder\n%s", accountsRunUsage)
		case err != nil:
			return fmt.Errorf("--cwd: %w", err)
		case !st.IsDir():
			return fmt.Errorf("--cwd: %s is not a folder", cwd)
		}
	}
	user, err := accountNamed("claude", words[1])
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, muted.Render("Claude Code on "+user+", in its own folder in magpie · while it runs, don't switch Claude Code itself to this account or remove it from magpie"))
	// Ctrl+C is Claude Code's, which shares the terminal: magpie waits on
	// it to end, and reads its sign-in back then
	interrupts.Add(1)
	defer interrupts.Add(-1)
	err = provider.RunClaudeAccount(user, provider.ClaudeRun{Args: pass, Dir: cwd, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr})
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitCode()
		if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			code = 128 + int(ws.Signal())
		}
		if code <= 0 {
			code = 1
		}
		// Claude Code has said why itself
		return exitError{code: code}
	}
	return err
}
