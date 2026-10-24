package eruncommon

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

const (
	runtimeUsageHistoryFileName = "usage-history.json"
)

// LoadRuntimeUsageHistory reads an environment's retained usage. A missing or
// unreadable history is "nothing observed yet", not an error: the monitor tick
// that writes it must never fail over its own bookkeeping, and every consumer
// already has to handle an environment with no evidence.
func LoadRuntimeUsageHistory(tenant, environment string) (deploy.RuntimeUsageHistory, error) {
	path, err := RuntimeUsageHistoryPath(tenant, environment)
	if err != nil {
		return deploy.RuntimeUsageHistory{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return deploy.RuntimeUsageHistory{}, nil
	}
	var history deploy.RuntimeUsageHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return deploy.RuntimeUsageHistory{}, nil
	}
	return history, nil
}

// RuntimeUsageHistoryPath keeps the history beside the activity markers. It has
// to outlive the container that produced it, which is the whole reason it is a
// store and not a live read.
func RuntimeUsageHistoryPath(tenant, environment string) (string, error) {
	dir, err := activity.EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, runtimeUsageHistoryFileName), nil
}
