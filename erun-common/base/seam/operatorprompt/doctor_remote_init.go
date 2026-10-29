package operatorprompt

// RemoteInitFinishPrompt asks for one value; label distinguishes the repository
// URL from the CodeCommit SSH key ID.
type RemoteInitFinishPrompt func(label string) (string, error)
