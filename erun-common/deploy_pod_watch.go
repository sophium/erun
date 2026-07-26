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

// HelmReleaseContainerFailureError surfaces the real pod/container failure
// behind an aborted helm release, so callers report the underlying reason
// instead of helm's generic timeout text.
type HelmReleaseContainerFailureError struct {
	ReleaseName string
	Namespace   string
	Pod         string
	Container   string
	Reason      string
	Message     string
	// Err wraps the underlying helm process error so errors.As/Is still
	// reaches helm-side error types.
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

// podStatusList is a deliberately partial parse of `kubectl get pods -o json`:
// unknown fields are ignored so kubectl version drift does not break the watcher.
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

func permanentImagePullFailure(message string) bool {
	lower := strings.ToLower(message)
	for _, frag := range permanentImagePullFailureSubstrings {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}

// crashLoopRestartThreshold guards against transient init crashes: a single
// crash can be normal, so only repeated restarts mark a container terminal.
const crashLoopRestartThreshold = 2

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

// watchReleasePods is best-effort: transient kubectl errors are ignored so a
// flaky `get pods` never aborts a deploy that helm would otherwise drive to
// success.
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

// runKubectlGetPods bounds a hung apiserver with its own timeout beyond the
// parent context, and kills the subprocess only once cmd.Process is set so a
// missing kubectl binary surfaces as the exec error rather than a nil panic.
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
		// Bound the drain. On Windows an endpoint-security agent can hold the
		// killed kubectl's stdout pipe open (or a grandchild inherits its write
		// end), so cmd.Output() never reaches EOF and never returns — even though
		// the process is dead. Without this timeout the watcher goroutine, and the
		// deploy that joins it after helm has already succeeded, hang forever
		// (observed: `erun deploy` alive minutes past `helm STATUS=deployed`, no
		// child processes, blocked in Go). done is buffered (cap 1), so abandoning
		// it here leaks nothing — the goroutine's send still completes.
		select {
		case <-done:
		case <-time.After(defaultPodWatchKubectlTimeout):
		}
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

func containerWaitingTerminalFailure(c containerStatusEntry) (reason, message string, ok bool) {
	w := c.State.Waiting
	if _, isPull := imagePullWaitingReasons[w.Reason]; isPull {
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

// renderPodSummaries prefixes each line with four spaces to align with the
// existing "    namespace ..." status block.
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
