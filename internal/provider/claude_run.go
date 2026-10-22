package provider

// `magpie accounts run claude <account>` (#992): Claude Code itself, as
// the user runs it, started on a saved Claude account beside the Claude
// Code signed in to the user's own. It runs in the account's config
// directory (claudeAccountDir), the one the gateway's Claude subscription
// runs Claude Code in for that account: the sign-in there keeps one
// holder, Claude Code, which refreshes it as it does for the gateway, and
// magpie only reads it back (syncSavedClaude). Nothing is linked or copied
// in beside it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/claudecode"
)

// ClaudeRun is a Claude Code started by RunClaudeAccount: its arguments,
// the folder it works in ("" for magpie's own) and the terminal it is
// given.
type ClaudeRun struct {
	Args           []string
	Dir            string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// RunClaudeAccount runs Claude Code on the saved Claude account user, in
// its config directory, and waits for it. What Claude Code made of the
// sign-in there is read back into logins.json once it ends. An exit that
// isn't 0 comes back as the *exec.ExitError. Refused, with nothing run or
// made: the account Claude Code itself is signed in to, which has no such
// directory (its sign-in is Claude Code's own: `claude` is that account);
// an account that can't be used (claudeSignedOut); and no Claude Code.
func RunClaudeAccount(user string, r ClaudeRun) error {
	if discoveryOff() {
		return errAccountDiscoveryOff
	}
	if own, ok := liveLogin("claude"); ok && strings.EqualFold(own.User, user) {
		return claudeOwnRunError(user)
	}
	loginsMu.Lock()
	held := false
	for _, l := range readLogins() {
		if l.Agent == "claude" && strings.EqualFold(l.User, user) {
			held = l.Held
		}
	}
	loginsMu.Unlock()
	if held {
		// Claude Code was on it when magpie last looked: its saved copy
		// is Claude Code's own sign-in, which a directory would hold twice
		return claudeOwnRunError(user)
	}
	cli, ok := findClaudeCLI()
	if !ok {
		return errors.New("Claude Code, the `claude` command line, isn't installed here: install it (https://code.claude.com/docs), " + claudecode.InstallHint)
	}
	return inSavedClaudeDir(user, func(dir string) error {
		// through the account's own proxy, as the gateway's runs on it go
		env, err := cli.proxied(ProxyOfLogin("claude", user), claudeRunEnv(os.Environ(), dir))
		if err != nil {
			return err
		}
		if p, _ := claudecode.Downloaded(); cli.wsl == nil && p != "" && cli.path == p && !slices.ContainsFunc(env, func(e string) bool {
			return strings.HasPrefix(e, "DISABLE_AUTOUPDATER=")
		}) {
			// magpie's download stands in for no agent: it doesn't update
			// itself, as it doesn't for the bridge
			env = append(env, "DISABLE_AUTOUPDATER=1")
		}
		if cli.wsl != nil {
			// one in WSL is told the directory by its path there
			env = cli.wsl.Env(env, "CLAUDE_CONFIG_DIR/p")
		}
		cmd := cli.command(context.Background(), r.Args...)
		cmd.Env = env
		cmd.Dir = r.Dir
		cmd.Stdin, cmd.Stdout, cmd.Stderr = r.Stdin, r.Stdout, r.Stderr
		return cmd.Run()
	})
}

func claudeOwnRunError(user string) error {
	return fmt.Errorf("%s is the account Claude Code itself is signed in to: run `claude` for it. magpie accounts run claude starts Claude Code on your other saved accounts", user)
}

// claudeRunEnv is env for Claude Code run by the user on a saved account
// in config directory dir: none of what would point it at another
// endpoint, token, directory or a Claude Code it runs inside.
func claudeRunEnv(env []string, dir string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range withoutClaudeWiring(env) {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT":
			continue
		}
		out = append(out, kv)
	}
	return append(out, "CLAUDE_CONFIG_DIR="+dir)
}
