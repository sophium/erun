package main

import (
	"errors"
	"fmt"
	"log"

	eruncommon "github.com/sophium/erun/erun-common"
)

// A platform refusal reaches the operator beside the control they just used,
// so it has to read as a reason they can act on rather than as the exchange
// that produced it. The client already distinguishes the refusals that mean
// something distinct, so the mapping is on those; anything else keeps its
// original text, which is better than a sentence that guesses wrong.
//
// The wire form is not discarded, only moved: it is logged, where a diagnosis
// needs it, instead of shown, where it only tells the operator that software
// was involved.

// platformAction names the attempt in the sentence the operator reads. It is
// a verb phrase completing "You do not have permission to ...".
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

// operatorPlatformError turns a platform failure into what the operator needs
// to know. Anything unrecognised is returned unchanged.
func operatorPlatformError(action platformAction, err error) error {
	if err == nil {
		return nil
	}

	var sentence string
	switch {
	case errors.Is(err, eruncommon.ErrPlatformForbidden):
		sentence = fmt.Sprintf("You do not have permission to %s.", action)
	case errors.Is(err, eruncommon.ErrPlatformUnauthorized):
		sentence = "Your sign-in is no longer valid for this tenant. Sign in again and retry."
	case errors.Is(err, eruncommon.ErrPlatformNotFound):
		if action == actionAddReviewer || action == actionRemoveReviewer {
			sentence = fmt.Sprintf("erun could not %s: the review or reviewer no longer exists. Refresh and try again.", action)
		} else {
			sentence = "That review no longer exists. Refresh to see the current list."
		}
	case errors.Is(err, eruncommon.ErrPlatformConflict):
		// A conflict is the one refusal that is usually transient and usually
		// somebody else's write, so it says what to do rather than what broke.
		sentence = fmt.Sprintf("Someone else changed this while you were working, so erun did not %s. Refresh and try again.", action)
	case errors.Is(err, eruncommon.ErrPlatformNotImplemented):
		sentence = fmt.Sprintf("This control plane cannot %s.", action)
	default:
		return err
	}

	log.Printf("erun-app: %s refused: %v", action, err)
	return errors.New(sentence)
}

// enrollERunPlatformUserError classifies EnrollERunPlatformUser's refusal the
// same way tenantDashboardIdentityFailure already classifies the read path's
// identity failure (tenant_dashboard.go), not operatorPlatformError above:
// every other write it classifies runs only after the dashboard's own
// identity resolution already succeeded, so ErrPlatformUnauthorized there
// means a session that aged out mid-write ("sign in again"). Enrollment runs
// exactly when that resolution has NOT succeeded, so a 401 here means the
// platform's own auth middleware refused the caller's identity outright, for
// one of three reasons its {code, message} envelope distinguishes -- and
// "sign in again" would be wrong advice for any of them.
func enrollERunPlatformUserError(err error) error {
	if err == nil {
		return nil
	}
	handoff := "You do not have permission to enroll yourself in this tenant. Ask an administrator to run the command below, or request an invitation."
	switch {
	case errors.Is(err, eruncommon.ErrPlatformForbidden):
		log.Printf("erun-app: enroll self refused: %v", err)
		return errors.New(handoff)
	case errors.Is(err, eruncommon.ErrPlatformUnauthorized):
		switch eruncommon.PlatformAuthErrorCode(err) {
		case "TENANT_UNRESOLVED", "RESOLUTION_FAILED":
			// Neither is an enrollment answer -- the wire form stays primary,
			// same as tenantDashboardIdentityFailure's own fall-through.
			return err
		}
		log.Printf("erun-app: enroll self refused: %v", err)
		return errors.New(handoff)
	default:
		return err
	}
}
