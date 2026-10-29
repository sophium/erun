package model

type DiffResult struct {
	WorkingDirectory string         `json:"workingDirectory,omitempty"`
	RawDiff          string         `json:"rawDiff"`
	Summary          DiffSummary    `json:"summary"`
	Files            []DiffFile     `json:"files,omitempty"`
	Tree             []DiffTreeNode `json:"tree,omitempty"`
	ReviewBase       DiffReviewBase `json:"reviewBase,omitempty"`
	ReviewCommits    []DiffCommit   `json:"reviewCommits,omitempty"`
	Scope            string         `json:"scope,omitempty"`
	SelectedCommit   string         `json:"selectedCommit,omitempty"`
	IncludesWorktree bool           `json:"includesWorktree,omitempty"`
}

type DiffSummary struct {
	FileCount int `json:"fileCount"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type DiffFile struct {
	Path      string     `json:"path"`
	OldPath   string     `json:"oldPath,omitempty"`
	NewPath   string     `json:"newPath,omitempty"`
	Status    string     `json:"status"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	Binary    bool       `json:"binary,omitempty"`
	Hunks     []DiffHunk `json:"hunks,omitempty"`
}

type DiffHunk struct {
	Header   string     `json:"header"`
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []DiffLine `json:"lines,omitempty"`
}

type DiffLine struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
	OldLine int    `json:"oldLine,omitempty"`
	NewLine int    `json:"newLine,omitempty"`
}

type DiffTreeNode struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	ParentPath string `json:"parentPath,omitempty"`
	Type       string `json:"type"`
	Depth      int    `json:"depth"`
	Status     string `json:"status,omitempty"`
	Additions  int    `json:"additions,omitempty"`
	Deletions  int    `json:"deletions,omitempty"`
}

type DiffReviewBase struct {
	Branch      string `json:"branch,omitempty"`
	Commit      string `json:"commit,omitempty"`
	ShortCommit string `json:"shortCommit,omitempty"`
}

type DiffCommit struct {
	Hash      string `json:"hash"`
	ShortHash string `json:"shortHash"`
	Subject   string `json:"subject"`
	Author    string `json:"author"`
	Date      string `json:"date"`
}

type DiffOptions struct {
	Scope          string `json:"scope,omitempty"`
	SelectedCommit string `json:"selectedCommit,omitempty"`
	// Target is "env" (default, the runtime working directory) or "erun" (the contribute-mode clone).
	Target string `json:"target,omitempty"`
}
