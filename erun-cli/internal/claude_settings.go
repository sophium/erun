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
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		return err
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")

	settings, err := loadSettingsFile(settingsPath)
	if err != nil {
		return err
	}

	if isBypassPermissionsConfigured(settings) {
		return nil
	}

	return writeBypassPermissionsSettings(settingsPath, settings)
}

func loadSettingsFile(path string) (map[string]json.RawMessage, error) {
	settings := make(map[string]json.RawMessage)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return settings, nil
	}
	if jsonErr := json.Unmarshal(data, &settings); jsonErr != nil || settings == nil {
		return make(map[string]json.RawMessage), nil
	}
	return settings, nil
}

func isBypassPermissionsConfigured(settings map[string]json.RawMessage) bool {
	return isBypassPermissionsMode(settings) && isSkipDangerousPromptEnabled(settings)
}

func isBypassPermissionsMode(settings map[string]json.RawMessage) bool {
	permData, ok := settings["permissions"]
	if !ok {
		return false
	}
	perms := make(map[string]json.RawMessage)
	if err := json.Unmarshal(permData, &perms); err != nil {
		return false
	}
	modeData, ok := perms["defaultMode"]
	if !ok {
		return false
	}
	var mode string
	return json.Unmarshal(modeData, &mode) == nil && mode == "bypassPermissions"
}

func isSkipDangerousPromptEnabled(settings map[string]json.RawMessage) bool {
	skipData, ok := settings["skipDangerousModePermissionPrompt"]
	if !ok {
		return false
	}
	var skip bool
	return json.Unmarshal(skipData, &skip) == nil && skip
}

func writeBypassPermissionsSettings(path string, settings map[string]json.RawMessage) error {
	perms := make(map[string]json.RawMessage)
	if permData, ok := settings["permissions"]; ok {
		_ = json.Unmarshal(permData, &perms)
		if perms == nil {
			perms = make(map[string]json.RawMessage)
		}
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
	return os.WriteFile(path, append(out, '\n'), 0o600)
}
