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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// kubectlPodWatchExecutionOperation is the ExecutionModeFor/
// ExecutionModeReport key for the `kubectl [--context c] --namespace <ns> get
// pods -o json` poll watchReleasePods runs throughout every real helm deploy
// (runHelmDeployWithPodWatch, deploy.go) to catch an early container failure
// before helm's own timeout fires. Its file and function names say "watch",
// but the mechanism has always been a ticker-driven re-Get, not a real
// client-go Watch -- exactly the shape kubectl-deployment-wait
// (kubernetes_client_go.go) already proved out, and the design pass behind
// kubectl-secret-apply's client-side-apply choice judged this class of
// operation safe for a read: a List writes no ownership metadata, so there is
// no server-side-apply-style divergence to reproduce here, only the same List
// the subprocess path already runs. It gets its own key rather than reusing
// kubectl-deployment-wait: a different resource kind (Pod, not Deployment), a
// List instead of a single Get, and its own classification logic entirely --
// exactly the two-independent-polling-loops distinction that keeps the two
// keys apart.
const kubectlPodWatchExecutionOperation = "kubectl-pod-watch"

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
	var head string
	if strings.TrimSpace(e.Container) == "" {
		// A pod-level failure (e.g. Unschedulable) precedes any container even
		// existing, so there is no container name to report.
		head = fmt.Sprintf("deploy failed early: pod %s %s", e.Pod, e.Reason)
	} else {
		head = fmt.Sprintf("deploy failed early: pod %s container %s %s", e.Pod, e.Container, e.Reason)
	}
	parts := []string{head}
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
	// Now returns the current time; nil defaults to time.Now. Tests inject a
	// fake clock here rather than sleeping real wall-clock time to exercise the
	// unschedulable grace period (see defaultUnscheduledGracePeriod).
	Now func() time.Time
}

func (p podWatchParams) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
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
	Spec struct {
		Containers []specContainerEntry `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase                 string                 `json:"phase"`
		Conditions            []podConditionEntry    `json:"conditions"`
		ContainerStatuses     []containerStatusEntry `json:"containerStatuses"`
		InitContainerStatuses []containerStatusEntry `json:"initContainerStatuses"`
	} `json:"status"`
}

// specContainerEntry is `spec.containers[]`: the declared resource limits a
// container asked for, which status.containerStatuses does not carry.
type specContainerEntry struct {
	Name      string `json:"name"`
	Resources struct {
		Limits map[string]string `json:"limits"`
	} `json:"resources"`
}

// podConditionEntry is a `status.conditions[]` entry. A pod that never gets
// admitted to a node has no container statuses at all to report a waiting
// reason from — the scheduler's own verdict lives only here, as
// PodScheduled=False with reason Unschedulable.
type podConditionEntry struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type containerStatusEntry struct {
	Name         string         `json:"name"`
	Image        string         `json:"image"`
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

// unscheduledGracePeriod is how long a pod may report PodScheduled=False
// before the watcher treats it as terminal. A pod is briefly unscheduled on
// the way to being scheduled — the scheduler re-evaluates on every relevant
// cluster change, and a node that is about to free up or finish autoscaling
// can easily take longer than one poll tick — so acting on the very first
// unschedulable observation would abort deploys that were only ever waiting
// normally. 30s is long enough to absorb that normal churn (roughly ten poll
// ticks at the default 2s interval) while staying well under the 5-minute
// helm rollout timeout it exists to beat: a genuine capacity/quota problem
// (e.g. "Insufficient cpu") does not resolve itself in 30s, so the grace only
// ever delays the honest failure, never turns it into a false positive.
const defaultUnscheduledGracePeriod = 30 * time.Second

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

// resolveUnscheduledGracePeriod honors ERUN_DEPLOY_POD_WATCH_UNSCHEDULED_GRACE,
// the test-only twin of ERUN_DEPLOY_POD_WATCH_INTERVAL: a real-run integration
// scenario cannot wait out defaultUnscheduledGracePeriod's 30s and stay fast,
// so it shrinks the grace instead of asserting on a wall-clock sleep tied to
// the production value. Not a production knob — the reasoning for the default
// lives on defaultUnscheduledGracePeriod, not on this override.
func resolveUnscheduledGracePeriod() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ERUN_DEPLOY_POD_WATCH_UNSCHEDULED_GRACE"))
	if raw == "" {
		return defaultUnscheduledGracePeriod
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return defaultUnscheduledGracePeriod
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
	// unscheduledSince tracks, per pod name, the first poll that observed
	// PodScheduled=False so classifyTerminalFailure can apply the grace period
	// across polls rather than deciding on a single snapshot.
	unscheduledSince := map[string]time.Time{}
	for {
		failure, summaries := pollOnce(ctx, params, unscheduledSince)
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

func pollOnce(ctx context.Context, params podWatchParams, unscheduledSince map[string]time.Time) (*HelmReleaseContainerFailureError, []podSummary) {
	output, err := runKubectlGetPods(ctx, params)
	if err != nil {
		return nil, nil
	}
	pods, ok := parsePodStatusList(output)
	if !ok {
		return nil, nil
	}
	releasePods := filterReleasePods(pods, params.ReleaseName)
	failure := classifyTerminalFailure(releasePods, params, unscheduledSince)
	return failure, summarizePods(releasePods)
}

// runKubectlGetPods dispatches to the subprocess or library path per the
// kubectl-pod-watch execution mode (see execution_mode.go).
func runKubectlGetPods(parent context.Context, params podWatchParams) ([]byte, error) {
	if currentExecutionMode(kubectlPodWatchExecutionOperation) == ExecutionModeLibrary {
		return libraryListReleasePods(parent, params)
	}
	return defaultRunKubectlGetPods(parent, params)
}

// libraryListReleasePods is the library-backed alternative to
// defaultRunKubectlGetPods, listing the same namespace's pods via
// k8s.io/client-go instead of shelling out to kubectl. The typed result is
// re-marshaled to JSON and fed through the exact same
// parsePodStatusList/classifyTerminalFailure/summarizePods pipeline the
// subprocess path uses: corev1.PodList carries the identical json tags
// kubectl's own `-o json` output does, so this is not a second parser to keep
// in sync, only a second source of the same bytes. Bounded by the same
// defaultPodWatchKubectlTimeout the subprocess path times its kubectl
// invocation with, layered onto whatever deadline the parent (helm deploy
// watch) context already carries.
func libraryListReleasePods(parent context.Context, params podWatchParams) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, defaultPodWatchKubectlTimeout)
	defer cancel()
	clientset, err := kubernetesClientsetForContext(strings.TrimSpace(params.KubernetesContext))
	if err != nil {
		return nil, err
	}
	list, err := clientset.CoreV1().Pods(strings.TrimSpace(params.Namespace)).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return json.Marshal(list)
}

// defaultRunKubectlGetPods bounds a hung apiserver with its own timeout
// beyond the parent context, and kills the subprocess only once cmd.Process
// is set so a missing kubectl binary surfaces as the exec error rather than a
// nil panic.
func defaultRunKubectlGetPods(parent context.Context, params podWatchParams) ([]byte, error) {
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

func classifyTerminalFailure(pods []podStatusItem, params podWatchParams, unscheduledSince map[string]time.Time) *HelmReleaseContainerFailureError {
	for _, pod := range pods {
		all := append([]containerStatusEntry{}, pod.Status.InitContainerStatuses...)
		all = append(all, pod.Status.ContainerStatuses...)
		for _, container := range all {
			if reason, message, ok := containerTerminalFailure(container); ok {
				delete(unscheduledSince, pod.Metadata.Name)
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
	// A pod that never got admitted to a node has no container statuses at all
	// to report a waiting reason from — the only place that carries why is
	// status.conditions. Checked second, after container-level failures, so a
	// pod that both failed to schedule once and later ran into a real
	// container problem reports the more specific container failure.
	for _, pod := range pods {
		message, unschedulable := podUnschedulableMessage(pod)
		if !unschedulable {
			delete(unscheduledSince, pod.Metadata.Name)
			continue
		}
		since, seen := unscheduledSince[pod.Metadata.Name]
		if !seen {
			unscheduledSince[pod.Metadata.Name] = params.now()
			continue
		}
		if params.now().Sub(since) < resolveUnscheduledGracePeriod() {
			continue
		}
		return &HelmReleaseContainerFailureError{
			ReleaseName: params.ReleaseName,
			Namespace:   params.Namespace,
			Pod:         pod.Metadata.Name,
			Container:   "",
			Reason:      "Unschedulable",
			Message:     message,
		}
	}
	return nil
}

// podUnschedulableMessage returns the scheduler's own message when a pod
// carries PodScheduled=False with reason Unschedulable, verbatim — so the
// abort reaches both the deploy output and the recorded provision-error with
// exactly what the scheduler said (e.g. "0/1 nodes are available: 1
// Insufficient cpu, 1 Insufficient memory") rather than a generic timeout.
func podUnschedulableMessage(pod podStatusItem) (string, bool) {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == "PodScheduled" && condition.Status == "False" && condition.Reason == "Unschedulable" {
			return condition.Message, true
		}
	}
	return "", false
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
		// A pod with no container statuses at all was never admitted to a node,
		// so the scheduler's own reason (if any) is the only thing worth showing
		// — visible immediately, even while still inside the grace period that
		// classifyTerminalFailure applies before treating it as terminal.
		if message, unschedulable := podUnschedulableMessage(pod); unschedulable {
			return fmt.Sprintf("pod %s: %s (Unschedulable: %s)", pod.Metadata.Name, phase, message)
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
