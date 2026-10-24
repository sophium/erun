package model

// `--sync-config` rewrites the in-pod config from the chart-injected ERUN_* env
// (env wins); keys the env does not carry are preserved untouched.

// ConfigDriftKind classifies why an on-disk key disagrees with the injected env.
type ConfigDriftKind string

const (
	// ConfigDriftMissing: the injected env carries the key but the disk does not.
	ConfigDriftMissing ConfigDriftKind = "missing"
	// ConfigDriftWrong: both carry the key with different values.
	ConfigDriftWrong ConfigDriftKind = "wrong"
	// ConfigDriftLegacyKey: the on-disk config still carries a legacy key such as `remote:`.
	ConfigDriftLegacyKey ConfigDriftKind = "legacy"
)

// ConfigDriftField is one key that disagrees between on-disk config and injected env.
type ConfigDriftField struct {
	Scope    string // "env" or "root"
	Key      string
	OnDisk   string
	Injected string
	Kind     ConfigDriftKind
}
