package diff

import (
	"path"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

func ParseGitDiff(raw string) model.DiffResult {
	result := model.DiffResult{
		RawDiff: raw,
		Files:   parseGitDiffFiles(raw),
	}
	for _, file := range result.Files {
		result.Summary.FileCount++
		result.Summary.Additions += file.Additions
		result.Summary.Deletions += file.Deletions
	}
	result.Tree = BuildDiffTree(result.Files)
	result.Files = reorderFilesByTree(result.Files, result.Tree)
	return result
}

func parseGitDiffFiles(raw string) []model.DiffFile {
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
	files       []model.DiffFile
	current     *model.DiffFile
	currentHunk *model.DiffHunk
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
	p.current = &model.DiffFile{Path: firstNonEmptyDiffPath(newPath, oldPath), OldPath: oldPath, NewPath: newPath, Status: "modified"}
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
		p.currentHunk.Lines = append(p.currentHunk.Lines, model.DiffLine{Kind: "meta", Content: line})
	default:
		p.appendContextLine(line)
	}
}

func (p *diffFileParser) appendAddedLine(line string) {
	p.current.Additions++
	p.currentHunk.Lines = append(p.currentHunk.Lines, model.DiffLine{Kind: "add", Content: strings.TrimPrefix(line, "+"), NewLine: p.newLine})
	p.newLine++
}

func (p *diffFileParser) appendDeletedLine(line string) {
	p.current.Deletions++
	p.currentHunk.Lines = append(p.currentHunk.Lines, model.DiffLine{Kind: "delete", Content: strings.TrimPrefix(line, "-"), OldLine: p.oldLine})
	p.oldLine++
}

func (p *diffFileParser) appendContextLine(line string) {
	p.currentHunk.Lines = append(p.currentHunk.Lines, model.DiffLine{Kind: "context", Content: strings.TrimPrefix(line, " "), OldLine: p.oldLine, NewLine: p.newLine})
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

func parseDiffHunkHeader(line string) model.DiffHunk {
	hunk := model.DiffHunk{Header: line}
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

func normalizeDiffFile(file *model.DiffFile) {
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
