package cli

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// StringKe's audit (#4532, 2.3): a setup-token could be added only in the
// window. `magpie accounts add claude --token <token|->` adds it as the
// window's "Add an account by setup-token…" does, "-" reading it from
// stdin, and never prints the token back: not when the words come in
// another order and the token isn't right after --token either.
func TestAccountsAddClaudeToken(t *testing.T) {
	root := helpHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, ".claude"))
	const tok, piped = "sk-ant-oat01-cli-token-abcdefghijklmnopqrstuvwxyz", "sk-ant-oat01-cli-piped-abcdefghijklmnopqrstuvwxyz"
	// what the command prints on stdout and stderr, with its error as run's
	// caller writes it to stderr
	add := func(args ...string) (string, error) {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		oldErr := os.Stderr
		os.Stderr = w
		errOut := make(chan string)
		go func() {
			b, _ := io.ReadAll(r)
			errOut <- string(b)
		}()
		out, err := said(t, func() error { return accountsCmd(append([]string{"accounts", "add"}, args...)) })
		w.Close()
		os.Stderr = oldErr
		out += <-errOut
		r.Close()
		if err != nil {
			out += "magpie: " + err.Error() + "\n"
		}
		return out, err
	}
	tokens := func() map[string]bool {
		out := map[string]bool{}
		for _, l := range provider.Logins("claude") {
			if l.Token {
				out[l.User] = true
			}
		}
		return out
	}

	out, err := add("claude", "--token", tok)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(tokens()) != 1 || strings.Contains(out, tok) || !strings.Contains(out, "added") {
		t.Fatalf("--token: %v\n%s", tokens(), out)
	}

	// "-": the next line of stdin, a pipe here
	in := filepath.Join(t.TempDir(), "in")
	if err := os.WriteFile(in, []byte(piped+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	oldIn := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = oldIn })
	out, err = add("cc", "--token", "-")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(tokens()) != 2 || strings.Contains(out, piped) || !strings.Contains(out, "added") {
		t.Fatalf("--token -: %v\n%s", tokens(), out)
	}

	// the same token again is the account that has it
	out, err = add("claude", "--token="+tok)
	if err != nil || len(tokens()) != 2 || strings.Contains(out, tok) || !strings.Contains(out, "already") {
		t.Fatalf("again: %v %v\n%s", err, tokens(), out)
	}

	// the agent word after the token is the same
	const after = "sk-ant-oat01-cli-after-abcdefghijklmnopqrstuvwxyz"
	out, err = add("--token", after, "claude")
	if err != nil || len(tokens()) != 3 || strings.Contains(out, after) || !strings.Contains(out, "added") {
		t.Fatalf("--token <token> claude: %v %v\n%s", err, tokens(), out)
	}

	// refused, and nothing typed is echoed on stdout or stderr: the token
	// not right after --token (it is taken as the agent then), another
	// agent, an API key, no token
	const typed = "sk-ant-oat01-cli-typed-abcdefghijklmnopqrstuvwxyz"
	before := tree(t, root)
	for _, args := range [][]string{
		{"--token", "claude", typed},
		{"--token", "cc", typed},
		{"--token=claude", typed},
		{"--token", "CLAUDE", typed},
		{typed, "--token", "claude"},
		{"claude", typed, "--token"},
		{"claude", "--token", "claude", typed},
		{"--token", typed},
		{"codex", "--token", typed},
		{"claude", "--token", "sk-ant-api03-secret-key"},
		{"claude", "--token"},
		{"claude", "extra", "--token", typed},
	} {
		out, err := add(args...)
		if err == nil {
			t.Fatalf("accounts add %s: no error\n%s", strings.Join(args, " "), out)
		}
		for _, a := range args {
			if strings.HasPrefix(a, "sk-ant-") && strings.Contains(out, a) {
				t.Fatalf("accounts add %s echoed the token:\n%s", strings.Join(args, " "), out)
			}
		}
	}
	if got := tree(t, root); !reflect.DeepEqual(got, before) {
		t.Fatalf("a refused --token wrote under the home: %v", tokens())
	}

	// --help anywhere prints the usage and keeps nothing
	const other = "sk-ant-oat01-cli-help-abcdefghijklmnopqrstuvwxyz"
	before = tree(t, root)
	for _, args := range [][]string{{"accounts", "add", "claude", "--token", other, "--help"}, {"accounts", "add", "claude", "--token", "-h"}, {"accounts", "add", "--help", "claude", "--token=" + other}} {
		out, err := runOut(t, args...)
		if err != nil || !strings.Contains(out, "--token") || strings.Contains(out, other) {
			t.Fatalf("magpie %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		if got := tree(t, root); !reflect.DeepEqual(got, before) {
			t.Fatalf("magpie %s wrote under the home", strings.Join(args, " "))
		}
	}
}
