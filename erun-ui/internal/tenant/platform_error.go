package tenant

import (
	"errors"
	"fmt"
	"log"

	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// Platform refusals are mapped to actionable sentences; the wire form is
// logged rather than shown. Unrecognised errors keep their text.

// platformAction completes "You do not have permission to ...".
type platformAction string

const (
	actionCreateReview         platformAction = "open a review"
	actionCloseReview          platformAction = "close this review"
	actionAdvanceQueue         platformAction = "advance the merge queue"
	actionOverrideAdvanceQueue platformAction = "override the merge queue's unresolved-thread gate"
	actionCommentReview        platformAction = "comment on this review"
	actionResolveComment       platformAction = "resolve this comment thread"
	actionUnresolveComment     platformAction = "reopen this comment thread"
	actionAddReviewer          platformAction = "assign this reviewer"
	actionRemoveReviewer       platformAction = "remove this reviewer"
)

// operatorPlatformError returns err unchanged when it cannot classify it.
func operatorPlatformError(action platformAction, err error) error {
	if err == nil {
		return nil
	}

	var sentence string
	switch {
	case errors.Is(err, platformclient.ErrPlatformForbidden):
		sentence = fmt.Sprintf("You do not have permission to %s.", action)
	case errors.Is(err, platformclient.ErrPlatformUnauthorized):
		sentence = "Your sign-in is no longer valid for this tenant. Sign in again and retry."
	case errors.Is(err, platformclient.ErrPlatformNotFound):
		if action == actionAddReviewer || action == actionRemoveReviewer {
			sentence = fmt.Sprintf("erun could not %s: the review or reviewer no longer exists. Refresh and try again.", action)
		} else {
			sentence = "That review no longer exists. Refresh to see the current list."
		}
	case errors.Is(err, platformclient.ErrPlatformConflict):
		// A conflict is usually transient and someone else's write.
		sentence = fmt.Sprintf("Someone else changed this while you were working, so erun did not %s. Refresh and try again.", action)
	case errors.Is(err, platformclient.ErrPlatformNotImplemented):
		sentence = fmt.Sprintf("This control plane cannot %s.", action)
	default:
		return err
	}

	log.Printf("erun-app: %s refused: %v", action, err)
	return errors.New(sentence)
}

// enrollERunPlatformUserError classifies enrollment refusals. Unlike other
// writes, a 401 here means the identity is not enrolled, so "sign in again" is
// wrong advice.
func enrollERunPlatformUserError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, platformclient.ErrPlatformForbidden):
		log.Printf("erun-app: enroll self refused: %v", err)
		return enrollmentHandoffError{}
	case errors.Is(err, platformclient.ErrPlatformUnauthorized):
		switch platformclient.PlatformAuthErrorCode(err) {
		case "TENANT_UNRESOLVED", "RESOLUTION_FAILED":
			return err
		}
		log.Printf("erun-app: enroll self refused: %v", err)
		return enrollmentHandoffError{}
	default:
		return err
	}
}

// enrollmentHandoffError is shown as an operator-facing sentence, not wrapped.
type enrollmentHandoffError struct{}

func (enrollmentHandoffError) Error() string {
	return "You do not have permission to enroll yourself in this tenant. Ask an administrator to run the command below, or request an invitation."
}
