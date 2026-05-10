package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

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
}

type diffTreeBuildNode struct {
	Name      string
	Path      string
	Type      string
	Status    string
	Additions int
	Deletions int
	Children  []diffTreeBuildNode
}

func ResolveGitDiff(projectRoot string, runGit GitCommandRunnerFunc) (DiffResult, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return DiffResult{}, fmt.Errorf("project root is required")
	}
	if runGit == nil {
		runGit = GitCommandRunner
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	if err := runGit(projectRoot, stdout, stderr, "diff", "--no-color", "--no-ext-diff"); err != nil {
		return DiffResult{}, fmt.Errorf("git diff: %w%s", err, formatGitCommandStderr(stderr.String()))
	}
	if err := appendUntrackedGitDiff(projectRoot, stdout, runGit); err != nil {
		return DiffResult{}, err
	}

	result := ParseGitDiff(stdout.String())
	result.WorkingDirectory = projectRoot
	result.IncludesWorktree = true
	return result, nil
}

func ResolveGitDiffWithOptions(projectRoot string, options DiffOptions, runGit GitCommandRunnerFunc) (DiffResult, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return DiffResult{}, fmt.Errorf("project root is required")
	}
	if runGit == nil {
		runGit = GitCommandRunner
	}

	base, baseFound, err := resolveGitDiffReviewBase(projectRoot, runGit)
	if err != nil {
		return DiffResult{}, err
	}
	commits, err := resolveGitDiffReviewCommits(projectRoot, base, baseFound, runGit)
	if err != nil {
		return DiffResult{}, err
	}

	scope := normalizeDiffScope(options.Scope)
	selectedCommit := strings.TrimSpace(options.SelectedCommit)
	stdout := new(bytes.Buffer)
	diffArgs := gitDiffReviewArgs(base.Commit, baseFound, scope, selectedCommit)
	stderr := new(bytes.Buffer)
	if err := runGit(projectRoot, stdout, stderr, diffArgs...); err != nil {
		return DiffResult{}, fmt.Errorf("git diff: %w%s", err, formatGitCommandStderr(stderr.String()))
	}
	if err := appendUntrackedGitDiff(projectRoot, stdout, runGit); err != nil {
		return DiffResult{}, err
	}

	result := ParseGitDiff(stdout.String())
	result.WorkingDirectory = projectRoot
	result.ReviewBase = base
	result.ReviewCommits = commits
	result.Scope = scope
	result.SelectedCommit = selectedCommit
	result.IncludesWorktree = true
	return result, nil
}

func normalizeDiffScope(scope string) string {
	switch strings.TrimSpace(scope) {
	case "all", "commit":
		return strings.TrimSpace(scope)
	default:
		return "current"
	}
}

func gitDiffReviewArgs(baseCommit string, baseFound bool, scope, selectedCommit string) []string {
	args := []string{"diff", "--no-color", "--no-ext-diff"}
	switch scope {
	case "all":
		if baseFound {
			args = append(args, baseCommit)
		}
		return args
	case "commit":
		if selectedCommit != "" {
			args = append(args, selectedCommit+"^")
		}
		return args
	default:
		return args
	}
}

func resolveGitDiffReviewBase(projectRoot string, runGit GitCommandRunnerFunc) (DiffReviewBase, bool, error) {
	var selected DiffReviewBase
	selectedDistance := -1
	for _, branch := range []string{"origin/HEAD", "origin/main", "origin/develop", "main", "develop"} {
		commit, err := gitOutput(projectRoot, runGit, "merge-base", "HEAD", branch)
		if err != nil || commit == "" {
			continue
		}
		distance, err := gitOutput(projectRoot, runGit, "rev-list", "--count", commit+"..HEAD")
		if err != nil {
			continue
		}
		parsedDistance, err := strconv.Atoi(strings.TrimSpace(distance))
		if err != nil {
			continue
		}
		shortCommit, _ := gitOutput(projectRoot, runGit, "rev-parse", "--short", commit)
		displayBranch := resolveGitDiffReviewBaseBranch(projectRoot, branch, runGit)
		if selectedDistance < 0 || parsedDistance < selectedDistance {
			selectedDistance = parsedDistance
			selected = DiffReviewBase{Branch: displayBranch, Commit: commit, ShortCommit: shortCommit}
		}
	}
	if selected.Commit != "" {
		return selected, true, nil
	}
	return DiffReviewBase{}, false, nil
}

func resolveGitDiffReviewBaseBranch(projectRoot, branch string, runGit GitCommandRunnerFunc) string {
	if branch != "origin/HEAD" {
		return branch
	}
	resolved, err := gitOutput(projectRoot, runGit, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err != nil || resolved == "" {
		return branch
	}
	return resolved
}

func resolveGitDiffReviewCommits(projectRoot string, base DiffReviewBase, baseFound bool, runGit GitCommandRunnerFunc) ([]DiffCommit, error) {
	if !baseFound {
		return nil, nil
	}
	output, err := gitOutput(projectRoot, runGit, "log", "--reverse", "--date=iso-strict", "--pretty=format:%H%x1f%h%x1f%an%x1f%aI%x1f%s%x1e", base.Commit+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return parseGitDiffReviewCommits(output), nil
}

func gitOutput(projectRoot string, runGit GitCommandRunnerFunc, args ...string) (string, error) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	if err := runGit(projectRoot, stdout, stderr, args...); err != nil {
		return "", fmt.Errorf("%w%s", err, formatGitCommandStderr(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func parseGitDiffReviewCommits(output string) []DiffCommit {
	output = strings.TrimSuffix(output, "\x1e")
	if output == "" {
		return nil
	}
	records := strings.Split(output, "\x1e")
	commits := make([]DiffCommit, 0, len(records))
	for _, record := range records {
		fields := strings.SplitN(strings.TrimSpace(record), "\x1f", 5)
		if len(fields) != 5 {
			continue
		}
		commits = append(commits, DiffCommit{
			Hash:      fields[0],
			ShortHash: fields[1],
			Author:    fields[2],
			Date:      fields[3],
			Subject:   fields[4],
		})
	}
	return commits
}

func appendUntrackedGitDiff(projectRoot string, rawDiff *bytes.Buffer, runGit GitCommandRunnerFunc) error {
	untrackedOutput := new(bytes.Buffer)
	untrackedStderr := new(bytes.Buffer)
	if err := runGit(projectRoot, untrackedOutput, untrackedStderr, "ls-files", "--others", "--exclude-standard", "-z"); err != nil {
		return fmt.Errorf("git ls-files: %w%s", err, formatGitCommandStderr(untrackedStderr.String()))
	}
	for _, file := range splitNULTerminated(untrackedOutput.String()) {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		fileDiff := new(bytes.Buffer)
		fileStderr := new(bytes.Buffer)
		err := runGit(projectRoot, fileDiff, fileStderr, "diff", "--no-color", "--no-ext-diff", "--no-index", "--", "/dev/null", file)
		if err != nil && fileDiff.Len() == 0 {
			return fmt.Errorf("git diff untracked %s: %w%s", file, err, formatGitCommandStderr(fileStderr.String()))
		}
		appendRawDiff(rawDiff, fileDiff.String())
	}
	return nil
}

func splitNULTerminated(value string) []string {
	value = strings.TrimSuffix(value, "\x00")
	if value == "" {
		return nil
	}
	return strings.Split(value, "\x00")
}

func appendRawDiff(rawDiff *bytes.Buffer, diff string) {
	if diff == "" {
		return
	}
	if rawDiff.Len() > 0 && !strings.HasSuffix(rawDiff.String(), "\n") {
		rawDiff.WriteString("\n")
	}
	rawDiff.WriteString(diff)
	if !strings.HasSuffix(diff, "\n") {
		rawDiff.WriteString("\n")
	}
}

func ParseGitDiff(raw string) DiffResult {
	result := DiffResult{
		RawDiff: raw,
		Files:   parseGitDiffFiles(raw),
	}
	for _, file := range result.Files {
		result.Summary.FileCount++
		result.Summary.Additions += file.Additions
		result.Summary.Deletions += file.Deletions
	}
	result.Tree = BuildDiffTree(result.Files)
	return result
}

func parseGitDiffFiles(raw string) []DiffFile {
	lines := strings.Split(raw, "\n")
	parser := diffFileParser{}

	for index, line := range lines {
		if index == len(lines)-1 && line == "" {
			continue
		}
		parser.parseLine(line)
	}
	parser.flush()
	return parser.files
}

type diffFileParser struct {
	files       []DiffFile
	current     *DiffFile
	currentHunk *DiffHunk
	oldLine     int
	newLine     int
}

func (p *diffFileParser) parseLine(line string) {
	if strings.HasPrefix(line, "diff --git ") {
		p.startFile(line)
		return
	}
	if p.current == nil {
		return
	}
	if p.parseFileMetadata(line) {
		return
	}
	if strings.HasPrefix(line, "@@ ") {
		p.startHunk(line)
		return
	}
	if p.currentHunk != nil {
		p.appendHunkLine(line)
	}
}

func (p *diffFileParser) startFile(line string) {
	p.flush()
	oldPath, newPath := parseDiffGitHeader(line)
	p.current = &DiffFile{Path: firstNonEmptyDiffPath(newPath, oldPath), OldPath: oldPath, NewPath: newPath, Status: "modified"}
}

func (p *diffFileParser) flush() {
	if p.current == nil {
		return
	}
	normalizeDiffFile(p.current)
	p.files = append(p.files, *p.current)
	p.current = nil
	p.currentHunk = nil
}

func (p *diffFileParser) parseFileMetadata(line string) bool {
	switch {
	case strings.HasPrefix(line, "new file mode "):
		p.current.Status = "added"
	case strings.HasPrefix(line, "deleted file mode "):
		p.current.Status = "deleted"
	case strings.HasPrefix(line, "rename from "):
		p.current.Status = "renamed"
		p.current.OldPath = strings.TrimSpace(strings.TrimPrefix(line, "rename from "))
	case strings.HasPrefix(line, "rename to "):
		p.current.Status = "renamed"
		p.current.NewPath = strings.TrimSpace(strings.TrimPrefix(line, "rename to "))
		p.current.Path = p.current.NewPath
	case strings.HasPrefix(line, "copy from "):
		p.current.Status = "copied"
		p.current.OldPath = strings.TrimSpace(strings.TrimPrefix(line, "copy from "))
	case strings.HasPrefix(line, "copy to "):
		p.current.Status = "copied"
		p.current.NewPath = strings.TrimSpace(strings.TrimPrefix(line, "copy to "))
		p.current.Path = p.current.NewPath
	case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
		p.current.Binary = true
		p.currentHunk = nil
	default:
		return false
	}
	return true
}

func (p *diffFileParser) startHunk(line string) {
	hunk := parseDiffHunkHeader(line)
	p.current.Hunks = append(p.current.Hunks, hunk)
	p.currentHunk = &p.current.Hunks[len(p.current.Hunks)-1]
	p.oldLine = p.currentHunk.OldStart
	p.newLine = p.currentHunk.NewStart
}

func (p *diffFileParser) appendHunkLine(line string) {
	switch {
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++ "):
		p.appendAddedLine(line)
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "--- "):
		p.appendDeletedLine(line)
	case strings.HasPrefix(line, "\\"):
		p.currentHunk.Lines = append(p.currentHunk.Lines, DiffLine{Kind: "meta", Content: line})
	default:
		p.appendContextLine(line)
	}
}

func (p *diffFileParser) appendAddedLine(line string) {
	p.current.Additions++
	p.currentHunk.Lines = append(p.currentHunk.Lines, DiffLine{Kind: "add", Content: strings.TrimPrefix(line, "+"), NewLine: p.newLine})
	p.newLine++
}

func (p *diffFileParser) appendDeletedLine(line string) {
	p.current.Deletions++
	p.currentHunk.Lines = append(p.currentHunk.Lines, DiffLine{Kind: "delete", Content: strings.TrimPrefix(line, "-"), OldLine: p.oldLine})
	p.oldLine++
}

func (p *diffFileParser) appendContextLine(line string) {
	p.currentHunk.Lines = append(p.currentHunk.Lines, DiffLine{Kind: "context", Content: strings.TrimPrefix(line, " "), OldLine: p.oldLine, NewLine: p.newLine})
	p.oldLine++
	p.newLine++
}

func parseDiffGitHeader(line string) (string, string) {
	value := strings.TrimSpace(strings.TrimPrefix(line, "diff --git "))
	parts := strings.SplitN(value, " b/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	oldPath := strings.TrimPrefix(strings.TrimSpace(parts[0]), "a/")
	newPath := strings.TrimSpace(parts[1])
	return oldPath, newPath
}

func parseDiffHunkHeader(line string) DiffHunk {
	hunk := DiffHunk{Header: line}
	end := strings.Index(line[3:], " @@")
	if end < 0 {
		return hunk
	}
	ranges := strings.Fields(line[3 : 3+end])
	if len(ranges) >= 2 {
		hunk.OldStart, hunk.OldLines = parseDiffRange(ranges[0], "-")
		hunk.NewStart, hunk.NewLines = parseDiffRange(ranges[1], "+")
	}
	return hunk
}

func parseDiffRange(value, prefix string) (int, int) {
	value = strings.TrimPrefix(strings.TrimSpace(value), prefix)
	parts := strings.SplitN(value, ",", 2)
	start, _ := strconv.Atoi(parts[0])
	lineCount := 1
	if len(parts) == 2 {
		lineCount, _ = strconv.Atoi(parts[1])
	}
	return start, lineCount
}

func normalizeDiffFile(file *DiffFile) {
	if file == nil {
		return
	}
	file.OldPath = strings.TrimPrefix(strings.TrimSpace(file.OldPath), "a/")
	file.NewPath = strings.TrimPrefix(strings.TrimSpace(file.NewPath), "b/")
	switch file.Status {
	case "added":
		file.Path = firstNonEmptyDiffPath(file.NewPath, file.Path)
	case "deleted":
		file.Path = firstNonEmptyDiffPath(file.OldPath, file.Path)
	default:
		file.Path = firstNonEmptyDiffPath(file.NewPath, file.OldPath, file.Path)
	}
	file.Path = cleanDiffPath(file.Path)
	file.OldPath = cleanDiffPath(file.OldPath)
	file.NewPath = cleanDiffPath(file.NewPath)
}

func firstNonEmptyDiffPath(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func cleanDiffPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	value = strings.TrimPrefix(value, "a/")
	value = strings.TrimPrefix(value, "b/")
	cleaned := path.Clean(value)
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func BuildDiffTree(files []DiffFile) []DiffTreeNode {
	root := make([]diffTreeBuildNode, 0)
	for _, file := range files {
		parts := strings.Split(cleanDiffPath(file.Path), "/")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		root = appendDiffTreeNode(root, parts, file, "")
	}
	flat := make([]DiffTreeNode, 0, len(files))
	for _, node := range root {
		flat = appendFlattenedDiffTreeNode(flat, node, "", 0)
	}
	return flat
}

func appendDiffTreeNode(nodes []diffTreeBuildNode, parts []string, file DiffFile, parent string) []diffTreeBuildNode {
	name := parts[0]
	nodePath := name
	if parent != "" {
		nodePath = parent + "/" + name
	}
	for i := range nodes {
		if nodes[i].Name != name {
			continue
		}
		if len(parts) == 1 {
			nodes[i] = diffFileTreeNode(file, nodePath)
			return nodes
		}
		nodes[i].Children = appendDiffTreeNode(nodes[i].Children, parts[1:], file, nodePath)
		nodes[i].Additions += file.Additions
		nodes[i].Deletions += file.Deletions
		return nodes
	}

	if len(parts) == 1 {
		return append(nodes, diffFileTreeNode(file, nodePath))
	}
	node := diffTreeBuildNode{
		Name:      name,
		Path:      nodePath,
		Type:      "directory",
		Additions: file.Additions,
		Deletions: file.Deletions,
		Children:  appendDiffTreeNode(nil, parts[1:], file, nodePath),
	}
	return append(nodes, node)
}

func diffFileTreeNode(file DiffFile, nodePath string) diffTreeBuildNode {
	return diffTreeBuildNode{
		Name:      path.Base(nodePath),
		Path:      file.Path,
		Type:      "file",
		Status:    file.Status,
		Additions: file.Additions,
		Deletions: file.Deletions,
	}
}

func appendFlattenedDiffTreeNode(flat []DiffTreeNode, node diffTreeBuildNode, parent string, depth int) []DiffTreeNode {
	flat = append(flat, DiffTreeNode{
		Name:       node.Name,
		Path:       node.Path,
		ParentPath: parent,
		Type:       node.Type,
		Depth:      depth,
		Status:     node.Status,
		Additions:  node.Additions,
		Deletions:  node.Deletions,
	})
	for _, child := range node.Children {
		flat = appendFlattenedDiffTreeNode(flat, child, node.Path, depth+1)
	}
	return flat
}

func formatGitCommandStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return "\n" + stderr
}

func WriteRawDiff(stdout io.Writer, result DiffResult) error {
	if result.RawDiff == "" {
		return nil
	}
	_, err := io.WriteString(stdout, result.RawDiff)
	return err
}
