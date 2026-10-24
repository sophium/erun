package model

// GitPushAccessStatus reports fetch and push capability separately: a public repo fetches
// anonymously, so a missing push credential otherwise surfaces only at the final push.
type GitPushAccessStatus struct {
	// RemoteURL is empty when there is no origin; other fields are then meaningless.
	RemoteURL string
	FetchOK   bool
	// GHAuthenticated never runs gh login/refresh/switch, whose interactive flow cannot finish in a pod.
	GHAuthenticated bool
	// PushCredential: a gh session counts only with `repo` scope or no scope list (fine-grained
	// tokens list none); GH_TOKEN/GITHUB_TOKEN always count. For SSH, any exit other than 255
	// (ssh's own failure code) means the host accepted the key.
	PushCredential bool
}

// FetchWorksPushDoesNot reports a readable origin with no credential that could push.
func (s GitPushAccessStatus) FetchWorksPushDoesNot() bool {
	return s.RemoteURL != "" && s.FetchOK && !s.PushCredential
}
