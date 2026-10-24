package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// reviewAPIStubServer runs a minimal, stateful erun-backend-api double
// covering every route `erun review` drives, so real-run scenarios exercise
// platformclient's review request/response handling —
// including a real create -> list -> show -> comment -> close round trip —
// rather than only its --dry-run trace branch.
func reviewAPIStubServer(t testing.TB) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	stub := &reviewAPIStub{
		reviews:     map[string]map[string]any{},
		comments:    map[string][]map[string]any{},
		nextReview:  1,
		nextComment: 1,
		nextBuild:   1,
	}

	mux.HandleFunc("GET /v1/whoami", stub.whoami)
	mux.HandleFunc("POST /v1/reviews", stub.createReview)
	mux.HandleFunc("GET /v1/reviews", stub.listReviews)
	mux.HandleFunc("GET /v1/reviews/{review_id}", stub.showReview)
	mux.HandleFunc("PATCH /v1/reviews/{review_id}/status", stub.updateReviewStatus)
	mux.HandleFunc("GET /v1/reviews/merge-queue", stub.listMergeQueue)
	mux.HandleFunc("POST /v1/reviews/merge-queue/advance", stub.advanceMergeQueue)
	mux.HandleFunc("POST /v1/reviews/merge-queue/override-advance", stub.overrideAdvanceMergeQueue)
	mux.HandleFunc("GET /v1/reviews/{review_id}/comments", stub.listComments)
	mux.HandleFunc("PATCH /v1/reviews/{review_id}/comments/{comment_id}/status", stub.updateCommentStatus)
	mux.HandleFunc("POST /v1/reviews/{review_id}/comments", stub.createComment)
	mux.HandleFunc("GET /v1/reviews/{review_id}/builds", stub.listBuilds)
	mux.HandleFunc("POST /v1/reviews/{review_id}/builds", stub.recordBuild)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// reviewAPIStub is the state reviewAPIStubServer's handlers share.
type reviewAPIStub struct {
	mu          sync.Mutex
	reviews     map[string]map[string]any
	reviewOrder []string
	comments    map[string][]map[string]any
	nextReview  int
	nextComment int
	nextBuild   int
}

func (s *reviewAPIStub) whoami(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	writeReviewStubJSON(w, map[string]any{
		"tenantId": "tenant-1", "userId": "user-1", "username": "test-user", "issuer": "https://idp.example", "subject": "sub-1",
	})
}

func (s *reviewAPIStub) createReview(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.reviews {
		if existing["name"] == body["name"] && existing["repository"] == body["repository"] {
			http.Error(w, "conflict: a review named "+body["name"]+" already exists", http.StatusConflict)
			return
		}
	}
	id := "review-" + strconv.Itoa(s.nextReview)
	s.nextReview++
	review := map[string]any{
		"reviewId": id, "tenantId": "tenant-1", "authorUserId": "user-1",
		"repository": body["repository"],
		"name":       body["name"], "targetBranch": body["targetBranch"], "sourceBranch": body["sourceBranch"],
		"status": "OPEN", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
	}
	// A declared issue is echoed the way the real API answers it: the
	// reference the caller stated, and DECLARED as where it came from.
	// Nothing here derives one from the source branch -- that is the
	// real API's job, and a stub inventing it would hide a client that
	// sent no issueRef at all.
	if strings.TrimSpace(body["issueRef"]) != "" {
		review["issueRef"] = body["issueRef"]
		review["issueRefSource"] = "DECLARED"
	}
	s.reviews[id] = review
	s.reviewOrder = append(s.reviewOrder, id)
	w.WriteHeader(http.StatusCreated)
	writeReviewStubJSON(w, review)
}

func (s *reviewAPIStub) listReviews(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	query := r.URL.Query()
	out := []map[string]any{}
	for _, id := range s.reviewOrder {
		review := s.reviews[id]
		if reviewMatchesListQuery(review, query) {
			out = append(out, review)
		}
	}
	writeReviewStubJSON(w, out)
}

// reviewMatchesListQuery applies GET /v1/reviews's equality filters, each
// ignored when its query parameter is empty.
func reviewMatchesListQuery(review map[string]any, query url.Values) bool {
	for _, field := range []string{"repository", "targetBranch", "sourceBranch", "status", "authorUserId"} {
		if v := query.Get(field); v != "" && review[field] != v {
			return false
		}
	}
	return true
}

func (s *reviewAPIStub) showReview(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	review, ok := s.reviews[r.PathValue("review_id")]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeReviewStubJSON(w, review)
}

func (s *reviewAPIStub) updateReviewStatus(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	defer s.mu.Unlock()
	review, ok := s.reviews[r.PathValue("review_id")]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	review["status"] = body["status"]
	writeReviewStubJSON(w, review)
}

func (s *reviewAPIStub) listMergeQueue(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	targetBranch := r.URL.Query().Get("targetBranch")
	repository := r.URL.Query().Get("repository")
	out := []map[string]any{}
	for _, id := range s.reviewOrder {
		review := s.reviews[id]
		if review["targetBranch"] != targetBranch {
			continue
		}
		if repository != "" && review["repository"] != repository {
			continue
		}
		if review["status"] != "READY" && review["status"] != "MERGE" {
			continue
		}
		out = append(out, review)
	}
	writeReviewStubJSON(w, out)
}

// advanceMergeQueue mirrors the real backend's own promotion target (READY ->
// MERGE, never straight to MERGED) so a real-run scenario can drive a review
// to MERGE and then exercise `review requeue`'s MERGE -> READY recovery path.
func (s *reviewAPIStub) advanceMergeQueue(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	defer s.mu.Unlock()
	// One MERGE per target branch, the same invariant the real backend
	// enforces: a branch whose slot is taken refuses with the review
	// holding it rather than promoting the next one.
	if review := s.occupyingMerge(body["targetBranch"], body["repository"]); review != nil {
		writeMergeQueueOccupied(w, review)
		return
	}
	s.promoteNextReady(w, body["targetBranch"], body["repository"])
}

// occupyingMerge returns the review already at MERGE for the target branch,
// or nil when its slot is free.
func (s *reviewAPIStub) occupyingMerge(targetBranch, repository string) map[string]any {
	for _, id := range s.reviewOrder {
		review := s.reviews[id]
		if review["targetBranch"] != targetBranch || review["status"] != "MERGE" {
			continue
		}
		if repository != "" && review["repository"] != repository {
			continue
		}
		return review
	}
	return nil
}

func writeMergeQueueOccupied(w http.ResponseWriter, review map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	writeReviewStubJSON(w, map[string]any{
		"code": "MERGE_QUEUE_OCCUPIED",
		"message": fmt.Sprintf("merge queue for %v already has a review at MERGE: %v (%v, %v); complete it or requeue it back to READY before advancing",
			review["targetBranch"], review["reviewId"], review["name"], review["sourceBranch"]),
		"details": map[string]any{
			"targetBranch": review["targetBranch"],
			"reviewId":     review["reviewId"],
			"name":         review["name"],
			"sourceBranch": review["sourceBranch"],
		},
	})
}

// promoteNextReady moves the first READY review for the target branch to
// MERGE and answers with it, or refuses an empty queue.
func (s *reviewAPIStub) promoteNextReady(w http.ResponseWriter, targetBranch, repository string) {
	for _, id := range s.reviewOrder {
		review := s.reviews[id]
		if review["targetBranch"] != targetBranch {
			continue
		}
		if repository != "" && review["repository"] != repository {
			continue
		}
		if review["status"] == "READY" {
			review["status"] = "MERGE"
			writeReviewStubJSON(w, review)
			return
		}
	}
	http.Error(w, "empty queue", http.StatusConflict)
}

// overrideAdvanceMergeQueue is the CLI plumbing's own concern here (the real
// unresolved-thread gate and its bypass are covered by erun-backend-api's
// service tests); this stub only proves the wire round trip and that a blank
// reason is refused.
func (s *reviewAPIStub) overrideAdvanceMergeQueue(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body["reason"]) == "" {
		http.Error(w, "invalid input", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promoteNextReady(w, body["targetBranch"], body["repository"])
}

func (s *reviewAPIStub) listComments(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writeReviewStubJSON(w, s.comments[r.PathValue("review_id")])
}

func (s *reviewAPIStub) updateCommentStatus(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, comment := range s.comments[r.PathValue("review_id")] {
		if comment["commentId"] != r.PathValue("comment_id") {
			continue
		}
		if parent, ok := comment["parentCommentId"].(string); ok && parent != "" {
			http.Error(w, "only the root comment of a thread can have its status updated", http.StatusBadRequest)
			return
		}
		comment["status"] = body["status"]
		writeReviewStubJSON(w, comment)
		return
	}
	http.Error(w, "not found", http.StatusNotFound)
}

func (s *reviewAPIStub) createComment(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	reviewID := r.PathValue("review_id")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.reviews[reviewID]; !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	id := "comment-" + strconv.Itoa(s.nextComment)
	s.nextComment++
	line, _ := body["line"].(float64)
	comment := map[string]any{
		"commentId": id, "tenantId": "tenant-1", "reviewId": reviewID, "creatorUserId": "user-1",
		"status": "OPEN", "commitId": body["commitId"], "filePath": body["filePath"], "line": int(line),
		"body": body["body"], "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
	}
	if parent, ok := body["parentCommentId"].(string); ok && parent != "" {
		comment["parentCommentId"] = parent
	}
	s.comments[reviewID] = append(s.comments[reviewID], comment)
	w.WriteHeader(http.StatusCreated)
	writeReviewStubJSON(w, comment)
}

func (s *reviewAPIStub) listBuilds(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	writeReviewStubJSON(w, []map[string]any{})
}

// recordBuild mirrors the real backend's auto-transition: recording a build
// moves the review straight to READY or FAILED, with no separate PATCH
// /status call — see erun-docs/docs/collaboration/builds.md.
func (s *reviewAPIStub) recordBuild(w http.ResponseWriter, r *http.Request) {
	if !requireBearer(w, r) {
		return
	}
	reviewID := r.PathValue("review_id")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	defer s.mu.Unlock()
	review, ok := s.reviews[reviewID]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	id := "build-" + strconv.Itoa(s.nextBuild)
	s.nextBuild++
	successful, _ := body["successful"].(bool)
	build := map[string]any{
		"buildId": id, "tenantId": "tenant-1", "reviewId": reviewID,
		"successful": successful, "commitId": body["commitId"], "version": body["version"],
		"createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z",
	}
	if detail, ok := body["failureDetail"].(string); ok && detail != "" {
		build["failureDetail"] = detail
	}
	if successful {
		review["status"] = "READY"
	} else {
		review["status"] = "FAILED"
	}
	w.WriteHeader(http.StatusCreated)
	writeReviewStubJSON(w, build)
}

// writeReviewStubJSON writes v as a handler's JSON body. A failed write has
// no one left to report to: the client reading the body sees it instead.
func writeReviewStubJSON(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return
	}
}
