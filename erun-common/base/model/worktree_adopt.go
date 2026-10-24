package model

// WorktreeAdoptParams paths are mounted outside /home/erun so the claim cannot
// shadow the tree being read.
type WorktreeAdoptParams struct {
	LegacyDir string
	ClaimDir  string
}
