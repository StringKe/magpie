package provider

// `magpie accounts run claude <account>` (#992): Claude Code itself, as
// the user runs it, started on a saved Claude account beside the Claude
// Code signed in to the user's own. It runs in the account's config
// directory (claudeAccountDir), the one the gateway's Claude subscription
// runs Claude Code in for that account: the sign-in there keeps one
// holder, Claude Code, which refreshes it as it does for the gateway, and
// magpie only reads it back (syncSavedClaude). The user's own setup is
// brought in beside it (shareClaudeSetup), never a sign-in.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/agentenv"
	"github.com/yetone/magpie/internal/appdir"
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
// isn't 0 comes back as the *exec.ExitError. An account added by a
// setup-token is run on its token (ClaudeDirToken). Refused, with nothing
// run or made: the account Claude Code itself is signed in to, which has
// no such directory (its sign-in is Claude Code's own: `claude` is that
// account); an account that can't be used (claudeSignedOut); and no
// Claude Code.
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
		environ := os.Environ()
		elsewhere := claudeElsewhere(envLookup(environ))
		if cli.wsl == nil {
			// one in WSL has its setup in WSL's home, not this one's
			elsewhere = elsewhere || claudeSettingsElsewhere(filepath.Join(appdir.ClaudeDir(), "settings.json"))
			for _, n := range shareClaudeSetup(dir, elsewhere) {
				if r.Stderr != nil {
					fmt.Fprintln(r.Stderr, n)
				}
			}
		}
		env := claudeRunEnv(environ, dir, elsewhere)
		// an account added by a setup-token has no sign-in in its
		// directory: Claude Code takes the token in its environment, as the
		// gateway's runs on it do (inClaudeDir), never on the command line
		tok := ClaudeDirToken(dir)
		if tok != "" {
			env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+tok)
		}
		// through the account's own proxy, as the gateway's runs on it go
		env, err := cli.proxied(ProxyOfLogin("claude", user), env)
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
			names := []string{"CLAUDE_CONFIG_DIR/p"}
			if tok != "" {
				names = append(names, "CLAUDE_CODE_OAUTH_TOKEN")
			}
			env = cli.wsl.Env(env, names...)
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
// endpoint, cloud (agentenv.ClaudeProviders, on or off), token, directory
// or a Claude Code it runs inside, and, where env or the user's settings
// (elsewhere) send Claude Code past Anthropic's API, none of the models and
// windows named for that endpoint (claudeEndpointEnv).
func claudeRunEnv(env []string, dir string, elsewhere bool) []string {
	elsewhere = elsewhere || claudeElsewhere(envLookup(env))
	out := make([]string, 0, len(env)+1)
	for _, kv := range withoutClaudeWiring(env) {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "CLAUDE_CONFIG_DIR", k == "CLAUDE_SECURESTORAGE_CONFIG_DIR", k == "CLAUDECODE", k == "CLAUDE_CODE_ENTRYPOINT":
			continue
		case slices.Contains(agentenv.ClaudeProviders, k):
			continue
		case elsewhere && slices.Contains(claudeEndpointEnv, k):
			continue
		}
		out = append(out, kv)
	}
	return append(out, "CLAUDE_CONFIG_DIR="+dir)
}

// claudeElsewhere says the env get reads sends Claude Code past
// Anthropic's own API, which the account's sign-in is for: to an endpoint
// (ANTHROPIC_BASE_URL) or, with a provider switch on, to a cloud.
func claudeElsewhere(get func(string) (string, bool)) bool {
	if v, _ := get("ANTHROPIC_BASE_URL"); v != "" {
		return true
	}
	for _, k := range agentenv.ClaudeProviders {
		if v, ok := get(k); ok && claudeSwitchOn(v) {
			return true
		}
	}
	return false
}

// claudeSwitchOn is whether Claude Code reads a switch's value as on, as
// agent's claudeSwitchOn says: 1, true, yes or on, in any case, spaces
// around it taken off.
func claudeSwitchOn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// claudeSettingsElsewhere says the env of the settings.json at path sends
// Claude Code past Anthropic's API (claudeElsewhere); not when it can't be
// read.
func claudeSettingsElsewhere(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var s struct {
		Env map[string]json.RawMessage `json:"env"`
	}
	if json.Unmarshal(b, &s) != nil {
		return false
	}
	return claudeElsewhere(func(k string) (string, bool) {
		v, ok := s.Env[k]
		return settingsEnvText(v), ok
	})
}

// envLookup reads a variable of env, the last one set winning, as exec
// does.
func envLookup(env []string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		for i := len(env) - 1; i >= 0; i-- {
			if name, v, _ := strings.Cut(env[i], "="); name == k {
				return v, true
			}
		}
		return "", false
	}
}

// settingsEnvText is a value of settings.json's env as the variable Claude
// Code makes of it: a string's text, else the JSON as written (true, 1).
func settingsEnvText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}
