package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/plugin"
	"github.com/yetone/magpie/internal/provider"
)

// StringKe (#4532): `magpie accounts on|off <agent> <email|name>` turns one
// account on or off and changes nothing else: not the accounts' order, not
// the account the agent is signed in to. An account Claude Code or Codex
// signed itself in to starts off, and the automatic switch moves only to
// accounts that are on; before, only the GUI's tick and `provider pool
// --accounts`, which also reorders and switches, could turn one on.
func TestAccountsOnOffChangesOneAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("DSH_HOME", "")
	// a name no agent has is asked of the plugins: with no Bun, the plugin
	// host doesn't download one
	t.Setenv("MAGPIE_BUN", filepath.Join(home, "no-bun"))
	t.Cleanup(plugin.Settle)
	jwt := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		return "h." + base64.RawURLEncoding.EncodeToString(b) + ".s"
	}
	codexAuth := func(email string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]any{
			"id_token":      jwt(map[string]any{"email": email}),
			"access_token":  jwt(map[string]any{"exp": time.Now().Add(time.Hour).Unix()}),
			"refresh_token": "r-" + email, "account_id": "acct-" + email}})
		return b
	}
	siwcAuth := func(email string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"clientId": "app_" + email, "hostId": "urn:uuid:h", "email": email,
			"access": "at-" + email, "refresh": "rt-" + email, "expires": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
		return b
	}
	live := codexAuth("me@example.com")
	authPath := filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, live, 0o600); err != nil {
		t.Fatal(err)
	}
	type saved struct {
		Order int             `json:"order,omitempty"`
		Agent string          `json:"agent"`
		User  string          `json:"user"`
		Seen  string          `json:"seen"`
		On    bool            `json:"on,omitempty"`
		Auth  json.RawMessage `json:"auth"`
	}
	logins := filepath.Join(filepath.Dir(provider.Path()), "logins.json")
	b, _ := json.Marshal([]saved{
		{Order: 1, Agent: "codex", User: "me@example.com", Seen: "2026-10-05T10:00:00Z", Auth: live},
		{Order: 3, Agent: "codex", User: "a@example.com", Seen: "2026-10-06T10:00:00Z", Auth: codexAuth("a@example.com")},
		{Order: 2, Agent: "codex", User: "b@example.com", Seen: "2026-10-07T10:00:00Z", On: true, Auth: codexAuth("b@example.com")},
		{Agent: "copilot", User: "mona", Seen: "2026-10-05T10:00:00Z", On: true, Auth: json.RawMessage(`{"oauth_token": "gho_m"}`)},
		{Agent: "copilot", User: "hubot", Seen: "2026-10-06T10:00:00Z", On: true, Auth: json.RawMessage(`{"oauth_token": "gho_h"}`)},
		{Agent: "copilot", User: "octo", Seen: "2026-10-04T10:00:00Z", Auth: json.RawMessage(`{"oauth_token": "gho_o"}`)},
		{Agent: "copilot", User: "mona@acme.ghe.com", Seen: "2026-10-03T10:00:00Z", On: true, Auth: json.RawMessage(`{"oauth_token": "gho_a", "host": "acme.ghe.com"}`)},
		{Agent: "chatgpt-api", User: "c1@example.com", Seen: "2026-10-05T10:00:00Z", On: true, Auth: siwcAuth("c1@example.com")},
		{Agent: "chatgpt-api", User: "c2@example.com", Seen: "2026-10-06T10:00:00Z", Auth: siwcAuth("c2@example.com")},
		{Agent: "chatgpt-api", User: "c3@example.com", Seen: "2026-10-07T10:00:00Z", On: true, Auth: siwcAuth("c3@example.com")},
	})
	if err := os.MkdirAll(filepath.Dir(logins), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logins, b, 0o600); err != nil {
		t.Fatal(err)
	}
	provider.ForgetAccounts()
	t.Cleanup(provider.ForgetAccounts)
	t.Cleanup(agent.SyncCatalogSettled) // a change syncs the agents' files behind it
	run := func(args ...string) (string, error) {
		t.Helper()
		return said(t, func() error { return accountsCmd(append([]string{"accounts"}, args...)) })
	}
	// the agent's accounts as listed, in order, with who is signed in, on
	// and paused, then the order logins.json keeps
	state := func(agent string) string {
		t.Helper()
		var rows []string
		for _, l := range provider.Logins(agent) {
			rows = append(rows, fmt.Sprintf("%s active=%v on=%v paused=%v", l.User, l.Active, l.On, l.Paused))
		}
		var ls []saved
		raw, _ := os.ReadFile(logins)
		json.Unmarshal(raw, &ls)
		for _, l := range ls {
			if l.Agent == agent {
				rows = append(rows, fmt.Sprintf("%s order=%d", l.User, l.Order))
			}
		}
		return strings.Join(rows, "\n")
	}
	// one change of the state: the line from, and only it, now to
	changes := func(was, from, to string) string {
		t.Helper()
		if !strings.Contains(was, from) {
			t.Fatalf("no %q in\n%s", from, was)
		}
		return strings.Replace(was, from, to, 1)
	}
	was := state("codex")

	out, err := run("on", "codex", "A@Example.com")
	if err != nil {
		t.Fatalf("accounts on codex by its email: %v", err)
	}
	if want := changes(was, "a@example.com active=false on=false", "a@example.com active=false on=true"); state("codex") != want {
		t.Fatalf("accounts on changed more than a@'s On:\nwant %s\ngot  %s", want, state("codex"))
	}
	if !strings.Contains(out, "✓") || !strings.Contains(out, "a@example.com is on") {
		t.Fatalf("accounts on said %q", out)
	}

	if _, err := run("alias", "codex", "b@example.com", "Spare"); err != nil {
		t.Fatal(err)
	}
	was = state("codex")
	if out, err = run("off", "codex", "spare"); err != nil {
		t.Fatalf("accounts off codex by its name: %v", err)
	}
	if want := changes(was, "b@example.com active=false on=true", "b@example.com active=false on=false"); state("codex") != want {
		t.Fatalf("accounts off by its name changed more than b@'s On:\nwant %s\ngot  %s", want, state("codex"))
	}
	if !strings.Contains(out, "b@example.com is off") {
		t.Fatalf("accounts off said %q", out)
	}

	// the account Codex is signed in to, off, is paused: Codex stays on it
	was = state("codex")
	if out, err = run("off", "codex", "me@example.com"); err != nil {
		t.Fatalf("accounts off on the signed-in account: %v", err)
	}
	if want := changes(was, "me@example.com active=true on=true paused=false", "me@example.com active=true on=true paused=true"); state("codex") != want || !strings.Contains(out, "paused") {
		t.Fatalf("the signed-in account off: %q\nwant %s\ngot  %s", out, want, state("codex"))
	}
	if got, _ := os.ReadFile(authPath); !bytes.Equal(got, live) {
		t.Fatal("accounts on|off wrote Codex's auth.json")
	}

	raw, _ := os.ReadFile(logins)
	for _, args := range [][]string{
		{"on", "codex", "nobody@example.com"},
		{"enable", "codex", "a@example.com"},
		{"on", "codex"},
		{"off", "codex", "a@example.com", "extra"},
		{"on", "grok", "a@example.com"},
	} {
		if _, err := run(args...); err == nil {
			t.Errorf("accounts %s was taken", strings.Join(args, " "))
		}
	}
	// next is switch's word only: to on|off it is an account's name
	if _, err := run("on", "codex", "next"); err == nil || !strings.Contains(err.Error(), `no saved codex account "next"`) {
		t.Errorf("accounts on codex next: %v", err)
	}
	if got, _ := os.ReadFile(logins); !bytes.Equal(got, raw) {
		t.Fatal("a refused accounts on|off wrote logins.json")
	}
	agent.SyncCatalogSettled() // the changes above have written the agents' files
	before := tree(t, home)
	out, err = runOut(t, "accounts", "on", "codex", "a@example.com", "--help")
	if err != nil || !strings.Contains(out, "magpie accounts on|off") {
		t.Fatalf("accounts on --help: %v %q", err, out)
	}
	if got := tree(t, home); !maps.Equal(got, before) {
		t.Fatalf("accounts on --help wrote under the home:\nbefore %v\nafter  %v", before, got)
	}

	// an agent whose accounts magpie keeps beside the agent's (side_logins.go)
	// the same way; the one magpie uses first is refused off
	was = state("copilot")
	if _, err := run("on", "copilot", "octo"); err != nil {
		t.Fatalf("accounts on copilot: %v", err)
	}
	if want := changes(was, "octo active=false on=false", "octo active=false on=true"); state("copilot") != want {
		t.Fatalf("accounts on copilot:\nwant %s\ngot  %s", want, state("copilot"))
	}
	// a login on an enterprise's host, named by --host as switch names it
	was = state("copilot")
	if _, err := run("off", "copilot", "mona", "--host", "acme.ghe.com"); err != nil {
		t.Fatalf("accounts off copilot --host: %v", err)
	}
	if want := changes(was, "mona@acme.ghe.com active=false on=true", "mona@acme.ghe.com active=false on=false"); state("copilot") != want {
		t.Fatalf("accounts off copilot --host:\nwant %s\ngot  %s", want, state("copilot"))
	}
	was = state("copilot")
	first := ""
	for _, l := range provider.Logins("copilot") {
		if l.Active {
			first = l.User
		}
	}
	if _, err := run("off", "copilot", first); err == nil || !strings.Contains(err.Error(), "first") {
		t.Fatalf("the account magpie uses first, %q, was turned off: %v", first, err)
	}
	if state("copilot") != was {
		t.Fatalf("a refused off changed the accounts:\n%s", state("copilot"))
	}

	// ChatGPT API's accounts have an On as Copilot's do (setSIWCLoginOn)
	was = state("chatgpt-api")
	if _, err := run("on", "chatgpt-api", "c2@example.com"); err != nil {
		t.Fatalf("accounts on chatgpt-api: %v", err)
	}
	if want := changes(was, "c2@example.com active=false on=false", "c2@example.com active=false on=true"); state("chatgpt-api") != want {
		t.Fatalf("accounts on chatgpt-api:\nwant %s\ngot  %s", want, state("chatgpt-api"))
	}
	first = ""
	for _, l := range provider.Logins("chatgpt-api") {
		if l.Active {
			first = l.User
		}
	}
	other := "c3@example.com"
	if first == other {
		other = "c1@example.com"
	}
	if err := provider.SetAccountName(provider.ChatGPTAPIID, other, "Side"); err != nil {
		t.Fatal(err)
	}
	was = state("chatgpt-api")
	if _, err := run("off", "chatgpt-api", "side"); err != nil {
		t.Fatalf("accounts off chatgpt-api by its name: %v", err)
	}
	if want := changes(was, other+" active=false on=true", other+" active=false on=false"); state("chatgpt-api") != want {
		t.Fatalf("accounts off chatgpt-api by its name:\nwant %s\ngot  %s", want, state("chatgpt-api"))
	}
	was = state("chatgpt-api")
	if _, err := run("off", "chatgpt-api", first); err == nil || !strings.Contains(err.Error(), "first") {
		t.Fatalf("the ChatGPT API account magpie uses first, %q, was turned off: %v", first, err)
	}
	if state("chatgpt-api") != was {
		t.Fatalf("a refused off changed the ChatGPT API accounts:\n%s", state("chatgpt-api"))
	}
	// only on|off takes chatgpt-api: its forget revokes the sign-in in a
	// goroutine the CLI's process exits before
	raw, _ = os.ReadFile(logins)
	for _, args := range [][]string{
		{"forget", "chatgpt-api", "c2@example.com"},
		{"switch", "chatgpt-api", "c2@example.com"},
		{"alias", "chatgpt-api", "c2@example.com", "Other"},
		{"chatgpt-api"},
	} {
		if _, err := run(args...); err == nil || !strings.Contains(err.Error(), `"chatgpt-api": only Claude Code`) {
			t.Errorf("accounts %s: %v", strings.Join(args, " "), err)
		}
	}
	if got, _ := os.ReadFile(logins); !bytes.Equal(got, raw) || state("chatgpt-api") != was {
		t.Fatalf("a refused chatgpt-api command changed the accounts:\n%s", state("chatgpt-api"))
	}
}

// With no ChatGPT API account, on|off says where one is signed in to, not
// `magpie accounts add chatgpt-api`, which accounts refuses.
func TestAccountsOnChatGPTAPINoneSaved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("MAGPIE_BUN", filepath.Join(home, "no-bun"))
	t.Cleanup(plugin.Settle)
	provider.ForgetAccounts()
	t.Cleanup(provider.ForgetAccounts)
	_, err := said(t, func() error { return accountsCmd([]string{"accounts", "on", "chatgpt-api", "x@example.com"}) })
	if err == nil || strings.Contains(err.Error(), "accounts add") || !strings.Contains(err.Error(), "no saved chatgpt-api accounts") {
		t.Fatalf("accounts on chatgpt-api with none saved: %v", err)
	}
}
