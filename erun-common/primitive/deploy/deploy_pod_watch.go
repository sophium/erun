package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// kubectlPodWatchExecutionOperation keys the ticker-driven `get pods` poll; a
// List writes no ownership metadata, so the library path is a safe swap.
const kubectlPodWatchExecutionOperation = "kubectl-pod-watch"

// HelmReleaseContainerFailureError reports the pod failure behind an aborted
// helm release instead of helm's timeout text.
type HelmReleaseContainerFailureError struct {
	ReleaseName string
	Namespace   string
	Pod         string
	Container   string
	Reason      string
	Message     string
	Err         error
}

func (e *HelmReleaseContainerFailureError) Error() string {
	if e == nil {
		return ""
	}
	var head string
	if strings.TrimSpace(e.Container) == "" {
		// A pod-level failure (e.g. Unschedulable) has no container yet.
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
	// Now defaults to time.Now; tests inject a fake clock.
	Now func() time.Time
}

func (p podWatchParams) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// podWatchOutcome's Failure is set only for a state that will not recover.
type podWatchOutcome struct {
	Failure *HelmReleaseContainerFailureError
	// Pulling is what helm cannot know: whether its deadline expired mid-pull.
	Pulling []string
}

// podStatusList is a partial parse so kubectl version drift does not break it.
type podStatusList struct {
	Items []PodStatusItem `json:"items"`
}

type PodStatusItem struct {
	Metadata struct {
		Name        string            `json:"name"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Containers     []SpecContainerEntry `json:"containers"`
		InitContainers []SpecContainerEntry `json:"initContainers"`
	} `json:"spec"`
	Status struct {
		Phase                 string                 `json:"phase"`
		Conditions            []PodConditionEntry    `json:"conditions"`
		ContainerStatuses     []ContainerStatusEntry `json:"containerStatuses"`
		InitContainerStatuses []ContainerStatusEntry `json:"initContainerStatuses"`
	} `json:"status"`
}

// SpecContainerEntry carries the declared requests, which no cgroup file records.
type SpecContainerEntry struct {
	Name      string `json:"name"`
	Resources struct {
		Limits   map[string]string `json:"limits"`
		Requests map[string]string `json:"requests"`
	} `json:"resources"`
}

// PodConditionEntry carries the scheduler's verdict for a pod never admitted to
// a node, which has no container statuses.
type PodConditionEntry struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type ContainerStatusEntry struct {
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

// terminalWaitingReasons never recover; image-pull reasons are handled
// separately so a slow pull keeps waiting.
var terminalWaitingReasons = map[string]struct{}{
	"InvalidImageName":           {},
	"CreateContainerConfigError": {},
	"CreateContainerError":       {},
	"RunContainerError":          {},
	"ContainerCannotRun":         {},
}

// imagePullWaitingReasons are retried by the kubelet, so only a permanent
// ErrImagePull message (permanentImagePullFailure) aborts early.
var imagePullWaitingReasons = map[string]struct{}{
	"ImagePullBackOff": {},
	"ErrImagePull":     {},
}

// Transient network causes are excluded so a slow registry keeps waiting.
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

// A single crash can be normal; only repeated restarts are terminal.
const crashLoopRestartThreshold = 2

// Pods are briefly unscheduled on the way to being scheduled; 30s absorbs that
// churn while staying well under helm's rollout timeout.
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

// ERUN_DEPLOY_POD_WATCH_UNSCHEDULED_GRACE is a test-only override.
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

// watchReleasePods ignores transient kubectl errors so a flaky poll never aborts
// a deploy.
func watchReleasePods(ctx context.Context, params podWatchParams) podWatchOutcome {
	if strings.TrimSpace(params.ReleaseName) == "" || strings.TrimSpace(params.Namespace) == "" {
		return podWatchOutcome{}
	}

	pollInterval := resolvePodWatchPollInterval()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	lastSummary := map[string]string{}
	// Replaced, not merged, so a finished pull is never blamed.
	var lastPulling []string
	unscheduledSince := map[string]time.Time{}
	for {
		failure, summaries, pulling, read := pollOnce(ctx, params, unscheduledSince)
		// Only a poll that read replaces lastPulling: the read our own cancellation
		// interrupts would otherwise erase the pull state as it is returned.
		if read {
			lastPulling = pulling
			renderPodSummaries(params.StatusOut, summaries, lastSummary)
		}
		if failure != nil {
			return podWatchOutcome{Failure: failure, Pulling: lastPulling}
		}
		select {
		case <-ctx.Done():
			return podWatchOutcome{Pulling: lastPulling}
		case <-ticker.C:
		}
	}
}

// pollOnce's read is false when no reading was taken, which callers must not
// treat as "nothing is pulling".
func pollOnce(ctx context.Context, params podWatchParams, unscheduledSince map[string]time.Time) (failure *HelmReleaseContainerFailureError, summaries []podSummary, pulling []string, read bool) {
	pods, ok := readPodStatusList(ctx, params)
	if !ok {
		return nil, nil, nil, false
	}
	releasePods := filterReleasePods(pods, params.ReleaseName)
	failure = classifyTerminalFailure(releasePods, params, unscheduledSince)
	return failure, summarizePods(releasePods), pullingContainers(releasePods), true
}

func readPodStatusList(ctx context.Context, params podWatchParams) (podStatusList, bool) {
	output, err := runKubectlGetPods(ctx, params)
	if err != nil {
		return podStatusList{}, false
	}
	return ParsePodStatusList(output)
}

// pullingContainers excludes permanent pull failures, which are terminal.
func pullingContainers(pods []PodStatusItem) []string {
	var pulling []string
	for _, pod := range pods {
		all := append([]ContainerStatusEntry{}, pod.Status.InitContainerStatuses...)
		all = append(all, pod.Status.ContainerStatuses...)
		for _, container := range all {
			if container.State.Waiting == nil {
				continue
			}
			if _, isPull := imagePullWaitingReasons[container.State.Waiting.Reason]; !isPull {
				continue
			}
			if permanentImagePullFailure(container.State.Waiting.Message) {
				continue
			}
			pulling = append(pulling, describePullingContainer(pod.Metadata.Name, container))
		}
	}
	sort.Strings(pulling)
	return pulling
}

// The image separates a slow pull from a tag that was never published.
func describePullingContainer(podName string, container ContainerStatusEntry) string {
	locator := podName + "/" + container.Name
	image := strings.TrimSpace(container.Image)
	if image == "" {
		return locator
	}
	return locator + " (" + image + ")"
}

func runKubectlGetPods(parent context.Context, params podWatchParams) ([]byte, error) {
	if config.CurrentExecutionMode(kubectlPodWatchExecutionOperation) == config.ExecutionModeLibrary {
		return libraryListReleasePods(parent, params)
	}
	return defaultRunKubectlGetPods(parent, params)
}

// libraryListReleasePods re-marshals to JSON: corev1.PodList shares kubectl's
// json tags, so both paths feed the same parser.
func libraryListReleasePods(parent context.Context, params podWatchParams) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, defaultPodWatchKubectlTimeout)
	defer cancel()
	clientset, err := kube.KubernetesClientsetForContext(strings.TrimSpace(params.KubernetesContext))
	if err != nil {
		return nil, err
	}
	list, err := clientset.CoreV1().Pods(strings.TrimSpace(params.Namespace)).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return json.Marshal(list)
}

// defaultRunKubectlGetPods kills only once cmd.Process is set, so a missing
// binary surfaces as the exec error.
func defaultRunKubectlGetPods(parent context.Context, params podWatchParams) ([]byte, error) {
	args := []string{}
	if c := strings.TrimSpace(params.KubernetesContext); c != "" {
		args = append(args, "--context", c)
	}
	args = append(args, "--namespace", params.Namespace, "get", "pods", "-o", "json")

	cmd := core.Command("kubectl", args...)
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
		// Bound the drain: on Windows a security agent can hold the killed kubectl's
		// pipe open, so cmd.Output() never returns. done is buffered, so nothing leaks.
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

func ParsePodStatusList(raw []byte) (podStatusList, bool) {
	var list podStatusList
	if err := json.Unmarshal(raw, &list); err != nil {
		return podStatusList{}, false
	}
	return list, true
}

func filterReleasePods(list podStatusList, releaseName string) []PodStatusItem {
	releaseName = strings.TrimSpace(releaseName)
	if releaseName == "" {
		return nil
	}
	var out []PodStatusItem
	for _, item := range list.Items {
		if item.Metadata.Annotations["meta.helm.sh/release-name"] == releaseName {
			out = append(out, item)
		}
	}
	return out
}

func classifyTerminalFailure(pods []PodStatusItem, params podWatchParams, unscheduledSince map[string]time.Time) *HelmReleaseContainerFailureError {
	for _, pod := range pods {
		all := append([]ContainerStatusEntry{}, pod.Status.InitContainerStatuses...)
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
	// Checked after container failures so a more specific failure wins.
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

func podUnschedulableMessage(pod PodStatusItem) (string, bool) {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == "PodScheduled" && condition.Status == "False" && condition.Reason == "Unschedulable" {
			return condition.Message, true
		}
	}
	return "", false
}

func containerTerminalFailure(c ContainerStatusEntry) (reason, message string, ok bool) {
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

func containerWaitingTerminalFailure(c ContainerStatusEntry) (reason, message string, ok bool) {
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

func summarizePods(pods []PodStatusItem) []podSummary {
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

func formatPodStatusLine(pod PodStatusItem) string {
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
		// Shown immediately, even within the unscheduled grace period.
		if message, unschedulable := podUnschedulableMessage(pod); unschedulable {
			return fmt.Sprintf("pod %s: %s (Unschedulable: %s)", pod.Metadata.Name, phase, message)
		}
		return fmt.Sprintf("pod %s: %s", pod.Metadata.Name, phase)
	}
	return fmt.Sprintf("pod %s: %s", pod.Metadata.Name, strings.Join(parts, ", "))
}

func formatContainerStatus(c ContainerStatusEntry) string {
	if c.State.Waiting != nil {
		reason := c.State.Waiting.Reason
		if reason == "" {
			reason = "Waiting"
		}
		// Pull states are progress, not failure.
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
