package lifecycle

import (
	"path"
	"strings"
)

type diffTreeBuildNode struct {
	Name      string
	Path      string
	Type      string
	Status    string
	Additions int
	Deletions int
	Children  []diffTreeBuildNode
}

// reorderFilesByTree aligns Files to the tree's file-leaf order so the
// desktop's two panels agree: its changed-files list is driven by Tree and its
// diff panel by Files, which otherwise diverge for untracked files (parsing
// appends them last, out of tree order).
func reorderFilesByTree(files []DiffFile, tree []DiffTreeNode) []DiffFile {
	if len(files) <= 1 {
		return files
	}
	indexesByPath := make(map[string][]int, len(files))
	for index, file := range files {
		indexesByPath[file.Path] = append(indexesByPath[file.Path], index)
	}
	ordered := make([]DiffFile, 0, len(files))
	used := make([]bool, len(files))
	for _, node := range tree {
		if node.Type != "file" {
			continue
		}
		for _, index := range indexesByPath[node.Path] {
			if used[index] {
				continue
			}
			ordered = append(ordered, files[index])
			used[index] = true
			break
		}
	}
	for index, file := range files {
		if !used[index] {
			ordered = append(ordered, file)
		}
	}
	return ordered
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
