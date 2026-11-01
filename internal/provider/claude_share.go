package provider

// `magpie accounts run claude` (#992) brings the user's own Claude Code
// setup into the saved account's config directory, as claude-swap's run
// does: the user's CLAUDE.md, skills, commands, agents and rules linked,
// their settings.json copied without what points Claude Code at another
// endpoint, cloud or key, or was written for that endpoint's models, and
// their own MCP servers put in the account's
// .claude.json. Never a sign-in, history or anything else of
// ~/.claude.json. The gateway's runs in the directory (a request, /usage,
// a warm-up, the keep-alive) read none of it: they run with
// --setting-sources "" and --strict-mcp-config. keybindings.json isn't
// linked: Claude Code reads it from the directory whatever the setting
// sources, and the gateway's interactive mode (claudetui) presses Enter
// and Escape in it.
//
// .magpie-shared.json in the directory records what magpie put there, so
// the next run updates exactly those and nothing the account's directory
// has of its own. They stay after the run; forgetClaudeDir removes the
// directory, and os.RemoveAll removes a link, not what it points to.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/yetone/magpie/internal/agentenv"
	"github.com/yetone/magpie/internal/appdir"
)

const claudeShareRecordName = ".magpie-shared.json"

// claudeLinked are linked from the user's Claude Code folder as they are.
var claudeLinked = []string{"CLAUDE.md", "skills", "commands", "agents", "rules"}

// claudeShareRecord is what magpie put in an account's config directory:
// the links, the sum of the settings.json it wrote, and the MCP servers it
// put in the directory's .claude.json, each by the sum of what it wrote
// (jsonSum), "" once the account changed or removed it there.
type claudeShareRecord struct {
	Links    []string          `json:"links,omitempty"`
	Settings string            `json:"settings,omitempty"`
	MCP      map[string]string `json:"mcpServers,omitempty"`
}

// claudeEndpointEnv are what Claude Code's env says about the models of an
// endpoint the settings name: the models magpie writes while it routes
// Claude Code (agent's claudeEnv and claudeOwnEnv), which the account's own
// sign-in may not have, and the windows, reply length, capabilities, auto
// mode review and tool search it writes for them.
var claudeEndpointEnv = append([]string{
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_AUTO_MODE_MODEL",
	"CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_MAX_OUTPUT_TOKENS", "CLAUDE_CODE_MODEL_CAPABILITIES",
	"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_CODE_AUTO_MODE_SERVER", "CLAUDE_CODE_DISABLE_EXPLORE_INHERIT_CAP",
	"ENABLE_TOOL_SEARCH",
}, func() []string {
	var out []string
	for _, t := range []string{"OPUS", "SONNET", "HAIKU", "FABLE"} {
		for _, s := range []string{"_NAME", "_DESCRIPTION", "_SUPPORTED_CAPABILITIES"} {
			out = append(out, "ANTHROPIC_DEFAULT_"+t+"_MODEL"+s)
		}
	}
	return out
}()...)

// ClaudeEndpointEnv is claudeEndpointEnv, for agent's test that compares
// it with the variable constants claude.go names.
func ClaudeEndpointEnv() []string { return slices.Clone(claudeEndpointEnv) }

// ClaudeRunSettings is claudeRunSettings, for agent's test of it on the
// settings magpie writes.
func ClaudeRunSettings(b []byte) ([]byte, error) { return claudeRunSettings(b, false) }

// shareClaudeSetup brings the user's Claude Code setup into the account's
// config directory dir. It says what it left out, and why. elsewhere says
// the run's environment sends Claude Code past Anthropic's API
// (claudeElsewhere), so the models the settings name are that endpoint's.
func shareClaudeSetup(dir string, elsewhere bool) (notes []string) {
	recPath := filepath.Join(dir, claudeShareRecordName)
	var rec claudeShareRecord
	if b, err := os.ReadFile(recPath); err == nil {
		if err := json.Unmarshal(b, &rec); err != nil {
			return []string{fmt.Sprintf("%s can't be read (%v): your Claude Code setup isn't brought in", recPath, err)}
		}
	} else if !os.IsNotExist(err) {
		return []string{fmt.Sprintf("%s can't be read (%v): your Claude Code setup isn't brought in", recPath, err)}
	}
	src := appdir.ClaudeDir()
	var now claudeShareRecord
	var own, unlinked []string
	var linkErr error
	for _, name := range claudeLinked {
		to := filepath.Join(dir, name)
		ours := slices.Contains(rec.Links, name)
		fi, err := os.Lstat(to)
		switch {
		case err == nil && (!ours || fi.Mode()&os.ModeSymlink == 0):
			if _, err := os.Stat(filepath.Join(src, name)); err == nil {
				own = append(own, name)
			}
			continue
		case err != nil && !os.IsNotExist(err):
			if ours {
				now.Links = append(now.Links, name)
			}
			continue
		}
		from := filepath.Join(src, name)
		if r, err := filepath.EvalSymlinks(from); err == nil {
			// a link's own target: Claude Code writing through a link to a
			// link replaces the one in between with a file
			from = r
		} else {
			if os.IsNotExist(err) && fi != nil {
				_ = os.Remove(to) // gone from the user's folder
			} else if fi != nil {
				now.Links = append(now.Links, name)
			}
			continue
		}
		if fi != nil {
			if cur, _ := os.Readlink(to); cur == from {
				now.Links = append(now.Links, name)
				continue
			}
			if err := os.Remove(to); err != nil {
				now.Links = append(now.Links, name)
				continue
			}
		}
		if err := os.Symlink(from, to); err != nil {
			unlinked, linkErr = append(unlinked, name), err
			continue
		}
		now.Links = append(now.Links, name)
	}
	sum, ownSettings, note := shareClaudeSettings(dir, src, rec.Settings, elsewhere)
	now.Settings = sum
	if note != "" {
		notes = append(notes, note)
	}
	if ownSettings {
		own = append(own, "settings.json")
	}
	var mcpNotes []string
	now.MCP, mcpNotes = shareClaudeMCP(dir, rec.MCP)
	notes = append(notes, mcpNotes...)
	if len(own) > 0 {
		notes = append(notes, "kept the account's own "+strings.Join(own, ", ")+" in "+dir+", not yours")
	}
	if len(unlinked) > 0 {
		notes = append(notes, fmt.Sprintf("%s not linked in (%v)", strings.Join(unlinked, ", "), linkErr))
	}
	switch {
	case len(now.Links) == 0 && now.Settings == "" && len(now.MCP) == 0:
		_ = os.Remove(recPath)
	default:
		b, _ := json.MarshalIndent(now, "", "  ")
		if err := writePrivate(recPath, append(b, '\n')); err != nil {
			notes = append(notes, fmt.Sprintf("%s not written: %v", recPath, err))
		}
	}
	return notes
}

// shareClaudeSettings writes the user's settings.json into dir, without
// what would take Claude Code off the account's own sign-in, when dir has
// none of its own. sum is the copy magpie wrote last; a copy changed since
// (Claude Code's /config, the user) is the account's own now, and kept:
// own says dir has one of its own beside the user's.
func shareClaudeSettings(dir, src, sum string, elsewhere bool) (written string, own bool, note string) {
	to := filepath.Join(dir, "settings.json")
	fi, err := os.Lstat(to)
	switch {
	case err == nil && (sum == "" || !fi.Mode().IsRegular() || fileSum(to) != sum):
		_, err := os.Stat(filepath.Join(src, "settings.json"))
		return "", err == nil, ""
	case err != nil && !os.IsNotExist(err):
		return sum, false, ""
	}
	b, err := os.ReadFile(filepath.Join(src, "settings.json"))
	switch {
	case os.IsNotExist(err):
		if fi != nil {
			_ = os.Remove(to) // gone from the user's folder
		}
		return "", false, ""
	case err != nil:
		return sum, false, ""
	}
	out, err := claudeRunSettings(b, elsewhere)
	if err != nil {
		return sum, false, fmt.Sprintf("your settings.json isn't brought in: %v", err)
	}
	if fi == nil || fileSum(to) != sumOf(out) {
		if err := writePrivate(to, out); err != nil {
			return sum, false, fmt.Sprintf("settings.json not written in %s: %v", dir, err)
		}
	}
	return sumOf(out), false, ""
}

// claudeRunSettings is settings.json for Claude Code run on a saved
// account: none of what would point it at another endpoint, cloud or key
// (claudeWiring, the provider switches of agentenv.ClaudeProviders, on or
// off, and apiKeyHelper, which Claude Code would show beside the sign-in as
// "Both … set"), and, where it or the run's environment (elsewhere) sends
// Claude Code past Anthropic's API (claudeElsewhere: magpie's gateway while
// connected, Bedrock, Vertex AI, …), none of what was written for that
// endpoint's models either: their names, windows, picker and review switch.
func claudeRunSettings(b []byte, elsewhere bool) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("not an object")
	}
	delete(m, "apiKeyHelper")
	var env map[string]json.RawMessage
	if raw, ok := m["env"]; ok {
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("its env: %w", err)
		}
	}
	if elsewhere || claudeElsewhere(func(k string) (string, bool) {
		v, ok := env[k]
		return settingsEnvText(v), ok
	}) {
		for _, k := range claudeEndpointEnv {
			delete(env, k)
		}
		for _, k := range []string{"model", "modelPicker", "autoCompactWindow"} {
			delete(m, k)
		}
		if err := claudeOwnModelSettings(m); err != nil {
			return nil, err
		}
	}
	if _, ok := m["env"]; ok {
		for k := range env {
			if claudeWiring(k) || slices.Contains(agentenv.ClaudeProviders, k) {
				delete(env, k)
			}
		}
		if len(env) == 0 {
			delete(m, "env")
		} else {
			m["env"], _ = json.Marshal(env)
		}
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// claudeOwnModelSettings keeps of m's modelSettings the entries named for a
// Claude model itself (claude-opus-5-5: /effort's and magpie tune's), which
// the account's sign-in runs too; one named for a model handed through the
// endpoint (cpa/gpt-6-luna, Claude Desktop's mythos-magpie-<n>) or a cloud's
// id of a Claude model (Vertex AI's claude-opus-5-5@20261001) goes.
func claudeOwnModelSettings(m map[string]json.RawMessage) error {
	raw, ok := m["modelSettings"]
	if !ok {
		return nil
	}
	var per map[string]json.RawMessage
	if err := json.Unmarshal(raw, &per); err != nil {
		return fmt.Errorf("its modelSettings: %w", err)
	}
	for id := range per {
		if !strings.HasPrefix(strings.ToLower(id), "claude-") || strings.ContainsAny(id, "/@") {
			delete(per, id)
		}
	}
	if len(per) == 0 {
		delete(m, "modelSettings")
		return nil
	}
	m["modelSettings"], _ = json.Marshal(per)
	return nil
}

// shareClaudeMCP puts the user's own MCP servers (~/.claude.json's
// top-level mcpServers) in the .claude.json of dir, which claudeSavedDir
// made, changing nothing else there. wrote is what magpie put there before
// (claudeShareRecord's MCP): a server still as magpie wrote it follows the
// user's, and goes when the user's goes; one changed or removed in dir
// since (`claude mcp remove` in an accounts run) is the account's from
// then on, left as the account left it until the user's own of that name
// goes. A server of that name the account had itself is kept.
func shareClaudeMCP(dir string, wrote map[string]string) (map[string]string, []string) {
	var user map[string]json.RawMessage
	switch b, err := os.ReadFile(claudeProfilePath()); {
	case os.IsNotExist(err):
	case err != nil:
		return wrote, nil
	default:
		var cfg struct {
			MCP map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return wrote, []string{fmt.Sprintf("your MCP servers aren't brought in: %s: %v", claudeProfilePath(), err)}
		}
		user = cfg.MCP
	}
	to := filepath.Join(dir, ".claude.json")
	if fi, err := os.Lstat(to); err != nil || !fi.Mode().IsRegular() {
		return wrote, nil
	}
	if len(user) == 0 && len(wrote) == 0 {
		return nil, nil
	}
	lock := to
	if r, err := filepath.EvalSymlinks(to); err == nil {
		lock = r
	}
	// a Claude Code running there saves the file under this lock
	release, err := takeClaudeLock(lock+".lock", claudeConfigStale)
	if err != nil {
		return wrote, []string{fmt.Sprintf("your MCP servers aren't brought in: %v", err)}
	}
	defer release()
	b, err := os.ReadFile(to)
	if err != nil {
		return wrote, nil
	}
	var cfg map[string]json.RawMessage
	if json.Unmarshal(b, &cfg) != nil || cfg == nil {
		return wrote, nil
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := cfg["mcpServers"]; ok && json.Unmarshal(raw, &servers) != nil {
		return wrote, nil
	}
	changed := false
	for name, sum := range wrote {
		if _, still := user[name]; still {
			continue
		}
		if have, has := servers[name]; has && sum != "" && jsonSum(have) == sum {
			delete(servers, name)
			changed = true
		}
	}
	names := make([]string, 0, len(user))
	for name := range user {
		names = append(names, name)
	}
	sort.Strings(names)
	now := map[string]string{}
	var kept, left []string
	for _, name := range names {
		have, has := servers[name]
		sum, ours := wrote[name]
		switch {
		case ours && (sum == "" || !has || jsonSum(have) != sum):
			now[name] = ""
			if has {
				kept = append(kept, name)
			} else {
				left = append(left, name)
			}
		case has && !ours:
			kept = append(kept, name)
		default:
			if !has || jsonSum(have) != jsonSum(user[name]) {
				servers[name] = user[name]
				changed = true
			}
			now[name] = jsonSum(user[name])
		}
	}
	if len(now) == 0 {
		now = nil
	}
	var notes []string
	if len(kept) > 0 {
		notes = append(notes, "kept the account's own MCP server "+strings.Join(kept, ", ")+", not yours")
	}
	if len(left) > 0 {
		notes = append(notes, "left out your MCP server "+strings.Join(left, ", ")+": it was removed from "+to)
	}
	if !changed {
		return now, notes
	}
	if len(servers) == 0 {
		delete(cfg, "mcpServers")
	} else {
		cfg["mcpServers"], _ = json.Marshal(servers)
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return wrote, notes
	}
	if err := writePrivate(to, append(out, '\n')); err != nil {
		return wrote, append(notes, fmt.Sprintf("your MCP servers aren't brought in: %v", err))
	}
	return now, notes
}

// jsonSum is the sum of a JSON value whatever its layout and key order, so
// .claude.json saved again by Claude Code reads as the same servers.
func jsonSum(raw json.RawMessage) string {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return sumOf(raw)
	}
	b, _ := json.Marshal(v)
	return sumOf(b)
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fileSum is the sum of the file at path, "" when it can't be read.
func fileSum(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return sumOf(b)
}
