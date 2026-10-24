package model

// Review statuses: OPEN while being worked on, then exactly one of the others.
// erun-backend-api's reviews table constrains status to this set.
const (
	ReviewStatusOpen   = "OPEN"
	ReviewStatusClosed = "CLOSED"
	ReviewStatusFailed = "FAILED"
	ReviewStatusReady  = "READY"
	ReviewStatusMerge  = "MERGE"
	ReviewStatusMerged = "MERGED"
)

// Shared by the `erun review` CLI and MCP tools: every HTTP call is traced
// first so --dry-run and MCP previews never touch the network.

// ReviewListParams is the `erun review list` input. Repository is not
// defaulted from the checkout, so listing discovers work across repositories.
type ReviewListParams struct {
	Repository     string
	TargetBranch   string
	SourceBranch   string
	Status         string
	AuthorUserID   string
	ReviewerUserID string
	Mine           bool
	WaitingOnMe    bool
}

// ReviewDetail is the `erun review show` result. UnresolvedThreads counts OPEN
// root comments.
type ReviewDetail struct {
	Review            PlatformReview    `json:"review"`
	Comments          []PlatformComment `json:"comments"`
	Builds            []PlatformBuild   `json:"builds"`
	UnresolvedThreads int               `json:"unresolvedThreads"`
}

// ReviewCommentParams is the `erun review comment` input; ParentCommentID makes
// it a reply.
type ReviewCommentParams struct {
	ReviewID        string
	CommitID        string
	FilePath        string
	Line            int
	Body            string
	ParentCommentID string
}

// ReviewRecordBuildParams is the `erun review record-build` input.
type ReviewRecordBuildParams struct {
	ReviewID      string
	CommitID      string
	Version       string
	Successful    bool
	FailureDetail string
}
