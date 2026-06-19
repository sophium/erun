package eruncommon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// HelmReleaseContainerFailureError is returned when the pod watcher observes
// a terminal container failure for a pod that belongs to the in-flight helm
// release. DeployHelmChart raises this error after killing the helm process,
// so the caller can surface the underlying pod/container/reason instead of
// helm's generic timeout text.
type HelmReleaseContainerFailureError struct {
	ReleaseName string
	Namespace   string
	Pod         string
	Container   string
	Reason      string
	Message     string
	// Err is the helm process error captured after the watcher killed the
	// upgrade, if any. It is wrapped so errors.As/Is on helm-side error
	// types still works for callers that care.
	Err error
}

func (e *HelmReleaseContainerFailureError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{
		fmt.Sprintf("deploy failed early: pod %s container %s %s", e.Pod, e.Container, e.Reason),
	}
	if msg := strings.TrimSpace(e.Message); msg != "" {
		parts = append(parts, msg)
	}
	return strings.Join(parts, ": ")
}

func (e *HelmReleaseContainerFailureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// podWatchParams configures the pod watcher started alongside a helm upgrade.
// StatusOut receives the per-pod summary lines the watcher prints as the
// release rolls out; in production it is the same writer ctx.Info uses
// (typically stdout) so the lines align with the surrounding deploy block.
type podWatchParams struct {
	ReleaseName       string
	Namespace         string
	KubernetesContext string
	StatusOut         io.Writer
}

// podWatchOutcome is the watcher's terminal observation. Failure is non-nil
// only when a container has reached a state that will not recover without
// operator intervention (image pull failures, config errors, repeated crashes).
type podWatchOutcome struct {
	Failure *HelmReleaseContainerFailureError
}

// podStatusList is the parsed shape of `kubectl get pods -o json` we care
// about. The parser is tolerant of unknown fields so kubectl version drift
// does not break the watcher.
type podStatusList struct {
	Items []podStatusItem `json:"items"`
}

type podStatusItem struct {
	Metadata struct {
		Name        string            `json:"name"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Status struct {
		Phase                 string                 `json:"phase"`
		ContainerStatuses     []containerStatusEntry `json:"containerStatuses"`
		InitContainerStatuses []containerStatusEntry `json:"initContainerStatuses"`
	} `json:"status"`
}

type containerStatusEntry struct {
	Name         string         `json:"name"`
	RestartCount int            `json:"restartCount"`
	Ready        bool           `json:"ready"`
	State        containerState `json:"state"`
	LastState    containerState `json:"lastState"`
}

type containerState struct {
	Waiting    *containerStateWaiting    `json:"waiting"`
	Running    *containerStateRunning    `json:"running"`
	Terminated *containerStateTerminated `json:"terminated"`
}

type containerStateWaiting struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type containerStateRunning struct {
	StartedAt string `json:"startedAt"`
}

type containerStateTerminated struct {
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	ExitCode int    `json:"exitCode"`
}

// terminalWaitingReasons enumerate container `state.waiting.reason` values
// that will not recover without operator intervention. Once a container
// reports any of these, the watcher kills the helm upgrade rather than
// waiting for the timeout. Image-pull reasons are deliberately NOT here — they
// are handled by imagePullWaitingReasons / permanentImagePullFailure so a slow
// pull keeps waiting. InvalidImageName stays terminal: the image reference is
// malformed, so no amount of waiting makes it pullable.
var terminalWaitingReasons = map[string]struct{}{
	"InvalidImageName":           {},
	"CreateContainerConfigError": {},
	"CreateContainerError":       {},
	"RunContainerError":          {},
	"ContainerCannotRun":         {},
}

// imagePullWaitingReasons mark a container as still trying to fetch its image.
// A large image on a slow or rate-limited registry legitimately cycles through
// these states (Pulling -> ErrImagePull -> ImagePullBackOff -> retry) while the
// kubelet retries with growing backoff, so they are NOT terminal on their own:
// the watcher keeps waiting up to the rollout timeout. Only a *permanent* pull
// error (permanentImagePullFailure) aborts the deploy early. ImagePullBackOff's
// own message is a generic "Back-off pulling image" with no detail, but the
// kubelet alternates into ErrImagePull whose message carries the real registry
// error — that is where a permanent failure (missing tag, bad auth) is caught.
var imagePullWaitingReasons = map[string]struct{}{
	"ImagePullBackOff": {},
	"ErrImagePull":     {},
}

// permanentImagePullFailureSubstrings are lowercased fragments of kubelet
// image-pull error messages that retrying will never resolve: the registry has
// definitively rejected the reference (missing tag/repo, denied auth, malformed
// name). Transient causes (timeouts, DNS blips, connection resets, TLS
// handshake failures) are deliberately excluded so a slow or briefly
// unreachable registry keeps waiting up to the rollout timeout rather than
// failing the deploy on a recoverable hiccup.
var permanentImagePullFailureSubstrings = []string{
	"manifest unknown",
	"manifestunknown",
	"not found",
	"repository does not exist",
	"pull access denied",
	"unauthorized",
	"authentication required",
	"forbidden",
	"denied",
	"invalid reference format",
	"no such image",
}

// permanentImagePullFailure reports whether an image-pull error message
// indicates a failure that will not recover by waiting.
func permanentImagePullFailure(message string) bool {
	lower := strings.ToLower(message)
	for _, frag := range permanentImagePullFailureSubstrings {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}

// crashLoopRestartThreshold is the number of restarts a CrashLoopBackOff
// container must accumulate before the watcher treats it as terminal. Single
// transient crashes during init can be normal; repeated ones are not.
const crashLoopRestartThreshold = 2

// podWatchPollInterval and podWatchKubectlTimeout govern the kubectl poll
// loop. They are tunable via env vars so the integration suite can drive a
// faster cadence than production. ERUN_DEPLOY_POD_WATCH_INTERVAL accepts any
// time.ParseDuration value; values below 100ms are clamped to 100ms.
const (
	defaultPodWatchPollInterval   = 2 * time.Second
	defaultPodWatchKubectlTimeout = 10 * time.Second
	minPodWatchPollInterval       = 100 * time.Millisecond
)

func resolvePodWatchPollInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ERUN_DEPLOY_POD_WATCH_INTERVAL"))
	if raw == "" {
		return defaultPodWatchPollInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultPodWatchPollInterval
	}
	if d < minPodWatchPollInterval {
		return minPodWatchPollInterval
	}
	return d
}

// watchReleasePods polls kubectl until the parent context is canceled or a
// terminal container failure is observed. It writes a fresh status line to
// stderr each time the per-pod summary changes so the user sees progress.
// The poller itself is best-effort: transient kubectl errors are ignored
// (a flaky `get pods` call should not abort a deploy that helm would
// otherwise drive to success).
func watchReleasePods(ctx context.Context, params podWatchParams) podWatchOutcome {
	if strings.TrimSpace(params.ReleaseName) == "" || strings.TrimSpace(params.Namespace) == "" {
		return podWatchOutcome{}
	}

	pollInterval := resolvePodWatchPollInterval()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	lastSummary := map[string]string{}
	for {
		failure, summaries := pollOnce(ctx, params)
		renderPodSummaries(params.StatusOut, summaries, lastSummary)
		if failure != nil {
			return podWatchOutcome{Failure: failure}
		}
		select {
		case <-ctx.Done():
			return podWatchOutcome{}
		case <-ticker.C:
		}
	}
}

func pollOnce(ctx context.Context, params podWatchParams) (*HelmReleaseContainerFailureError, []podSummary) {
	output, err := runKubectlGetPods(ctx, params)
	if err != nil {
		return nil, nil
	}
	pods, ok := parsePodStatusList(output)
	if !ok {
		return nil, nil
	}
	releasePods := filterReleasePods(pods, params.ReleaseName)
	failure := classifyTerminalFailure(releasePods, params)
	return failure, summarizePods(releasePods)
}

// runKubectlGetPods runs `kubectl get pods -n <ns> -o json`. The parent
// context cancels when helm finishes; an additional kubectlTimeout bounds a
// hung apiserver. Both paths kill the subprocess only after Start() has
// actually set cmd.Process so a missing kubectl binary surfaces as the
// underlying exec error rather than a nil-pointer panic.
func runKubectlGetPods(parent context.Context, params podWatchParams) ([]byte, error) {
	args := []string{}
	if c := strings.TrimSpace(params.KubernetesContext); c != "" {
		args = append(args, "--context", c)
	}
	args = append(args, "--namespace", params.Namespace, "get", "pods", "-o", "json")

	cmd := Command("kubectl", args...)
	type runResult struct {
		out []byte
		err error
	}
	done := make(chan runResult, 1)
	go func() {
		out, err := cmd.Output()
		done <- runResult{out: out, err: err}
	}()

	timer := time.NewTimer(defaultPodWatchKubectlTimeout)
	defer timer.Stop()

	killAndDrain := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	}
	select {
	case <-parent.Done():
		killAndDrain()
		return nil, parent.Err()
	case <-timer.C:
		killAndDrain()
		return nil, fmt.Errorf("kubectl get pods timed out")
	case r := <-done:
		return r.out, r.err
	}
}

func parsePodStatusList(raw []byte) (podStatusList, bool) {
	var list podStatusList
	if err := json.Unmarshal(raw, &list); err != nil {
		return podStatusList{}, false
	}
	return list, true
}

func filterReleasePods(list podStatusList, releaseName string) []podStatusItem {
	releaseName = strings.TrimSpace(releaseName)
	if releaseName == "" {
		return nil
	}
	var out []podStatusItem
	for _, item := range list.Items {
		if item.Metadata.Annotations["meta.helm.sh/release-name"] == releaseName {
			out = append(out, item)
		}
	}
	return out
}

func classifyTerminalFailure(pods []podStatusItem, params podWatchParams) *HelmReleaseContainerFailureError {
	for _, pod := range pods {
		all := append([]containerStatusEntry{}, pod.Status.InitContainerStatuses...)
		all = append(all, pod.Status.ContainerStatuses...)
		for _, container := range all {
			if reason, message, ok := containerTerminalFailure(container); ok {
				return &HelmReleaseContainerFailureError{
					ReleaseName: params.ReleaseName,
					Namespace:   params.Namespace,
					Pod:         pod.Metadata.Name,
					Container:   container.Name,
					Reason:      reason,
					Message:     message,
				}
			}
		}
	}
	return nil
}

func containerTerminalFailure(c containerStatusEntry) (reason, message string, ok bool) {
	if c.State.Waiting != nil {
		if r, m, waitingOK := containerWaitingTerminalFailure(c); waitingOK {
			return r, m, true
		}
	}
	if t := c.State.Terminated; t != nil && t.ExitCode != 0 && c.RestartCount >= crashLoopRestartThreshold {
		reason := strings.TrimSpace(t.Reason)
		if reason == "" {
			reason = "Error"
		}
		return reason, t.Message, true
	}
	return "", "", false
}

// containerWaitingTerminalFailure classifies a waiting container as a terminal
// failure: either a reason in terminalWaitingReasons, or a CrashLoopBackOff
// that has restarted past the threshold (in which case the last terminated
// message is preferred when present).
func containerWaitingTerminalFailure(c containerStatusEntry) (reason, message string, ok bool) {
	w := c.State.Waiting
	if _, isPull := imagePullWaitingReasons[w.Reason]; isPull {
		// Still fetching the image: keep waiting up to the rollout timeout
		// unless the registry has permanently rejected the reference.
		if permanentImagePullFailure(w.Message) {
			return w.Reason, w.Message, true
		}
		return "", "", false
	}
	if _, terminal := terminalWaitingReasons[w.Reason]; terminal {
		return w.Reason, w.Message, true
	}
	if w.Reason == "CrashLoopBackOff" && c.RestartCount >= crashLoopRestartThreshold {
		msg := w.Message
		if t := c.LastState.Terminated; t != nil && strings.TrimSpace(t.Message) != "" {
			msg = strings.TrimSpace(t.Message)
		}
		return w.Reason, msg, true
	}
	return "", "", false
}

// podSummary is one printable line per pod: short pod name plus a list of
// container states. Ordered deterministically so renderPodSummaries can
// dedupe by string equality.
type podSummary struct {
	PodName string
	Line    string
}

func summarizePods(pods []podStatusItem) []podSummary {
	out := make([]podSummary, 0, len(pods))
	for _, pod := range pods {
		out = append(out, podSummary{
			PodName: pod.Metadata.Name,
			Line:    formatPodStatusLine(pod),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PodName < out[j].PodName })
	return out
}

func formatPodStatusLine(pod podStatusItem) string {
	parts := make([]string, 0, len(pod.Status.InitContainerStatuses)+len(pod.Status.ContainerStatuses))
	for _, c := range pod.Status.InitContainerStatuses {
		parts = append(parts, "init/"+formatContainerStatus(c))
	}
	for _, c := range pod.Status.ContainerStatuses {
		parts = append(parts, formatContainerStatus(c))
	}
	if len(parts) == 0 {
		phase := strings.TrimSpace(pod.Status.Phase)
		if phase == "" {
			phase = "Pending"
		}
		return fmt.Sprintf("pod %s: %s", pod.Metadata.Name, phase)
	}
	return fmt.Sprintf("pod %s: %s", pod.Metadata.Name, strings.Join(parts, ", "))
}

func formatContainerStatus(c containerStatusEntry) string {
	if c.State.Waiting != nil {
		reason := c.State.Waiting.Reason
		if reason == "" {
			reason = "Waiting"
		}
		// Image-pull states are progress, not failure: label them "Pulling
		// image" so the operator reads the rollout as still fetching the image
		// (the deploy keeps waiting up to the rollout timeout) rather than
		// mistaking "Waiting (ImagePullBackOff)" for a hard error.
		verb := "Waiting"
		if _, isPull := imagePullWaitingReasons[c.State.Waiting.Reason]; isPull {
			verb = "Pulling image"
		}
		if c.RestartCount > 0 {
			return fmt.Sprintf("%s %s (%s, restarts=%d)", c.Name, verb, reason, c.RestartCount)
		}
		return fmt.Sprintf("%s %s (%s)", c.Name, verb, reason)
	}
	if c.State.Running != nil {
		ready := "NotReady"
		if c.Ready {
			ready = "Ready"
		}
		return fmt.Sprintf("%s Running (%s)", c.Name, ready)
	}
	if c.State.Terminated != nil {
		reason := c.State.Terminated.Reason
		if reason == "" {
			reason = "Terminated"
		}
		return fmt.Sprintf("%s Terminated (%s exitCode=%d)", c.Name, reason, c.State.Terminated.ExitCode)
	}
	return fmt.Sprintf("%s %s", c.Name, "Unknown")
}

// renderPodSummaries writes a status line for every pod whose summary
// changed since the last poll. Output goes to stderr and is prefixed with
// four spaces to align with the existing "    namespace ..." block.
func renderPodSummaries(out io.Writer, summaries []podSummary, last map[string]string) {
	if out == nil {
		return
	}
	for _, s := range summaries {
		if last[s.PodName] == s.Line {
			continue
		}
		last[s.PodName] = s.Line
		_, _ = io.WriteString(out, "    "+s.Line+"\n")
	}
}
