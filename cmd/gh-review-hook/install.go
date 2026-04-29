package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type installTarget struct {
	label string
	path  string
}

func buildTargets() ([]installTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot determine working directory: %w", err)
	}
	return []installTarget{
		{
			label: "Global (~/.claude/settings.json) – all projects",
			path:  filepath.Join(home, ".claude", "settings.json"),
		},
		{
			label: "Global local (~/.claude/settings.local.json) – all projects, not git-tracked",
			path:  filepath.Join(home, ".claude", "settings.local.json"),
		},
		{
			label: "Project (./.claude/settings.json) – this project, git-tracked",
			path:  filepath.Join(cwd, ".claude", "settings.json"),
		},
		{
			label: "Project local (./.claude/settings.local.json) – this project, not git-tracked",
			path:  filepath.Join(cwd, ".claude", "settings.local.json"),
		},
	}, nil
}

// mergeHook reads the settings file at path (creating it if absent),
// merges in the gh-review-hook Stop entry idempotently, and writes back.
// Returns (alreadyInstalled bool, err error).
func mergeHook(path, binaryPath string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("cannot read %s: %w", path, err)
	}

	var root map[string]json.RawMessage
	if len(data) == 0 {
		root = make(map[string]json.RawMessage)
	} else {
		if err := json.Unmarshal(data, &root); err != nil {
			return false, fmt.Errorf("cannot parse %s: %w", path, err)
		}
	}

	var hooksMap map[string]json.RawMessage
	if raw, ok := root["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooksMap); err != nil {
			return false, fmt.Errorf("cannot parse hooks field in %s: %w", path, err)
		}
	} else {
		hooksMap = make(map[string]json.RawMessage)
	}

	var stopRaw []json.RawMessage
	if raw, ok := hooksMap["Stop"]; ok {
		if err := json.Unmarshal(raw, &stopRaw); err != nil {
			return false, fmt.Errorf("cannot parse hooks.Stop in %s: %w", path, err)
		}
	}

	resolvedBinary, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		resolvedBinary = binaryPath
	}

	for _, groupRaw := range stopRaw {
		var group struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(groupRaw, &group); err != nil {
			continue
		}
		for _, h := range group.Hooks {
			resolvedCmd, err := filepath.EvalSymlinks(h.Command)
			if err != nil {
				resolvedCmd = h.Command
			}
			if resolvedCmd == resolvedBinary || h.Command == binaryPath {
				return true, nil
			}
		}
	}

	newGroup := map[string]interface{}{
		"hooks": []map[string]string{
			{"type": "command", "command": binaryPath},
		},
	}
	newGroupBytes, err := json.Marshal(newGroup)
	if err != nil {
		return false, fmt.Errorf("cannot marshal new hook entry: %w", err)
	}
	stopRaw = append(stopRaw, json.RawMessage(newGroupBytes))

	stopBytes, err := json.Marshal(stopRaw)
	if err != nil {
		return false, fmt.Errorf("cannot marshal Stop array: %w", err)
	}
	hooksMap["Stop"] = json.RawMessage(stopBytes)

	hooksBytes, err := json.Marshal(hooksMap)
	if err != nil {
		return false, fmt.Errorf("cannot marshal hooks map: %w", err)
	}
	root["hooks"] = json.RawMessage(hooksBytes)

	// map encoding does not preserve key ordering; values are preserved exactly.
	output, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, fmt.Errorf("cannot marshal settings: %w", err)
	}
	output = append(output, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("cannot create directory %s: %w", dir, err)
	}

	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode()
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, output, mode); err != nil {
		return false, fmt.Errorf("cannot write temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return false, fmt.Errorf("cannot rename %s to %s: %w", tmpPath, path, err)
	}

	return false, nil
}

func runInstall() int {
	targets, err := buildTargets()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	binaryPath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot determine executable path: "+err.Error())
		return 1
	}

	selected, cancelled, err := selectTarget(targets)
	if err != nil {
		fmt.Fprintln(os.Stderr, "TUI error: "+err.Error())
		return 1
	}
	if cancelled {
		fmt.Fprintln(os.Stderr, "Installation cancelled.")
		return 130
	}

	alreadyInstalled, err := mergeHook(targets[selected].path, binaryPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	if alreadyInstalled {
		fmt.Printf("gh-review-hook is already installed in %s\n", targets[selected].path)
	} else {
		fmt.Printf("Installed gh-review-hook hook into %s\n", targets[selected].path)
		fmt.Printf("Binary: %s\n", binaryPath)
		fmt.Println("Note: JSON keys in the settings file may have been reordered (Go map encoding is alphabetical).")
	}
	return 0
}
