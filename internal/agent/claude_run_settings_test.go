package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// `magpie accounts run claude` copies the user's settings.json into a
// saved account's folder without what magpie writes beside its gateway
// (provider.ClaudeEndpointEnv). This compares that list with the variable
// constants named here only; a variable claude.go writes under a name not
// listed here is TestClaudeRunSettingsAfterConnecting's to find, through
// what Apply writes.
func TestClaudeEndpointEnvIsMagpies(t *testing.T) {
	var wrote []string
	all := append(slices.Clone(claudeEnv), claudeOwnEnv...)
	all = append(all, claudeContextEnv, claudeOutputEnv, claudeCapsEnv, claudeCompactEnv, claudeReviewEnv, claudeExploreEnv, claudeSearchEnv)
	for _, k := range all {
		if k != "ANTHROPIC_BASE_URL" && k != "ANTHROPIC_AUTH_TOKEN" && !slices.Contains(wrote, k) {
			wrote = append(wrote, k)
		}
	}
	listed := provider.ClaudeEndpointEnv()
	slices.Sort(wrote)
	slices.Sort(listed)
	if !slices.Equal(wrote, listed) {
		t.Fatalf("magpie writes %v beside its gateway; provider.ClaudeEndpointEnv is %v", wrote, listed)
	}
}

// Connected as magpie connects it, the user's settings.json holds magpie's
// gateway, models, picker, window and review switch beside the user's own;
// the copy an accounts run gets is the user's own alone, as it was before
// magpie was connected.
func TestClaudeRunSettingsAfterConnecting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := provider.Save(provider.Provider{ID: "cpa", Name: "CPA", Chat: "http://127.0.0.1:1/v1", Key: "k", Models: []string{"gpt-6-luna", "claude-sonnet-4-6"}}); err != nil {
		t.Fatal(err)
	}
	at := here(home)
	path := filepath.Join(at.claudeDir(), "settings.json")
	own := `{"theme": "dark", "env": {"MY_FLAG": "1"}, "permissions": {"allow": ["Bash(ls)"]}, "hooks": {"Stop": []}}`
	writeFile(t, path, own)
	if err := claudeIn(at).Apply("model", "cpa/gpt-6-luna"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := provider.ClaudeRunSettings(b)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(own), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the account's copy of\n%s\nis\n%s\nwant the user's own\n%s", b, out, own)
	}
}
