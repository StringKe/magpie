package provider

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// claudeRTHolders are the places a Claude Code could refresh the refresh
// token rt from: Claude Code's own sign-in, and each saved account's folder.
func claudeRTHolders(t *testing.T, cred, rt string) []string {
	t.Helper()
	var at []string
	if b, _ := os.ReadFile(cred); strings.Contains(string(b), rt) {
		at = append(at, "Claude Code")
	}
	for _, l := range readLogins() {
		if c, ok := readClaudeDir(claudeAccountDir(l.User)); ok && c.OAuth.RefreshToken == rt {
			at = append(at, l.User+"'s folder")
		}
	}
	return at
}

// handOvers records the accounts the gateway is told were handed to Claude
// Code itself.
func handOvers(t *testing.T) *[]string {
	t.Helper()
	var handed []string
	old := ClaudeHandedOver
	t.Cleanup(func() { ClaudeHandedOver = old })
	ClaudeHandedOver = func(u string) { handed = append(handed, u) }
	return &handed
}

// unwritableClaudeProfile makes ~/.claude.json a link to a file not made
// yet, in a folder magpie can't write: read as no file, and its write
// fails. The returned func mends it.
func unwritableClaudeProfile(t *testing.T, home string) (mend func()) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a folder without write permission is written to here")
	}
	profile := filepath.Join(home, ".claude.json")
	ro := filepath.Join(home, "ro")
	if err := os.Mkdir(ro, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(profile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ro, ".claude.json"), profile); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	return func() {
		_ = os.Chmod(ro, 0o700)
		if err := os.Remove(profile); err != nil {
			t.Fatal(err)
		}
	}
}

// StringKe's audit, #4532: a switch to a, whose folder holds its sign-in,
// that fails on ~/.claude.json after Claude Code's sign-in was written left
// a's refresh token in two places, Claude Code's and the folder's, under a
// profile that still named b. Trying again was "already on a", and the
// folder stayed. Now Claude Code keeps b's sign-in, or stays signed out if
// it was, the folder stays a's one holder, and the switch tried again once
// the file mends hands a over.
func TestClaudeSwitchFailingOnItsProfileLeavesOneHolder(t *testing.T) {
	for _, name := range []string{"unparsable", "unwritable", "signed out"} {
		t.Run(name, func(t *testing.T) {
			home, cred := claudeLockAccounts(t)
			handed := handOvers(t)
			if _, err := claudeSavedDir("a@example.com"); err != nil {
				t.Fatal(err)
			}
			profile := filepath.Join(home, ".claude.json")
			mend := func() {}
			switch name {
			case "unparsable":
				// cut off mid-write by another program
				if err := os.WriteFile(profile, []byte(`{"oauthAccount": {"emailAddress": "b@exa`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unwritable":
				mend = unwritableClaudeProfile(t, home)
			case "signed out":
				// logged out of Claude Code: the switch writes a sign-in
				// where there was none
				if err := os.Remove(cred); err != nil {
					t.Fatal(err)
				}
				forgetClaudeCredential()
				mend = unwritableClaudeProfile(t, home)
			}
			forgetAccountCaches()
			if err := SwitchLogin("claude", "a@example.com"); err == nil {
				t.Fatal("switched with a ~/.claude.json that couldn't be rewritten")
			}
			if name == "signed out" {
				if _, err := os.Stat(cred); !os.IsNotExist(err) {
					b, _ := os.ReadFile(cred)
					t.Fatalf("Claude Code left signed in to a after the switch failed: %s", b)
				}
			} else if b, _ := os.ReadFile(cred); !strings.Contains(string(b), "sk-ant-ort01-b") {
				t.Fatalf("Claude Code left on a's sign-in after the switch failed: %s", b)
			}
			if h := claudeRTHolders(t, cred, "sk-ant-ort01-old"); len(h) != 1 {
				t.Fatalf("a's refresh token held by %v after the switch failed", h)
			}
			if len(*handed) != 0 {
				t.Fatalf("handed over after a failed switch: %v", *handed)
			}
			// the file mended, the switch is tried again
			mend()
			writeFile(t, profile, map[string]any{"oauthAccount": map[string]any{"emailAddress": "b@example.com", "accountUuid": "u-b"}})
			forgetClaudeCredential()
			forgetAccountCaches()
			if err := SwitchLogin("claude", "a@example.com"); err != nil {
				t.Fatal(err)
			}
			if h := claudeRTHolders(t, cred, "sk-ant-ort01-old"); !slices.Equal(h, []string{"Claude Code"}) {
				t.Fatalf("a's refresh token held by %v after the switch", h)
			}
			if _, err := os.Stat(claudeAccountDir("a@example.com")); !os.IsNotExist(err) {
				t.Fatalf("a's folder kept after the switch: %v", err)
			}
			if !slices.Equal(*handed, []string{"a@example.com"}) {
				t.Fatalf("handed over %v", *handed)
			}
			if b, _ := os.ReadFile(profile); !strings.Contains(string(b), "a@example.com") {
				t.Fatalf("Claude Code's profile not a's: %s", b)
			}
		})
	}
}

// StringKe's audit, #4532: a first sign-in while Claude Code is signed out
// is put in Claude Code, and a ~/.claude.json write that failed after it
// left that sign-in there, under no profile of its own, with the account
// not kept. Now Claude Code stays signed out and logins.json as it was.
func TestClaudeSignInFailingOnItsProfileLeavesClaudeCodeAsItWas(t *testing.T) {
	home := claudeHome(t)
	unwritableClaudeProfile(t, home)
	l, err := claudeLogin(claudeCredentials{OAuth: claudeAuth{
		AccessToken: "sk-ant-oat01-new", RefreshToken: "sk-ant-ort01-new",
		ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), SubscriptionType: "max",
	}}, map[string]any{"emailAddress": "new@example.com", "accountUuid": "u-new"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(loginsPath())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := addLogin(l); err == nil {
		t.Fatal("signed in with a ~/.claude.json that couldn't be rewritten")
	}
	cred := claudeCredentialsPath()
	if _, err := os.Stat(cred); !os.IsNotExist(err) {
		b, _ := os.ReadFile(cred)
		t.Fatalf("Claude Code left signed in to new after the sign-in failed: %s", b)
	}
	after, err := os.ReadFile(loginsPath())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("logins.json changed on a failed sign-in: %s", after)
	}
}

// StringKe's audit, #4532: Claude Code put on a by another tool (cswap)
// while a's folder holds its sign-in. A switch to a was "already on a" and
// left the folder, a second holder of a's refresh token. Now the folder
// goes and the gateway is told; when the folder's sign-in is the later one
// (Claude Code refreshed it there, and the tool put a spent copy in),
// Claude Code is given the folder's.
func TestClaudeSwitchToTheAccountOnHandsItsFolderOver(t *testing.T) {
	for _, name := range []string{"same", "folder later"} {
		t.Run(name, func(t *testing.T) {
			home, cred := claudeLockAccounts(t)
			handed := handOvers(t)
			dir, err := claudeSavedDir("a@example.com")
			if err != nil {
				t.Fatal(err)
			}
			if name == "folder later" {
				writeFile(t, filepath.Join(dir, ".credentials.json"), map[string]any{"claudeAiOauth": map[string]any{
					"accessToken": "sk-ant-oat01-a2", "refreshToken": "sk-ant-ort01-a2",
					"expiresAt": time.Now().Add(8 * time.Hour).UnixMilli(), "subscriptionType": "max"}})
			}
			// the other tool signs Claude Code in to a, with the sign-in it kept
			writeFile(t, cred, map[string]any{"claudeAiOauth": map[string]any{"accessToken": "sk-ant-oat01-old", "refreshToken": "sk-ant-ort01-old",
				"expiresAt": time.Now().Add(time.Hour).UnixMilli(), "subscriptionType": "max"}})
			writeFile(t, filepath.Join(home, ".claude.json"), map[string]any{"oauthAccount": map[string]any{"emailAddress": "a@example.com", "accountUuid": "u-a"}})
			forgetClaudeCredential()
			forgetAccountCaches()
			rememberLogins(true)
			if err := SwitchLogin("claude", "a@example.com"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("a's folder kept beside Claude Code's own sign-in: %v", err)
			}
			if !slices.Equal(*handed, []string{"a@example.com"}) {
				t.Fatalf("handed over %v", *handed)
			}
			want := "sk-ant-ort01-old"
			if name == "folder later" {
				want = "sk-ant-ort01-a2"
			}
			if b, _ := os.ReadFile(cred); !strings.Contains(string(b), want) {
				t.Fatalf("Claude Code not on a's %s: %s", want, b)
			}
			if a := savedAuth(t, "a@example.com"); !strings.Contains(a, want) {
				t.Fatalf("a saved without its %s: %s", want, a)
			}
		})
	}
}

// #4750 beside #4532's 1.3: a switch to the account Claude Code is on
// already (another tool put it there before magpie looked, or a switch
// whose undo failed) removed the account's folder but left Held on the
// account before, so a run queued behind it made the folder again from
// logins.json: a second holder of Claude Code's own refresh token.
func TestClaudeSwitchToTheAccountOnMarksItHeld(t *testing.T) {
	home, cred := claudeLockAccounts(t)
	dir, err := claudeSavedDir("a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	// the other tool signs Claude Code in to a; magpie hasn't looked since
	writeFile(t, cred, map[string]any{"claudeAiOauth": map[string]any{"accessToken": "sk-ant-oat01-old", "refreshToken": "sk-ant-ort01-old",
		"expiresAt": time.Now().Add(time.Hour).UnixMilli(), "subscriptionType": "max"}})
	writeFile(t, filepath.Join(home, ".claude.json"), map[string]any{"oauthAccount": map[string]any{"emailAddress": "a@example.com", "accountUuid": "u-a"}})
	forgetClaudeCredential()
	forgetAccountCaches()
	if err := SwitchLogin("claude", "a@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the fixture: a's folder kept beside Claude Code's own sign-in: %v", err)
	}
	if _, err := claudeSavedDir("a@example.com"); err == nil {
		t.Fatalf("a run queued behind the switch got the handed-over account's directory again; held: %v", heldClaude())
	}
	if h := claudeRTHolders(t, cred, "sk-ant-ort01-old"); !slices.Equal(h, []string{"Claude Code"}) {
		t.Fatalf("a's refresh token held by %v", h)
	}
}

// heldClaude are the saved Claude accounts logins.json marks Held.
func heldClaude() []string {
	var held []string
	for _, l := range readLogins() {
		if l.Agent == "claude" && l.Held {
			held = append(held, l.User)
		}
	}
	return held
}

// StringKe's audit, #4532: another tool switched Claude Code to a and back,
// and Claude Code refreshed a's sign-in meanwhile: logins.json has the new
// one, a's folder the one that refresh spent. The folder's was taken over
// the saved one, and a's next run there was refused. Now the later expiry
// wins: the saved sign-in stays, and the folder starts from it again. A
// sign-in imported (expiresAt 1) and refreshed in the folder is the
// folder's still.
func TestClaudeOlderFolderCopyGivesWay(t *testing.T) {
	t.Run("folder older", func(t *testing.T) {
		home, cred := claudeLockAccounts(t)
		dir, err := claudeSavedDir("a@example.com")
		if err != nil {
			t.Fatal(err)
		}
		profile := filepath.Join(home, ".claude.json")
		writeFile(t, cred, map[string]any{"claudeAiOauth": map[string]any{"accessToken": "sk-ant-oat01-a2", "refreshToken": "sk-ant-ort01-a2",
			"expiresAt": time.Now().Add(8 * time.Hour).UnixMilli(), "subscriptionType": "max"}})
		writeFile(t, profile, map[string]any{"oauthAccount": map[string]any{"emailAddress": "a@example.com", "accountUuid": "u-a"}})
		forgetClaudeCredential()
		forgetAccountCaches()
		rememberLogins(true)
		claudeWriteB(t, cred, "b")
		writeFile(t, profile, map[string]any{"oauthAccount": map[string]any{"emailAddress": "b@example.com", "accountUuid": "u-b"}})
		forgetClaudeCredential()
		forgetAccountCaches()
		rememberLogins(true)
		if a := savedAuth(t, "a@example.com"); !strings.Contains(a, "sk-ant-ort01-a2") {
			t.Fatalf("the fixture: a saved as %s", a)
		}
		if _, err := claudeSavedDir("a@example.com"); err != nil {
			t.Fatal(err)
		}
		if a := savedAuth(t, "a@example.com"); !strings.Contains(a, "sk-ant-ort01-a2") {
			t.Fatalf("a's older folder copy taken over its saved sign-in: %s", a)
		}
		if c, _ := readClaudeDir(dir); c.OAuth.RefreshToken != "sk-ant-ort01-a2" {
			t.Fatalf("a's folder runs on %q", c.OAuth.RefreshToken)
		}
	})
	t.Run("imported", func(t *testing.T) {
		claudeLockAccounts(t)
		loginsMu.Lock()
		ls := readLogins()
		for i := range ls {
			if ls[i].User == "a@example.com" {
				ls[i].Auth = mustJSONRaw(t, map[string]any{"claudeAiOauth": map[string]any{"accessToken": "sk-ant-oat01-imp", "refreshToken": "sk-ant-ort01-imp", "expiresAt": 1}})
			}
		}
		err := writeLogins(ls)
		loginsMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		dir, err := claudeSavedDir("a@example.com")
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, ".credentials.json"), map[string]any{"claudeAiOauth": map[string]any{
			"accessToken": "sk-ant-oat01-a3", "refreshToken": "sk-ant-ort01-a3",
			"expiresAt": time.Now().Add(8 * time.Hour).UnixMilli(), "subscriptionType": "max"}})
		syncSavedClaude("a@example.com")
		if a := savedAuth(t, "a@example.com"); !strings.Contains(a, "sk-ant-ort01-a3") {
			t.Fatalf("the sign-in Claude Code refreshed an import to wasn't kept: %s", a)
		}
	})
}
