package model

import (
	"time"
)

// HostCredentialsRefresh reports the profile written and when it lapses.
type HostCredentialsRefresh struct {
	Alias      string    `json:"alias"`
	Profile    string    `json:"profile"`
	Path       string    `json:"path"`
	Region     string    `json:"region,omitempty"`
	Expiration time.Time `json:"expiration,omitzero"`
}

// HostCredentialsStatus is the pod's erun-host profile; an empty Region means none resolved.
type HostCredentialsStatus struct {
	Alias      string    `json:"alias"`
	Profile    string    `json:"profile"`
	Present    bool      `json:"present"`
	Expiration time.Time `json:"expiration,omitzero"`
	Expired    bool      `json:"expired"`
	Region     string    `json:"region,omitempty"`
}
