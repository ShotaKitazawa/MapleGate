// Package settings reads the few values that tell the client which deployment to talk to. None of them is secret.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Settings struct {
	StartURL  string `json:"start_url"`
	Region    string `json:"region"`
	AccountID string `json:"account_id"`
	RoleName  string `json:"role_name"`
	APIURL    string `json:"api_url"`
	// GameProcesses are the program names that mean the game is running; with none, the tunnel is never ended for lack of the game.
	GameProcesses []string `json:"game_processes,omitempty"`
}

// Dir is where the settings and the sign in cache live: the user's own application support folder.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "MapleGate"), nil
}

func Load(dir string) (Settings, error) {
	p := filepath.Join(dir, "config.json")
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, fmt.Errorf("%s does not exist; ask for the settings file and put it there", p)
	}
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", p, err)
	}
	if s.RoleName == "" {
		s.RoleName = "gamevpn-player"
	}
	for name, v := range map[string]string{"start_url": s.StartURL, "region": s.Region, "account_id": s.AccountID, "api_url": s.APIURL} {
		if v == "" {
			return Settings{}, fmt.Errorf("%s: %s is missing", p, name)
		}
	}
	return s, nil
}
