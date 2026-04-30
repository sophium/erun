package eruncommon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
)

type openKubectlRunnerFunc func(args []string, stdout, stderr io.Writer) error

func runOpenKubectl(args []string, stdout, stderr io.Writer) error {
	cmd := exec.Command("kubectl", args...)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	return cmd.Run()
}

func enrichShellDeploymentError(req ShellLaunchParams, err error, runner openKubectlRunnerFunc) error {
	if err == nil {
		return nil
	}
	diagnostic := shellDeploymentFailureDiagnostic(req, runner)
	if diagnostic == "" {
		return err
	}
	return fmt.Errorf("%w\n\n%s", err, diagnostic)
}

func shellDeploymentFailureDiagnostic(req ShellLaunchParams, runner openKubectlRunnerFunc) string {
	if runner == nil {
		return ""
	}
	pods, err := loadRuntimePodDiagnostics(req, runner)
	if err != nil {
		return "Runtime pod diagnostics unavailable: " + err.Error()
	}
	if len(pods.Items) == 0 {
		return fmt.Sprintf("Runtime pod diagnostics: no pods found for selector app=%s in namespace %s.", RuntimeReleaseName(req.Tenant), strings.TrimSpace(req.Namespace))
	}

	sort.SliceStable(pods.Items, func(i, j int) bool {
		return pods.Items[i].Metadata.Name < pods.Items[j].Metadata.Name
	})
	lines := []string{"Runtime pod diagnostics:"}
	for index, pod := range pods.Items {
		if index >= 3 {
			lines = append(lines, fmt.Sprintf("... %d more pod(s) omitted", len(pods.Items)-index))
			break
		}
		lines = append(lines, formatRuntimePodDiagnostic(pod)...)
		lines = append(lines, formatRuntimePodEvents(req, pod, runner)...)
	}
	return strings.Join(lines, "\n")
}

func loadRuntimePodDiagnostics(req ShellLaunchParams, runner openKubectlRunnerFunc) (runtimePodDiagnosticList, error) {
	args := kubectlTargetArgs(req)
	args = append(args, "get", "pods", "-l", "app="+RuntimeReleaseName(req.Tenant), "-o", "json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runner(args, &stdout, &stderr); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return runtimePodDiagnosticList{}, fmt.Errorf("kubectl get pods: %s", detail)
	}
	var pods runtimePodDiagnosticList
	if err := json.Unmarshal(stdout.Bytes(), &pods); err != nil {
		return runtimePodDiagnosticList{}, fmt.Errorf("parse kubectl get pods output: %w", err)
	}
	return pods, nil
}

func formatRuntimePodDiagnostic(pod runtimePodDiagnostic) []string {
	name := strings.TrimSpace(pod.Metadata.Name)
	if name == "" {
		name = "<unknown>"
	}
	status := []string{"phase=" + fallbackRuntimeDiagnosticValue(pod.Status.Phase)}
	if reason := strings.TrimSpace(pod.Status.Reason); reason != "" {
		status = append(status, "reason="+reason)
	}
	if message := strings.TrimSpace(pod.Status.Message); message != "" {
		status = append(status, "message="+singleLineRuntimeDiagnostic(message))
	}
	lines := []string{fmt.Sprintf("- Pod %s: %s", name, strings.Join(status, ", "))}
	if conditions := formatRuntimePodConditions(pod.Status.Conditions); conditions != "" {
		lines = append(lines, "  Conditions: "+conditions)
	}
	for _, container := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		if line := formatRuntimeContainerStatus(container); line != "" {
			lines = append(lines, "  "+line)
		}
	}
	return lines
}

func formatRuntimePodConditions(conditions []runtimePodConditionDiagnostic) string {
	parts := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		conditionType := strings.TrimSpace(condition.Type)
		if conditionType == "" {
			continue
		}
		value := conditionType + "=" + fallbackRuntimeDiagnosticValue(condition.Status)
		if reason := strings.TrimSpace(condition.Reason); reason != "" {
			value += " (" + reason
			if message := strings.TrimSpace(condition.Message); message != "" {
				value += ": " + singleLineRuntimeDiagnostic(message)
			}
			value += ")"
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, ", ")
}

func formatRuntimeContainerStatus(status runtimeContainerStatusDiagnostic) string {
	name := strings.TrimSpace(status.Name)
	if name == "" {
		return ""
	}
	parts := []string{fmt.Sprintf("Container %s: ready=%t restartCount=%d", name, status.Ready, status.RestartCount)}
	if state := formatRuntimeContainerState("state", status.State); state != "" {
		parts = append(parts, state)
	}
	if lastState := formatRuntimeContainerState("lastState", status.LastState); lastState != "" {
		parts = append(parts, lastState)
	}
	return strings.Join(parts, ", ")
}

func formatRuntimeContainerState(label string, state runtimeContainerStateDiagnostic) string {
	switch {
	case state.Waiting != nil:
		return label + "=waiting" + formatRuntimeStateDetail(state.Waiting.Reason, state.Waiting.Message, "", "", 0)
	case state.Running != nil:
		return label + "=running" + formatRuntimeStateDetail("", "", state.Running.StartedAt, "", 0)
	case state.Terminated != nil:
		return label + "=terminated" + formatRuntimeStateDetail(state.Terminated.Reason, state.Terminated.Message, state.Terminated.StartedAt, state.Terminated.FinishedAt, state.Terminated.ExitCode)
	default:
		return ""
	}
}

func formatRuntimeStateDetail(reason, message, startedAt, finishedAt string, exitCode int) string {
	parts := make([]string, 0, 5)
	if exitCode != 0 {
		parts = append(parts, fmt.Sprintf("exitCode=%d", exitCode))
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		parts = append(parts, "reason="+reason)
	}
	if message = strings.TrimSpace(message); message != "" {
		parts = append(parts, "message="+singleLineRuntimeDiagnostic(message))
	}
	if startedAt = strings.TrimSpace(startedAt); startedAt != "" {
		parts = append(parts, "startedAt="+startedAt)
	}
	if finishedAt = strings.TrimSpace(finishedAt); finishedAt != "" {
		parts = append(parts, "finishedAt="+finishedAt)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func formatRuntimePodEvents(req ShellLaunchParams, pod runtimePodDiagnostic, runner openKubectlRunnerFunc) []string {
	podName := strings.TrimSpace(pod.Metadata.Name)
	if podName == "" {
		return nil
	}
	events, err := loadRuntimePodEvents(req, podName, runner)
	if err != nil {
		return []string{"  Events unavailable: " + err.Error()}
	}
	events = warningRuntimePodEvents(events)
	if len(events) == 0 {
		return []string{"  Warning events: none found"}
	}
	sort.SliceStable(events, func(i, j int) bool {
		return runtimeEventTimestamp(events[i]) > runtimeEventTimestamp(events[j])
	})
	lines := []string{"  Warning events:"}
	for index, event := range events {
		if index >= 5 {
			lines = append(lines, fmt.Sprintf("    ... %d more warning event(s) omitted", len(events)-index))
			break
		}
		lines = append(lines, "    "+formatRuntimePodEvent(event))
	}
	return lines
}

func loadRuntimePodEvents(req ShellLaunchParams, podName string, runner openKubectlRunnerFunc) ([]runtimePodEventDiagnostic, error) {
	args := kubectlTargetArgs(req)
	args = append(args, "get", "events", "-o", "json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runner(args, &stdout, &stderr); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("kubectl get events: %s", detail)
	}
	var events runtimePodEventDiagnosticList
	if err := json.Unmarshal(stdout.Bytes(), &events); err != nil {
		return nil, fmt.Errorf("parse kubectl get events output: %w", err)
	}
	filtered := make([]runtimePodEventDiagnostic, 0, len(events.Items))
	for _, event := range events.Items {
		if runtimePodEventMatchesPod(event, podName) {
			filtered = append(filtered, event)
		}
	}
	return filtered, nil
}

func warningRuntimePodEvents(events []runtimePodEventDiagnostic) []runtimePodEventDiagnostic {
	warnings := make([]runtimePodEventDiagnostic, 0, len(events))
	for _, event := range events {
		if strings.EqualFold(strings.TrimSpace(event.Type), "Warning") {
			warnings = append(warnings, event)
		}
	}
	return warnings
}

func runtimePodEventMatchesPod(event runtimePodEventDiagnostic, podName string) bool {
	podName = strings.TrimSpace(podName)
	return podName != "" && (strings.TrimSpace(event.InvolvedObject.Name) == podName || strings.TrimSpace(event.Regarding.Name) == podName)
}

func formatRuntimePodEvent(event runtimePodEventDiagnostic) string {
	parts := make([]string, 0, 4)
	if timestamp := runtimeEventTimestamp(event); timestamp != "" {
		parts = append(parts, timestamp)
	}
	if eventType := strings.TrimSpace(event.Type); eventType != "" {
		parts = append(parts, eventType)
	}
	if reason := strings.TrimSpace(event.Reason); reason != "" {
		parts = append(parts, reason)
	}
	if len(parts) == 0 {
		parts = append(parts, "event")
	}
	line := strings.Join(parts, " ")
	if message := runtimePodEventMessage(event); message != "" {
		line += ": " + singleLineRuntimeDiagnostic(message)
	}
	if count := runtimePodEventCount(event); count > 1 {
		line += fmt.Sprintf(" (x%d)", count)
	}
	return line
}

func runtimeEventTimestamp(event runtimePodEventDiagnostic) string {
	for _, candidate := range []string{event.EventTime, event.LastTimestamp, event.DeprecatedLastTimestamp, event.Series.LastObservedTime, event.Metadata.CreationTimestamp, event.FirstTimestamp, event.DeprecatedFirstTimestamp} {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			return candidate
		}
	}
	return ""
}

func runtimePodEventMessage(event runtimePodEventDiagnostic) string {
	if message := strings.TrimSpace(event.Message); message != "" {
		return message
	}
	return strings.TrimSpace(event.Note)
}

func runtimePodEventCount(event runtimePodEventDiagnostic) int {
	if event.Count > 0 {
		return event.Count
	}
	if event.DeprecatedCount > 0 {
		return event.DeprecatedCount
	}
	if event.Series.Count > 0 {
		return event.Series.Count
	}
	return 0
}

func fallbackRuntimeDiagnosticValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<unknown>"
	}
	return value
}

func singleLineRuntimeDiagnostic(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

type runtimePodDiagnosticList struct {
	Items []runtimePodDiagnostic `json:"items"`
}

type runtimePodDiagnostic struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Phase                 string                             `json:"phase"`
		Reason                string                             `json:"reason"`
		Message               string                             `json:"message"`
		Conditions            []runtimePodConditionDiagnostic    `json:"conditions"`
		InitContainerStatuses []runtimeContainerStatusDiagnostic `json:"initContainerStatuses"`
		ContainerStatuses     []runtimeContainerStatusDiagnostic `json:"containerStatuses"`
	} `json:"status"`
}

type runtimePodConditionDiagnostic struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type runtimeContainerStatusDiagnostic struct {
	Name         string                          `json:"name"`
	Ready        bool                            `json:"ready"`
	RestartCount int                             `json:"restartCount"`
	State        runtimeContainerStateDiagnostic `json:"state"`
	LastState    runtimeContainerStateDiagnostic `json:"lastState"`
}

type runtimeContainerStateDiagnostic struct {
	Waiting    *runtimeContainerWaitingDiagnostic    `json:"waiting"`
	Running    *runtimeContainerRunningDiagnostic    `json:"running"`
	Terminated *runtimeContainerTerminatedDiagnostic `json:"terminated"`
}

type runtimeContainerWaitingDiagnostic struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type runtimeContainerRunningDiagnostic struct {
	StartedAt string `json:"startedAt"`
}

type runtimeContainerTerminatedDiagnostic struct {
	ExitCode   int    `json:"exitCode"`
	Reason     string `json:"reason"`
	Message    string `json:"message"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

type runtimePodEventDiagnosticList struct {
	Items []runtimePodEventDiagnostic `json:"items"`
}

type runtimePodEventDiagnostic struct {
	Metadata struct {
		CreationTimestamp string `json:"creationTimestamp"`
	} `json:"metadata"`
	InvolvedObject struct {
		Name string `json:"name"`
	} `json:"involvedObject"`
	Regarding struct {
		Name string `json:"name"`
	} `json:"regarding"`
	Type                     string `json:"type"`
	Reason                   string `json:"reason"`
	Message                  string `json:"message"`
	Note                     string `json:"note"`
	Count                    int    `json:"count"`
	DeprecatedCount          int    `json:"deprecatedCount"`
	FirstTimestamp           string `json:"firstTimestamp"`
	LastTimestamp            string `json:"lastTimestamp"`
	DeprecatedFirstTimestamp string `json:"deprecatedFirstTimestamp"`
	DeprecatedLastTimestamp  string `json:"deprecatedLastTimestamp"`
	EventTime                string `json:"eventTime"`
	Series                   struct {
		Count            int    `json:"count"`
		LastObservedTime string `json:"lastObservedTime"`
	} `json:"series"`
}
