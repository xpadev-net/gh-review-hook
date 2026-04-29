package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read file: %v", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return root
}

func getStopCommands(t *testing.T, root map[string]json.RawMessage) []string {
	t.Helper()
	hooksRaw, ok := root["hooks"]
	if !ok {
		return nil
	}
	var hooksMap map[string]json.RawMessage
	if err := json.Unmarshal(hooksRaw, &hooksMap); err != nil {
		t.Fatalf("cannot parse hooks: %v", err)
	}
	stopRaw, ok := hooksMap["Stop"]
	if !ok {
		return nil
	}
	var groupsRaw []json.RawMessage
	if err := json.Unmarshal(stopRaw, &groupsRaw); err != nil {
		t.Fatalf("cannot parse Stop array: %v", err)
	}
	var cmds []string
	for _, raw := range groupsRaw {
		var group struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(raw, &group); err != nil {
			continue // skip malformed entries
		}
		for _, h := range group.Hooks {
			cmds = append(cmds, h.Command)
		}
	}
	return cmds
}

func TestMergeHook_NewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	alreadyInstalled, err := mergeHook(path, "/usr/local/bin/gh-review-hook")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if alreadyInstalled {
		t.Fatal("expected not already installed")
	}
	root := readJSON(t, path)
	cmds := getStopCommands(t, root)
	if len(cmds) != 1 || cmds[0] != "/usr/local/bin/gh-review-hook" {
		t.Fatalf("unexpected commands: %v", cmds)
	}
}

func TestMergeHook_PreservesExistingFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	existing := `{"permissions":{"allow":["Bash"]},"model":"claude-opus-4-5"}`
	os.WriteFile(path, []byte(existing), 0o600)

	_, err := mergeHook(path, "/usr/bin/gh-review-hook")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	root := readJSON(t, path)
	if _, ok := root["permissions"]; !ok {
		t.Fatal("permissions field lost")
	}
	if _, ok := root["model"]; !ok {
		t.Fatal("model field lost")
	}
	cmds := getStopCommands(t, root)
	if len(cmds) != 1 || cmds[0] != "/usr/bin/gh-review-hook" {
		t.Fatalf("unexpected commands: %v", cmds)
	}
}

func TestMergeHook_AppendsToExistingStop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	existing := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/other/hook"}]}]}}`
	os.WriteFile(path, []byte(existing), 0o600)

	_, err := mergeHook(path, "/usr/bin/gh-review-hook")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cmds := getStopCommands(t, readJSON(t, path))
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d: %v", len(cmds), cmds)
	}
}

func TestMergeHook_IdempotentExactPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	_, err := mergeHook(path, "/usr/bin/gh-review-hook")
	if err != nil {
		t.Fatalf("first install error: %v", err)
	}

	fi1, _ := os.Stat(path)
	alreadyInstalled, err := mergeHook(path, "/usr/bin/gh-review-hook")
	if err != nil {
		t.Fatalf("second install error: %v", err)
	}
	if !alreadyInstalled {
		t.Fatal("expected alreadyInstalled=true on second call")
	}
	fi2, _ := os.Stat(path)
	if fi1.ModTime() != fi2.ModTime() {
		t.Fatal("file was modified despite already being installed")
	}
}

func TestMergeHook_MalformedStopEntrySkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	// hooks array contains a malformed entry (hooks is a string, not array)
	existing := `{"hooks":{"Stop":[{"hooks":"not-an-array"}]}}`
	os.WriteFile(path, []byte(existing), 0o600)

	alreadyInstalled, err := mergeHook(path, "/usr/bin/gh-review-hook")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if alreadyInstalled {
		t.Fatal("malformed entry should not trigger alreadyInstalled")
	}
	cmds := getStopCommands(t, readJSON(t, path))
	if len(cmds) != 1 || cmds[0] != "/usr/bin/gh-review-hook" {
		t.Fatalf("unexpected commands: %v", cmds)
	}
}

func TestMergeHook_PreservesOtherHookEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	existing := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"/pre/hook"}]}]}}`
	os.WriteFile(path, []byte(existing), 0o600)

	_, err := mergeHook(path, "/usr/bin/gh-review-hook")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	root := readJSON(t, path)
	var hooksMap map[string]json.RawMessage
	json.Unmarshal(root["hooks"], &hooksMap)
	if _, ok := hooksMap["PreToolUse"]; !ok {
		t.Fatal("PreToolUse event was lost")
	}
	if _, ok := hooksMap["Stop"]; !ok {
		t.Fatal("Stop event not added")
	}
}

func TestMergeHook_NoTmpFileLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	_, err := mergeHook(path, "/usr/bin/gh-review-hook")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal(".tmp file was left behind")
	}
}

func TestMergeHook_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dir", "settings.json")

	_, err := mergeHook(path, "/usr/bin/gh-review-hook")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("settings file not created")
	}
}
