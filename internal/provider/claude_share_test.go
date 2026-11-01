package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// yourClaudeSetup writes a Claude Code setup into the test's ~/.claude, and
// MCP servers of the user's own into ~/.claude.json beside the account
// Claude Code is signed in to there.
func yourClaudeSetup(t *testing.T, home string) {
	t.Helper()
	claude := filepath.Join(home, ".claude")
	for path, body := range map[string]string{
		"CLAUDE.md":              "my rules\n",
		"skills/review/SKILL.md": "---\nname: review\n---\n",
		"commands/ship.md":       "ship it\n",
		"agents/helper.md":       "---\nname: helper\n---\n",
		"rules/go.md":            "gofmt\n",
		"keybindings.json":       `{"bindings": [{"context": "Chat", "bindings": {"enter": "chat:newline"}}]}`,
		"history.jsonl":          "{}\n",
		"projects/-work/x.jsonl": "{}\n",
		"settings.json":          `{"env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:8080", "ANTHROPIC_AUTH_TOKEN": "magpie-key", "ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-flash", "MY_FLAG": "1"}, "model": "glm-5", "apiKeyHelper": "/bin/key", "permissions": {"allow": ["Bash(ls)"]}}`,
		".credentials.json":      `{"claudeAiOauth": {"accessToken": "tok-yours", "refreshToken": "r-yours"}}`,
		"plugins/installed.json": "{}\n",
		"sessions/one.json":      "{}\n",
		"ide/1.lock":             "x",
		"settings.local.json":    "{}",
	} {
		p := filepath.Join(claude, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(home, ".claude.json"), map[string]any{
		"oauthAccount":  map[string]any{"emailAddress": "pro@x.com", "organizationUuid": "org-pro@x.com", "organizationName": "pro@x.com's Organization"},
		"primaryApiKey": "sk-ant-api03-yours",
		"mcpServers":    map[string]any{"fs": map[string]any{"command": "fs-mcp"}},
		"projects":      map[string]any{"/work": map[string]any{"mcpServers": map[string]any{"local": map[string]any{"command": "local-mcp"}}}},
	})
}

// shared is what of names the account's directory holds.
func shared(dir string, names ...string) []string {
	var out []string
	for _, n := range names {
		if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func jsonAt(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

// The account's config directory as magpie's own runs have it (a card's
// /usage, a warm-up, the keep-alive, the gateway's requests: all through
// claudeSavedDir) holds none of the user's setup, whatever ~/.claude has.
func TestClaudeSavedDirBringsNoSetup(t *testing.T) {
	claudeThree(t)
	home := os.Getenv("HOME")
	yourClaudeSetup(t, home)
	dir, err := claudeSavedDir("max@x.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := shared(dir, "CLAUDE.md", "skills", "commands", "agents", "rules", "settings.json", "keybindings.json", claudeShareRecordName); len(got) > 0 {
		t.Fatalf("claudeSavedDir brought %v in", got)
	}
	if cfg := jsonAt(t, filepath.Join(dir, ".claude.json")); cfg["mcpServers"] != nil {
		t.Fatalf("claudeSavedDir put MCP servers in the account's .claude.json: %v", cfg)
	}
}

// The setup shared in for `accounts run` is the user's configuration and
// nothing else: the five folders and files linked, settings.json copied
// without magpie's gateway wiring, the user's own MCP servers merged into
// the account's .claude.json beside its own oauthAccount. No sign-in,
// history, keybindings or other file of ~/.claude or ~/.claude.json.
func TestShareClaudeSetupIsConfigurationOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a link needs Developer Mode on Windows")
	}
	claudeThree(t)
	home := os.Getenv("HOME")
	yourClaudeSetup(t, home)
	dir, err := claudeSavedDir("max@x.com")
	if err != nil {
		t.Fatal(err)
	}
	if notes := shareClaudeSetup(dir, false); len(notes) > 0 {
		t.Fatalf("notes: %v", notes)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{".claude.json", ".credentials.json", claudeShareRecordName, "CLAUDE.md", "agents", "commands", "rules", "settings.json", "skills"}
	if !slices.Equal(names, want) {
		t.Fatalf("the account's directory holds %v, want %v", names, want)
	}
	for _, n := range claudeLinked {
		got, err := os.Readlink(filepath.Join(dir, n))
		real, _ := filepath.EvalSymlinks(filepath.Join(home, ".claude", n))
		if err != nil || got != real {
			t.Errorf("%s links to %q (%v), want %q", n, got, err, real)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".credentials.json")); strings.Contains(string(b), "yours") {
		t.Fatalf("the user's own sign-in went in: %s", b)
	}
	settings := jsonAt(t, filepath.Join(dir, "settings.json"))
	if fi, _ := os.Lstat(filepath.Join(dir, "settings.json")); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("settings.json is a link to the user's, gateway wiring and all")
	}
	env, _ := settings["env"].(map[string]any)
	if len(env) != 1 || env["MY_FLAG"] != "1" || settings["model"] != nil || settings["apiKeyHelper"] != nil || settings["permissions"] == nil {
		t.Fatalf("settings.json in the account's directory: %v", settings)
	}
	cfg := jsonAt(t, filepath.Join(dir, ".claude.json"))
	acct, _ := cfg["oauthAccount"].(map[string]any)
	if acct["emailAddress"] != "max@x.com" {
		t.Fatalf("the account's oauthAccount was changed: %v", cfg)
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if len(servers) != 1 || servers["fs"] == nil || cfg["primaryApiKey"] != nil || cfg["projects"] != nil {
		t.Fatalf("the account's .claude.json: %v", cfg)
	}
	// and again: nothing changes, nothing is said
	before, _ := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if notes := shareClaudeSetup(dir, false); len(notes) > 0 {
		t.Fatalf("a second run said %v", notes)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".claude.json")); string(after) != string(before) {
		t.Fatalf("a second run rewrote .claude.json:\n%s\n%s", before, after)
	}
}

// What the account's directory has of its own stays as it is, and the
// next run brings in only what magpie put there before: a copy of
// settings.json changed in the account's directory is the account's now,
// and what the user takes out of their own setup goes from the account's.
func TestShareClaudeSetupKeepsTheAccountsOwn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a link needs Developer Mode on Windows")
	}
	claudeThree(t)
	home := os.Getenv("HOME")
	yourClaudeSetup(t, home)
	dir, err := claudeSavedDir("max@x.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("the account's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// a link of the account's own, not magpie's
	rules := t.TempDir()
	if err := os.Symlink(rules, filepath.Join(dir, "rules")); err != nil {
		t.Fatal(err)
	}
	ownLink := func() {
		t.Helper()
		if got, err := os.Readlink(filepath.Join(dir, "rules")); err != nil || got != rules {
			t.Fatalf("the account's own rules link was replaced: %q (%v), want %q", got, err, rules)
		}
	}
	cfg := jsonAt(t, filepath.Join(dir, ".claude.json"))
	cfg["mcpServers"] = map[string]any{"fs": map[string]any{"command": "the-accounts-fs"}}
	writeFile(t, filepath.Join(dir, ".claude.json"), cfg)
	notes := shareClaudeSetup(dir, false)
	ownLink()
	if len(notes) != 2 || !strings.Contains(notes[0], "MCP server fs") || !strings.Contains(notes[1], "own CLAUDE.md, rules in") {
		t.Fatalf("notes: %v", notes)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md")); string(b) != "the account's\n" {
		t.Fatalf("the account's own CLAUDE.md was replaced: %q", b)
	}
	if servers, _ := jsonAt(t, filepath.Join(dir, ".claude.json"))["mcpServers"].(map[string]any); servers["fs"].(map[string]any)["command"] != "the-accounts-fs" {
		t.Fatalf("the account's own MCP server was replaced: %v", servers)
	}
	var rec claudeShareRecord
	if b, err := os.ReadFile(filepath.Join(dir, claudeShareRecordName)); err != nil || json.Unmarshal(b, &rec) != nil {
		t.Fatalf("%s: %v", claudeShareRecordName, err)
	}
	if slices.Contains(rec.Links, "CLAUDE.md") || slices.Contains(rec.Links, "rules") || len(rec.MCP) > 0 {
		t.Fatalf("the account's own were taken for magpie's: %+v", rec)
	}

	// the copy changed in the account's directory, and the user's own
	// changed: the account's stays
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"theme": "light"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"theme": "dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// a server added, the skills taken out
	user := jsonAt(t, filepath.Join(home, ".claude.json"))
	user["mcpServers"] = map[string]any{"fs": map[string]any{"command": "fs-mcp"}, "web": map[string]any{"command": "web-mcp"}}
	writeFile(t, filepath.Join(home, ".claude.json"), user)
	if err := os.RemoveAll(filepath.Join(home, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}
	notes = shareClaudeSetup(dir, false)
	if b, _ := os.ReadFile(filepath.Join(dir, "settings.json")); string(b) != `{"theme": "light"}` {
		t.Fatalf("the account's changed settings.json was replaced: %s (notes %v)", b, notes)
	}
	if len(notes) != 2 || !strings.Contains(notes[1], "own CLAUDE.md, rules, settings.json") {
		t.Fatalf("notes: %v", notes)
	}
	ownLink()
	if _, err := os.Lstat(filepath.Join(dir, "skills")); !os.IsNotExist(err) {
		t.Fatalf("the link to the skills taken out is still there: %v", err)
	}
	servers, _ := jsonAt(t, filepath.Join(dir, ".claude.json"))["mcpServers"].(map[string]any)
	if len(servers) != 2 || servers["web"] == nil || servers["fs"].(map[string]any)["command"] != "the-accounts-fs" {
		t.Fatalf("the account's MCP servers: %v", servers)
	}
	// and the server taken out of the user's goes from the account's
	user["mcpServers"] = map[string]any{"fs": map[string]any{"command": "fs-mcp"}}
	writeFile(t, filepath.Join(home, ".claude.json"), user)
	shareClaudeSetup(dir, false)
	servers, _ = jsonAt(t, filepath.Join(dir, ".claude.json"))["mcpServers"].(map[string]any)
	if len(servers) != 1 || servers["web"] != nil {
		t.Fatalf("the account's MCP servers: %v", servers)
	}
}

// A server magpie put in the account's .claude.json and the account changed
// or removed there since (`claude mcp remove` in an accounts run) is the
// account's from then on: the next run leaves the change and doesn't bring
// the removed one back, and says so. One still as magpie wrote it follows
// the user's, and goes when the user's goes.
func TestShareClaudeMCPFollowsTheAccountsEdits(t *testing.T) {
	claudeThree(t)
	home := os.Getenv("HOME")
	yourClaudeSetup(t, home)
	user := jsonAt(t, filepath.Join(home, ".claude.json"))
	user["mcpServers"] = map[string]any{"fs": map[string]any{"command": "fs-mcp"}, "web": map[string]any{"command": "web-mcp"}, "db": map[string]any{"command": "db-mcp"}}
	writeFile(t, filepath.Join(home, ".claude.json"), user)
	dir, err := claudeSavedDir("max@x.com")
	if err != nil {
		t.Fatal(err)
	}
	acct := filepath.Join(dir, ".claude.json")
	servers := func() map[string]any {
		t.Helper()
		s, _ := jsonAt(t, acct)["mcpServers"].(map[string]any)
		return s
	}
	command := func(s map[string]any, name string) any {
		m, _ := s[name].(map[string]any)
		return m["command"]
	}
	mcpNotes := func(notes []string) []string {
		var out []string
		for _, n := range notes {
			if strings.Contains(n, "MCP") {
				out = append(out, n)
			}
		}
		return out
	}
	if n := mcpNotes(shareClaudeSetup(dir, false)); len(n) > 0 {
		t.Fatalf("notes: %v", n)
	}
	if s := servers(); len(s) != 3 {
		t.Fatalf("the account's MCP servers: %v", s)
	}

	// in the accounts run: fs changed, web removed; and the user's db changed
	cfg := jsonAt(t, acct)
	cfg["mcpServers"] = map[string]any{"fs": map[string]any{"command": "the-accounts-fs"}, "db": map[string]any{"command": "db-mcp"}}
	writeFile(t, acct, cfg)
	user["mcpServers"] = map[string]any{"fs": map[string]any{"command": "fs-mcp-2"}, "web": map[string]any{"command": "web-mcp"}, "db": map[string]any{"command": "db-mcp-2"}}
	writeFile(t, filepath.Join(home, ".claude.json"), user)
	for run := 1; run <= 2; run++ {
		n := mcpNotes(shareClaudeSetup(dir, false))
		s := servers()
		if command(s, "fs") != "the-accounts-fs" || s["web"] != nil || command(s, "db") != "db-mcp-2" || len(s) != 2 {
			t.Fatalf("run %d: the account's MCP servers: %v", run, s)
		}
		if len(n) != 2 || !strings.Contains(n[0], "own MCP server fs,") || !strings.Contains(n[1], "left out your MCP server web:") {
			t.Fatalf("run %d: notes: %v", run, n)
		}
	}

	// the user's db and fs taken out: magpie's db goes, the account's fs stays
	user["mcpServers"] = map[string]any{"web": map[string]any{"command": "web-mcp"}}
	writeFile(t, filepath.Join(home, ".claude.json"), user)
	shareClaudeSetup(dir, false)
	if s := servers(); len(s) != 1 || command(s, "fs") != "the-accounts-fs" {
		t.Fatalf("the account's MCP servers: %v", s)
	}
}

// Forgetting the account removes its directory, links and all, and none of
// the user's own files they point to.
func TestForgetClaudeDirLeavesYourSetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a link needs Developer Mode on Windows")
	}
	claudeThree(t)
	home := os.Getenv("HOME")
	yourClaudeSetup(t, home)
	dir, err := claudeSavedDir("max@x.com")
	if err != nil {
		t.Fatal(err)
	}
	shareClaudeSetup(dir, false)
	if _, err := os.Readlink(filepath.Join(dir, "skills")); err != nil {
		t.Fatalf("no link to forget: %v", err)
	}
	forgetClaudeDir("max@x.com")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the account's directory is still there: %v", err)
	}
	for _, p := range []string{"CLAUDE.md", "skills/review/SKILL.md", "commands/ship.md", "agents/helper.md", "rules/go.md", "settings.json"} {
		if _, err := os.Stat(filepath.Join(home, ".claude", filepath.FromSlash(p))); err != nil {
			t.Errorf("the user's %s went with the account's directory: %v", p, err)
		}
	}
	if servers, _ := jsonAt(t, filepath.Join(home, ".claude.json"))["mcpServers"].(map[string]any); servers["fs"] == nil {
		t.Errorf("the user's MCP servers went: %v", servers)
	}
}

// Claude Code on a saved account gets the user's settings without what
// points it at another endpoint or key: magpie's gateway and key while
// connected, and the models magpie wrote for it, so the run is the
// account's own sign-in, never the gateway nor "Both … set". A user not
// connected keeps their own models.
func TestClaudeRunSettings(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"connected": {
			`{"env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:8080", "ANTHROPIC_AUTH_TOKEN": "magpie-key", "ANTHROPIC_MODEL": "glm-5", "ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-flash", "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "DeepSeek", "ANTHROPIC_SMALL_FAST_MODEL": "glm-4", "CLAUDE_CODE_SUBAGENT_MODEL": "glm-5", "MY_FLAG": "1"}, "model": "glm-5", "hooks": {"Stop": []}}`,
			`{"env": {"MY_FLAG": "1"}, "hooks": {"Stop": []}}`,
		},
		// what magpie sizes and lists its models by, Claude Desktop's
		// windows under modelSettings among them; the user's own effort and
		// a Claude model's own window (/effort, magpie tune) stay
		"connected, its windows and picker": {
			`{"env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:8080", "ANTHROPIC_AUTH_TOKEN": "magpie-key", "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "500000", "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "128000", "CLAUDE_CODE_MODEL_CAPABILITIES": "x", "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "272000", "CLAUDE_CODE_AUTO_MODE_SERVER": "0", "CLAUDE_CODE_DISABLE_EXPLORE_INHERIT_CAP": "1", "CLAUDE_CODE_EFFORT_LEVEL": "max"}, "modelPicker": {"options": []}, "autoCompactWindow": 272000, "modelSettings": {"cpa/gpt-6-luna": {"autoCompactWindow": 131000}, "mythos-magpie-2": {"autoCompactWindow": 131000}, "claude-opus-5-5": {"effortLevel": "high", "autoCompactWindow": 400000}}, "effortLevel": "high"}`,
			`{"env": {"CLAUDE_CODE_EFFORT_LEVEL": "max"}, "modelSettings": {"claude-opus-5-5": {"effortLevel": "high", "autoCompactWindow": 400000}}, "effortLevel": "high"}`,
		},
		"the claude.ai sign-in kept": {
			`{"env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:8080", "ANTHROPIC_AUTH_TOKEN": ""}, "model": "opus"}`,
			`{}`,
		},
		"a key of the user's own": {
			`{"env": {"ANTHROPIC_API_KEY": "sk-ant-api03-x", "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-5"}, "apiKeyHelper": "/bin/key", "model": "opus"}`,
			`{"env": {"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-5"}, "model": "opus"}`,
		},
		// a cloud's switch sends Claude Code past the account's sign-in,
		// and the models beside it are that cloud's ids; the cloud's own
		// variables, which nothing reads without the switch, stay
		"on Bedrock": {
			`{"env": {"CLAUDE_CODE_USE_BEDROCK": "1", "AWS_REGION": "us-east-1", "ANTHROPIC_MODEL": "us.anthropic.claude-opus-5-5-v1:0", "ANTHROPIC_SMALL_FAST_MODEL": "us.anthropic.claude-haiku-5-v1:0", "MY_FLAG": "1"}, "model": "us.anthropic.claude-opus-5-5-v1:0", "modelSettings": {"us.anthropic.claude-opus-5-5-v1:0": {"effortLevel": "high"}}}`,
			`{"env": {"AWS_REGION": "us-east-1", "MY_FLAG": "1"}}`,
		},
		"on Vertex AI, as JSON's true": {
			`{"env": {"CLAUDE_CODE_USE_VERTEX": true, "ANTHROPIC_VERTEX_PROJECT_ID": "p", "ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-5-5@20261001"}, "model": "opus", "modelSettings": {"claude-opus-5-5@20261001": {"effortLevel": "high"}, "claude-opus-5-5": {"effortLevel": "max"}}}`,
			`{"env": {"ANTHROPIC_VERTEX_PROJECT_ID": "p"}, "modelSettings": {"claude-opus-5-5": {"effortLevel": "max"}}}`,
		},
		"a switch off": {
			`{"env": {"CLAUDE_CODE_USE_BEDROCK": "0", "CLAUDE_CODE_USE_FOUNDRY": "false", "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-5"}, "model": "opus"}`,
			`{"env": {"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-5"}, "model": "opus"}`,
		},
	} {
		out, err := claudeRunSettings([]byte(c.in), false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var got, want any
		_ = json.Unmarshal(out, &got)
		_ = json.Unmarshal([]byte(c.want), &want)
		if g, _ := json.Marshal(got); string(g) != mustJSON(want) {
			t.Errorf("%s: %s, want %s", name, out, c.want)
		}
	}
	// the switch in the terminal's environment, the models in settings.json
	out, err := claudeRunSettings([]byte(`{"env": {"ANTHROPIC_MODEL": "us.anthropic.claude-opus-5-5-v1:0", "MY_FLAG": "1"}, "model": "us.anthropic.claude-opus-5-5-v1:0"}`), true)
	if err != nil || string(out) != "{\n  \"env\": {\n    \"MY_FLAG\": \"1\"\n  }\n}\n" {
		t.Errorf("with the run's environment on a cloud: %s (%v)", out, err)
	}
	for _, bad := range []string{`not json`, `[]`, `{"env": "x"}`} {
		if _, err := claudeRunSettings([]byte(bad), false); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

// The run's environment loses a cloud's switch, on or off, and with one on,
// or the user's settings on a cloud, the models named for it: a terminal
// with CLAUDE_CODE_USE_VERTEX=1 would otherwise run the account on Vertex
// AI's credentials.
func TestClaudeRunEnv(t *testing.T) {
	keys := func(env []string) []string {
		var out []string
		for _, kv := range env {
			k, _, _ := strings.Cut(kv, "=")
			out = append(out, k)
		}
		return out
	}
	got := keys(claudeRunEnv([]string{"PATH=/bin", "CLAUDE_CODE_USE_VERTEX=1", "ANTHROPIC_VERTEX_PROJECT_ID=p", "ANTHROPIC_MODEL=claude-opus-5-5@20261001", "CLAUDE_CODE_USE_BEDROCK=0", "CLAUDE_CONFIG_DIR=/elsewhere"}, "/acct", false))
	if want := []string{"PATH", "ANTHROPIC_VERTEX_PROJECT_ID", "CLAUDE_CONFIG_DIR"}; !slices.Equal(got, want) {
		t.Fatalf("on Vertex AI: %v, want %v", got, want)
	}
	got = keys(claudeRunEnv([]string{"CLAUDE_CODE_USE_BEDROCK=0", "ANTHROPIC_MODEL=claude-opus-5-5"}, "/acct", false))
	if want := []string{"ANTHROPIC_MODEL", "CLAUDE_CONFIG_DIR"}; !slices.Equal(got, want) {
		t.Fatalf("a switch off: %v, want %v", got, want)
	}
	got = keys(claudeRunEnv([]string{"ANTHROPIC_MODEL=us.anthropic.claude-opus-5-5-v1:0"}, "/acct", true))
	if want := []string{"CLAUDE_CONFIG_DIR"}; !slices.Equal(got, want) {
		t.Fatalf("the user's settings on a cloud: %v, want %v", got, want)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
