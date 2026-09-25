package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aidan-bailey/loom/config"
)

// CountUsers counts the stored sessions that run on account name across
// the state.json files in configDirs. Read-only: unlike
// config.LoadStateFrom it never quarantines a corrupt file. A missing file
// counts zero; an unreadable or corrupt one is an error, because "is this
// account in use" must not be answered with a guess. A repeated dir counts
// once.
func CountUsers(configDirs []string, name string) (int, error) {
	seen := map[string]bool{}
	n := 0
	for _, dir := range configDirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		path := filepath.Join(dir, config.StateFileName)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", path, err)
		}
		var st struct {
			Instances []struct {
				Account string `json:"account"`
			} `json:"instances"`
		}
		if err := json.Unmarshal(data, &st); err != nil {
			return 0, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, in := range st.Instances {
			if in.Account == name {
				n++
			}
		}
	}
	return n, nil
}

// KnownStateDirs lists every config dir that can hold sessions: the global
// dir, LOOM_HOME's when it differs, and each registered workspace's.
func KnownStateDirs() ([]string, error) {
	global, err := config.GetGlobalConfigDir()
	if err != nil {
		return nil, err
	}
	dirs := []string{global}
	if home, err := config.GetConfigDir(); err == nil && home != global {
		dirs = append(dirs, home)
	}
	reg, err := config.LoadWorkspaceRegistry()
	if err != nil {
		return nil, err
	}
	for i := range reg.Workspaces {
		dirs = append(dirs, config.WorkspaceConfigDir(&reg.Workspaces[i]))
	}
	return dirs, nil
}
