package kubecontext

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

type emptyCloudConfig struct{}

func (emptyCloudConfig) LoadERunConfig() (config.ERunConfig, string, error) {
	return config.ERunConfig{}, "", nil
}

func (emptyCloudConfig) SaveERunConfig(config.ERunConfig) error { return nil }

func TestContextListJSONAlwaysCarriesTheCollection(t *testing.T) {
	var buf bytes.Buffer
	ctx := core.Context{Output: model.OutputJSON, Stdout: &buf, Stderr: &buf}
	if err := runContextListCommand(ctx, emptyCloudConfig{}, cloud.CloudContextDependencies{}); err != nil {
		t.Fatalf("run context list: %v", err)
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &document); err != nil {
		t.Fatalf("context list is not a JSON object: %v (%s)", err, buf.String())
	}
	raw, present := document["cloudContexts"]
	if !present {
		t.Fatalf("context list dropped the collection field entirely: %s", buf.String())
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("cloudContexts is not an array: %v (%s)", err, raw)
	}
	if rows == nil {
		t.Fatalf("cloudContexts is null rather than []: %s", buf.String())
	}
}
