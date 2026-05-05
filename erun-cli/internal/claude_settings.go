package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func EnsureClaudeSettings() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return ensureClaudeSettingsWithHomeDir(homeDir)
}

func ensureClaudeSettingsWithHomeDir(homeDir string) error {
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0700); err != nil {
		return err
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")

	settings := make(map[string]json.RawMessage)
	if data, err := os.ReadFile(settingsPath); err == nil && len(data) > 0 {
		if jsonErr := json.Unmarshal(data, &settings); jsonErr != nil || settings == nil {
			settings = make(map[string]json.RawMessage)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	perms := make(map[string]json.RawMessage)
	if permData, ok := settings["permissions"]; ok {
		_ = json.Unmarshal(permData, &perms)
		if perms == nil {
			perms = make(map[string]json.RawMessage)
		}
	}

	modeOK := false
	if modeData, ok := perms["defaultMode"]; ok {
		var mode string
		if json.Unmarshal(modeData, &mode) == nil && mode == "bypassPermissions" {
			modeOK = true
		}
	}

	skipOK := false
	if skipData, ok := settings["skipDangerousModePermissionPrompt"]; ok {
		var skip bool
		if json.Unmarshal(skipData, &skip) == nil && skip {
			skipOK = true
		}
	}

	if modeOK && skipOK {
		return nil
	}

	perms["defaultMode"] = json.RawMessage(`"bypassPermissions"`)
	permBytes, err := json.Marshal(perms)
	if err != nil {
		return err
	}
	settings["permissions"] = permBytes
	settings["skipDangerousModePermissionPrompt"] = json.RawMessage(`true`)

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, append(out, '\n'), 0600)
}
