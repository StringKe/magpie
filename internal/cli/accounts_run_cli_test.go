package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/testenv"
)

// fakeClaude stands in for Claude Code: `claude auth status` answers
// $FAKE_STATUS; any other run writes down the config directory, folder,
// wiring and arguments it was given, renews the sign-in it keeps in that
// directory, as Claude Code's refresh does, and exits 7. Only the shell's
// own commands, with PATH holding nothing else.
const fakeClaude = `#!/bin/sh
if [ "$1" = auth ]; then
  printf '%s\n' "$FAKE_STATUS"
  exit 0
fi
{
  printf 'dir=%s\n' "$CLAUDE_CONFIG_DIR"
  printf 'cwd=%s\n' "$(pwd -P)"
  printf 'key=%s\n' "${ANTHROPIC_API_KEY-unset}"
  printf 'nested=%s\n' "${CLAUDECODE-unset}"
  printf 'proxy=%s\n' "${HTTPS_PROXY-unset}"
  printf 'update=%s\n' "${DISABLE_AUTOUPDATER-unset}"
  for a in "$@"; do printf 'arg=%s\n' "$a"; done
} > "$FAKE_RECORD"
printf '{"claudeAiOauth":{"accessToken":"sk-ant-oat01-renewed","refreshToken":"sk-ant-ort01-renewed","expiresAt":4102444800000,"subscriptionType":"max"}}\n' > "$CLAUDE_CONFIG_DIR/.credentials.json"
exit 7
`

// The config directory magpie keeps a saved Claude account in, as the
// gateway's Claude subscription runs Claude Code for it.
func claudeAccountDirOf(config, user string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(user)))
	return filepath.Join(config, "claude-accounts", hex.EncodeToString(sum[:])[:16])
}

// runInChild runs `magpie accounts run <args>` in a fresh process, as
// main does: its output, and its exit code.
func runInChild(t *testing.T, home string, bin bool, extra []string, args ...string) (string, int) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(args)
	path := filepath.Join(home, "nothing")
	if bin {
		path = filepath.Join(home, "fakebin")
	}
	cmd := exec.Command(self, "-test.run=^TestAccountsRunClaude$")
	cmd.Env = append([]string{"MAGPIE_TEST_ACCOUNTS_RUN=" + string(a),
		"HOME=" + home, "USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"), "XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CODEX_HOME=" + filepath.Join(home, ".codex"),
		"PATH=" + path, "USER=magpie-test", "NO_COLOR=1",
		"FAKE_RECORD=" + filepath.Join(home, "record"), "FAKE_STATUS={\"loggedIn\": false}",
		testenv.Marker + "=" + os.Getenv(testenv.Marker),
		"TMP=" + os.Getenv("TMP"), "TEMP=" + os.Getenv("TEMP"), "TMPDIR=" + os.Getenv("TMPDIR")}, extra...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("%v: %s", err, out)
	}
	return string(out), cmd.ProcessState.ExitCode()
}

// magpie accounts run claude <account> (#992) starts Claude Code on a saved
// account in that account's own config directory, the one the gateway's
// Claude subscription runs it in, with Claude Code's arguments as they
// came, in --cwd, and ends with its exit code; the sign-in Claude Code
// renewed there is read back into logins.json. The account Claude Code
// itself is signed in to, an unknown or signed-out one and a machine with
// no Claude Code are refused with nothing run and no directory made.
func TestAccountsRunClaude(t *testing.T) {
	if v := os.Getenv("MAGPIE_TEST_ACCOUNTS_RUN"); v != "" {
		var args []string
		if err := json.Unmarshal([]byte(v), &args); err != nil {
			t.Fatal(err)
		}
		args = append([]string{"accounts", "run"}, args...)
		if helpAsked(args) {
			fmt.Println(helpFor(args))
			os.Exit(0)
		}
		err := accountsCmd(args)
		var e exitError
		switch {
		case errors.As(err, &e):
			fmt.Fprintln(os.Stderr, "magpie:", e.Error())
			os.Exit(e.code)
		case err != nil:
			fmt.Fprintln(os.Stderr, "magpie:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in Claude Code is a shell script")
	}
	setup := func(t *testing.T, own bool) (home, config string) {
		t.Helper()
		home = t.TempDir()
		config = filepath.Join(home, ".config", "magpie")
		if err := os.MkdirAll(config, 0o700); err != nil {
			t.Fatal(err)
		}
		testenv.Program(t, filepath.Join(home, "fakebin", "claude"), fakeClaude)
		auth := func(tok string) string {
			return `{"claudeAiOauth": {"accessToken": "sk-ant-oat01-` + tok + `", "refreshToken": "sk-ant-ort01-` + tok + `", "expiresAt": 4102444800000, "subscriptionType": "max"}}`
		}
		logins := `[
  {"agent": "claude", "user": "other@example.com", "seen": "2026-10-20T10:00:00Z", "auth": ` + auth("old") + `, "profile": {"emailAddress": "other@example.com"}},
  {"agent": "claude", "user": "gone@example.com", "seen": "2026-10-20T10:00:00Z", "auth": {}}`
		if own {
			logins += `,
  {"agent": "claude", "user": "own@example.com", "seen": "2026-10-20T10:00:00Z", "held": true, "auth": ` + auth("own") + `}`
			// Claude Code itself signed in to own@example.com
			if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(auth("own")), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".claude", ".claude.json"), []byte(`{"oauthAccount": {"emailAddress": "own@example.com"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(config, "logins.json"), []byte(logins+"\n]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return home, config
	}
	ran := func(t *testing.T, home string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(home, "record"))
		if os.IsNotExist(err) {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	t.Run("runs", func(t *testing.T) {
		home, config := setup(t, false)
		work := filepath.Join(home, "work")
		if err := os.Mkdir(work, 0o700); err != nil {
			t.Fatal(err)
		}
		out, code := runInChild(t, home, true, []string{"ANTHROPIC_API_KEY=sk-ant-api03-not-this", "CLAUDECODE=1"},
			"claude", "other@example.com", "--cwd", work, "--", "-p", "two words", "--help")
		if code != 7 {
			t.Fatalf("exit %d, want Claude Code's 7: %s", code, out)
		}
		real, _ := filepath.EvalSymlinks(work)
		dir := claudeAccountDirOf(config, "other@example.com")
		want := "dir=" + dir + "\ncwd=" + real + "\nkey=unset\nnested=unset\nproxy=unset\nupdate=unset\narg=-p\narg=two words\narg=--help\n"
		if got := ran(t, home); got != want {
			t.Fatalf("Claude Code was run with\n%s\nwant\n%s\n(magpie said %s)", got, want, out)
		}
		// the sign-in Claude Code renewed in the directory is the account's
		var saved []struct {
			User string          `json:"user"`
			Auth json.RawMessage `json:"auth"`
		}
		b, _ := os.ReadFile(filepath.Join(config, "logins.json"))
		if err := json.Unmarshal(b, &saved); err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(saved, func(s struct {
			User string          `json:"user"`
			Auth json.RawMessage `json:"auth"`
		}) bool {
			return s.User == "other@example.com"
		})
		if i < 0 || !strings.Contains(string(saved[i].Auth), "sk-ant-ort01-renewed") {
			t.Fatalf("logins.json didn't take the renewed sign-in back: %s", b)
		}
		// and Claude Code's own sign-in is left alone
		if _, err := os.Stat(filepath.Join(home, ".claude", ".credentials.json")); !os.IsNotExist(err) {
			t.Fatalf("Claude Code's own sign-in was written: %v", err)
		}
	})

	t.Run("its own folder by default", func(t *testing.T) {
		home, config := setup(t, false)
		if out, code := runInChild(t, home, true, nil, "claude", "other@example.com"); code != 7 {
			t.Fatalf("exit %d: %s", code, out)
		}
		if got := ran(t, home); !strings.HasPrefix(got, "dir="+claudeAccountDirOf(config, "other@example.com")+"\n") || strings.Contains(got, "arg=") {
			t.Fatalf("Claude Code was run with\n%s", got)
		}
	})

	t.Run("through the account's proxy", func(t *testing.T) {
		home, config := setup(t, false)
		// as the gateway's runs on the account go (#1807)
		if err := os.WriteFile(filepath.Join(config, "providers.json"), []byte(`{"providers": [{"id": "claude", "accountProxies": {"other@example.com": "http://127.0.0.1:9"}}]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, code := runInChild(t, home, true, nil, "claude", "other@example.com"); code != 7 {
			t.Fatalf("exit %d: %s", code, out)
		}
		if got := ran(t, home); !strings.Contains(got, "\nproxy=http://127.0.0.1:9\n") {
			t.Fatalf("Claude Code was run with\n%s", got)
		}
	})

	t.Run("magpie's download", func(t *testing.T) {
		home, config := setup(t, false)
		// no Claude Code of the machine's own: the one magpie downloaded
		// runs, and doesn't update itself, as for the bridge
		testenv.Program(t, filepath.Join(home, ".cache", "magpie", "claude-code", "2.1.300", "claude"), fakeClaude)
		if out, code := runInChild(t, home, false, nil, "claude", "other@example.com"); code != 7 {
			t.Fatalf("exit %d: %s", code, out)
		}
		if got := ran(t, home); !strings.HasPrefix(got, "dir="+claudeAccountDirOf(config, "other@example.com")+"\n") || !strings.Contains(got, "\nupdate=1\n") {
			t.Fatalf("Claude Code was run with\n%s", got)
		}
	})

	refused := []struct {
		name string
		own  bool
		bin  bool
		args []string
		says string
		user string // whose directory mustn't be made
	}{
		{"Claude Code's own account", true, true, []string{"claude", "own@example.com"}, "Claude Code itself is signed in to", "own@example.com"},
		{"unknown account", false, true, []string{"claude", "nobody@example.com"}, `no saved claude account "nobody@example.com"`, "nobody@example.com"},
		{"signed out", false, true, []string{"claude", "gone@example.com"}, "sign in again", "gone@example.com"},
		{"no Claude Code", false, false, []string{"claude", "other@example.com"}, "isn't installed", "other@example.com"},
		{"codex", false, true, []string{"codex", "other@example.com"}, "only Claude Code", "other@example.com"},
		{"a flag before --", false, true, []string{"claude", "other@example.com", "--model", "opus"}, "unknown --model", "other@example.com"},
		{"no account", false, true, []string{"claude"}, "usage: magpie accounts run claude", "other@example.com"},
		{"no such folder", false, true, []string{"claude", "other@example.com", "--cwd", "/nonexistent/magpie-test"}, "--cwd", "other@example.com"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			home, config := setup(t, c.own)
			var extra []string
			if c.own {
				extra = []string{`FAKE_STATUS={"loggedIn": true, "email": "own@example.com", "subscriptionType": "max"}`}
			}
			out, code := runInChild(t, home, c.bin, extra, c.args...)
			if code != 1 || !strings.Contains(out, c.says) {
				t.Fatalf("exit %d, %q; want 1 and %q", code, out, c.says)
			}
			if got := ran(t, home); got != "" {
				t.Fatalf("Claude Code was run:\n%s", got)
			}
			if _, err := os.Stat(claudeAccountDirOf(config, c.user)); !os.IsNotExist(err) {
				t.Fatalf("%s's config directory was made: %v", c.user, err)
			}
		})
	}
}

// What follows -- in magpie accounts run is Claude Code's: its --help is
// passed on, not taken for magpie's. Anywhere else --help is still help.
func TestAccountsRunHelpIsClaudeCodes(t *testing.T) {
	for args, want := range map[string]bool{
		"accounts run claude a@example.com -- --help": false,
		"account run claude a@example.com -- -h":      false,
		"accounts run --help":                         true,
		"accounts run claude --help -- -p":            true,
		"snow -- --help":                              true,
		"accounts switch claude -- --help":            true,
	} {
		if got := helpAsked(strings.Fields(args)); got != want {
			t.Errorf("helpAsked(%s) = %v, want %v", args, got, want)
		}
	}
	if !strings.Contains(helpFor([]string{"accounts", "run", "--help"}), "magpie accounts run claude <account>") {
		t.Errorf("magpie accounts --help doesn't list accounts run:\n%s", helpFor([]string{"accounts"}))
	}
}
