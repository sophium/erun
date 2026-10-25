package records

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	activitypkg "github.com/sophium/erun/erun-common/infra/activity"
)

var EnvironmentActivityKinds = []string{configpkg.ActivityKindSSH, configpkg.ActivityKindAPI, configpkg.ActivityKindMCP, configpkg.ActivityKindCLI, configpkg.ActivityKindCodex, ActivityKindProcess}

const environmentActivityClientCap = 8

func RecordEnvironmentActivity(params model.EnvironmentActivityParams) error {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	kind := strings.TrimSpace(params.Kind)
	if err := core.ErrMissingTenantOrEnvironment("record environment activity", tenant, environment); err != nil {
		return err
	}
	if !validEnvironmentActivityKind(kind) {
		return fmt.Errorf("unsupported activity kind %q", kind)
	}
	now := params.Now
	if now.IsZero() {
		now = time.Now()
	}
	dir, err := activitypkg.EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	path := filepath.Join(dir, kind+".json")
	snapshot, _ := loadEnvironmentActivitySnapshot(path)
	if params.Seen {
		snapshot.LastSeen = now
	} else {
		snapshot.LastActivity = now
		snapshot.LastSeen = now
	}
	if params.Bytes > 0 {
		snapshot.Bytes += params.Bytes
	}
	mergeEnvironmentActivityClients(&snapshot, params.ClientUpdates, now)

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	// Replace atomically: every idle read parses every snapshot.
	return configpkg.ReplaceFileAtomically(path, data, 0o644)
}

func mergeEnvironmentActivityClients(snapshot *model.EnvironmentActivitySnapshot, updates []model.EnvironmentActivityClientUpdate, now time.Time) {
	if len(updates) == 0 {
		return
	}
	if snapshot.Clients == nil {
		snapshot.Clients = make(map[string]model.EnvironmentActivityClient, len(updates))
	}
	for _, update := range updates {
		address := strings.TrimSpace(update.Address)
		if address == "" {
			continue
		}
		client := snapshot.Clients[address]
		if update.Bytes > 0 {
			client.Bytes += update.Bytes
			client.LastActivity = now
		}
		snapshot.Clients[address] = client
	}
	evictOldestEnvironmentActivityClients(snapshot.Clients, environmentActivityClientCap)
}

func evictOldestEnvironmentActivityClients(clients map[string]model.EnvironmentActivityClient, limit int) {
	if limit <= 0 || len(clients) <= limit {
		return
	}
	for len(clients) > limit {
		oldestKey := ""
		var oldestActivity time.Time
		first := true
		for key, client := range clients {
			if first || client.LastActivity.Before(oldestActivity) {
				oldestKey = key
				oldestActivity = client.LastActivity
				first = false
			}
		}
		if oldestKey == "" {
			return
		}
		delete(clients, oldestKey)
	}
}

func LoadEnvironmentActivity(tenant, environment string) (map[string]model.EnvironmentActivitySnapshot, error) {
	dir, err := activitypkg.EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return nil, err
	}
	result := map[string]model.EnvironmentActivitySnapshot{}
	for _, kind := range EnvironmentActivityKinds {
		snapshot, err := loadEnvironmentActivitySnapshot(filepath.Join(dir, kind+".json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result[kind] = snapshot
	}
	return result, nil
}

func loadEnvironmentActivitySnapshot(path string) (model.EnvironmentActivitySnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.EnvironmentActivitySnapshot{}, err
	}
	var snapshot model.EnvironmentActivitySnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return model.EnvironmentActivitySnapshot{}, err
	}
	return snapshot, nil
}

func validEnvironmentActivityKind(kind string) bool {
	for _, candidate := range EnvironmentActivityKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}
