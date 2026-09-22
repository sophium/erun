package eruncommon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// workspaceSyncTestClock is the injected clock the sync log's cadence is
// measured against, so a test drives the window rather than waiting it out.
type workspaceSyncTestClock struct{ at time.Time }

func (c *workspaceSyncTestClock) now() time.Time          { return c.at }
func (c *workspaceSyncTestClock) advance(d time.Duration) { c.at = c.at.Add(d) }
func (c *workspaceSyncTestClock) start() *workspaceSyncTestClock {
	c.at = time.Unix(workspaceSyncTestMTime, 0)
	return c
}

// captureWorkspaceSyncPassLog points the default logger at a buffer and swaps
// in a fresh sink on the given clock for the duration of the test, so what a
// test reads is only what its own passes produced. Both are process-global
// state, so both are restored.
func captureWorkspaceSyncPassLog(t *testing.T, clock *workspaceSyncTestClock) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	// Without this the default logger's own timestamp prefix sits between the
	// test and the line it is asserting on.
	log.SetFlags(0)
	sink := newWorkspaceSyncPassSink()
	sink.now = clock.now
	previousSink := workspaceSyncPasses
	workspaceSyncPasses = sink
	t.Cleanup(func() {
		workspaceSyncPasses = previousSink
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	})
	return &logs
}

// workspaceSyncQuietPass is a pass that changed nothing, which is the shape a
// steady-state mirror produces every few seconds.
func workspaceSyncQuietPass(mirror string) *workspaceSyncPassLog {
	return &workspaceSyncPassLog{
		params: WorkspaceSyncParams{RemotePath: "/workspace", LocalPath: mirror},
		remote: 4,
		local:  4,
	}
}

func newWorkspaceSyncTestSink(clock *workspaceSyncTestClock, lines *[]string) *workspaceSyncPassSink {
	sink := newWorkspaceSyncPassSink()
	sink.now = clock.now
	sink.logf = func(format string, args ...any) { *lines = append(*lines, fmt.Sprintf(format, args...)) }
	return sink
}

// workspaceSyncLocalStatLine fingerprints a mirror file exactly as the pod's own
// `stat -c '%s %Y %n'` listing would, so the next pass sees it as unchanged. A
// no-op passes neither size nor mtime otherwise, and every assertion about
// silence below would really be asserting about a fetch.
func workspaceSyncLocalStatLine(t *testing.T, root, path string) string {
	t.Helper()
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("stat mirror file %s: %v", path, err)
	}
	return fmt.Sprintf("%d %d %s", info.Size(), info.ModTime().Unix(), path)
}

// The whole point of the change: a pass that changed nothing says nothing. A
// full-detail line per pass per mirror is what buried every other subsystem in
// the desktop log and pushed the retained window under a day.
func TestSyncWorkspaceOnceANoOpPassLogsNothing(t *testing.T) {
	clock := (&workspaceSyncTestClock{}).start()
	logs := captureWorkspaceSyncPassLog(t, clock)

	mirror := t.TempDir()
	seedWorkspaceMirror(t, mirror, "app/keep.go")
	stubWorkspaceSyncSSH(t, []string{"app/keep.go"}, nil)
	t.Setenv(workspaceSyncStubStatEnv, workspaceSyncLocalStatLine(t, mirror, "app/keep.go"))

	if _, err := SyncWorkspaceOnce(context.Background(), WorkspaceSyncParams{
		HostAlias:  "pod",
		RemotePath: "/workspace",
		LocalPath:  mirror,
	}); err != nil {
		t.Fatalf("sync pass: %v", err)
	}
	if got := logs.String(); got != "" {
		t.Fatalf("an in-step pass logged a line it should have suppressed: %q", got)
	}
}

// A pass that moved files is the event the log exists for, and it still carries
// its full inputs and counts when it appears.
func TestSyncWorkspaceOnceAFetchedPassLogsItsCounters(t *testing.T) {
	clock := (&workspaceSyncTestClock{}).start()
	logs := captureWorkspaceSyncPassLog(t, clock)

	mirror := t.TempDir()
	archive := writeWorkspaceSyncArchive(t, map[string][]byte{"app/new.go": []byte("new")})
	// No stat listing, so the one remote path is unknown on the pod's side and
	// must be fetched — the transfer this asserts the line for.
	stubWorkspaceSyncSSH(t, []string{"app/new.go"}, nil)
	t.Setenv(workspaceSyncStubArchiveEnv, archive)

	if _, err := SyncWorkspaceOnce(context.Background(), WorkspaceSyncParams{
		HostAlias:  "pod",
		RemotePath: "/workspace",
		LocalPath:  mirror,
	}); err != nil {
		t.Fatalf("sync pass: %v", err)
	}
	want := fmt.Sprintf("erun: workspace sync /workspace -> %s: notGitRepo=false remote=1 staleIndex=0 mirror=0 fetch=1 deleted=0 signed=0\n", mirror)
	if got := logs.String(); got != want {
		t.Fatalf("fetched-pass line = %q, want %q", got, want)
	}
}

// A pass that deleted files, or that saw the stale index a deletion comes from,
// is the second event that must survive the change.
func TestSyncWorkspaceOnceADeletedPassLogsItsCounters(t *testing.T) {
	clock := (&workspaceSyncTestClock{}).start()
	logs := captureWorkspaceSyncPassLog(t, clock)

	mirror := t.TempDir()
	seedWorkspaceMirror(t, mirror, "app/keep.go", "app/gone.go")
	// gone.go is still in the pod's index and no longer in its worktree; keep.go
	// is unchanged, so the pass deletes without fetching.
	stubWorkspaceSyncSSH(t, []string{"app/keep.go", "app/gone.go"}, []string{"app/gone.go"})
	t.Setenv(workspaceSyncStubStatEnv, workspaceSyncLocalStatLine(t, mirror, "app/keep.go"))

	if _, err := SyncWorkspaceOnce(context.Background(), WorkspaceSyncParams{
		HostAlias:  "pod",
		RemotePath: "/workspace",
		LocalPath:  mirror,
	}); err != nil {
		t.Fatalf("sync pass: %v", err)
	}
	want := fmt.Sprintf("erun: workspace sync /workspace -> %s: notGitRepo=false remote=1 staleIndex=1 mirror=2 fetch=0 deleted=1 signed=0\n", mirror)
	if got := logs.String(); got != want {
		t.Fatalf("deleted-pass line = %q, want %q", got, want)
	}
}

// Every pass with something of its own to report logs a full line, whatever the
// kind of something: a transfer, a deletion, a signature, a stale index, or a
// failure. Nothing in this class is ever folded into an aggregate.
func TestWorkspaceSyncPassSinkReportsEveryPassThatDidSomething(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*workspaceSyncPassLog)
		want   string
	}{
		{"transferred files", func(l *workspaceSyncPassLog) { l.fetch = 3 }, "fetch=3"},
		{"deleted files", func(l *workspaceSyncPassLog) { l.deleted = 2 }, "deleted=2"},
		{"signed artifacts", func(l *workspaceSyncPassLog) { l.signed = 1; l.signNote = "ad-hoc signed app.exe" }, "signed=1 signNote=ad-hoc signed app.exe"},
		{"saw a stale index", func(l *workspaceSyncPassLog) { l.stale = 7 }, "staleIndex=7"},
		{"failed to fetch", func(l *workspaceSyncPassLog) { l.fetchErr = errors.New("tar ended early") }, "fetchError=tar ended early"},
		{"failed to delete", func(l *workspaceSyncPassLog) { l.deleteErr = errors.New("mirror is read-only") }, "deleteError=mirror is read-only"},
		{"failed outright", func(l *workspaceSyncPassLog) { l.failure = errors.New("ssh: no route to host") }, "error=ssh: no route to host"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			clock := (&workspaceSyncTestClock{}).start()
			var lines []string
			sink := newWorkspaceSyncTestSink(clock, &lines)

			pass := workspaceSyncQuietPass("/mirror/a")
			testCase.mutate(pass)
			sink.record(pass)

			if len(lines) != 1 {
				t.Fatalf("a reportable pass logged %d lines, want 1: %q", len(lines), lines)
			}
			if !strings.Contains(lines[0], testCase.want) {
				t.Fatalf("line %q does not carry %q", lines[0], testCase.want)
			}
			// The pass's own inputs ride along with the outcome, which is what
			// made the line worth keeping at all.
			if !strings.Contains(lines[0], "remote=4") || !strings.Contains(lines[0], "mirror=4") {
				t.Fatalf("line %q dropped the pass's inputs", lines[0])
			}
		})
	}
}

// The liveness line is what keeps a quiet log readable: without it, "the sync
// loop is running and nothing changed" would be indistinguishable from "the
// sync loop died". It is emitted on the documented cadence, it covers every
// mirror the window saw, and it stays bounded — counts, never paths.
func TestWorkspaceSyncPassSinkAggregatesQuietPassesOnItsWindow(t *testing.T) {
	clock := (&workspaceSyncTestClock{}).start()
	var lines []string
	sink := newWorkspaceSyncTestSink(clock, &lines)

	sink.record(workspaceSyncQuietPass("/mirror/a"))
	clock.advance(time.Second)
	sink.record(workspaceSyncQuietPass("/mirror/a"))
	clock.advance(time.Second)
	sink.record(workspaceSyncQuietPass("/mirror/b"))
	if len(lines) != 0 {
		t.Fatalf("passes inside the window logged %q", lines)
	}

	clock.advance(workspaceSyncLogWindow - 2*time.Second)
	sink.record(workspaceSyncQuietPass("/mirror/b"))
	want := "erun: workspace sync: 4 no-op passes in the last 15m0s across 2 mirrors"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("liveness lines = %q, want exactly [%q]", lines, want)
	}
	if strings.Contains(lines[0], "/mirror") {
		t.Fatalf("liveness line %q named a mirror; it must stay bounded", lines[0])
	}

	// The window reopens at the line it just emitted, so the pass right after it
	// is not reported, and the next window reports only what fell inside it.
	sink.record(workspaceSyncQuietPass("/mirror/a"))
	if len(lines) != 1 {
		t.Fatalf("a pass after the flush logged %q", lines)
	}
	clock.advance(workspaceSyncLogWindow)
	sink.record(workspaceSyncQuietPass("/mirror/a"))
	want = "erun: workspace sync: 2 no-op passes in the last 15m0s across 1 mirror"
	if len(lines) != 2 || lines[1] != want {
		t.Fatalf("liveness lines = %q, want a second %q", lines, want)
	}
}

// A reportable pass is itself proof the loop is alive, so it both reports the
// quiet passes it stands in for — in the order they happened — and reopens the
// window, rather than letting the next liveness line land immediately behind it.
func TestWorkspaceSyncPassSinkFlushesSuppressedPassesInOrderAndReopensTheWindow(t *testing.T) {
	clock := (&workspaceSyncTestClock{}).start()
	var lines []string
	sink := newWorkspaceSyncTestSink(clock, &lines)

	sink.record(workspaceSyncQuietPass("/mirror/a"))
	clock.advance(workspaceSyncLogWindow)
	pass := workspaceSyncQuietPass("/mirror/a")
	pass.fetch = 2
	sink.record(pass)

	want := []string{
		"erun: workspace sync: 1 no-op pass in the last 15m0s across 1 mirror",
		"erun: workspace sync /workspace -> /mirror/a: notGitRepo=false remote=4 staleIndex=0 mirror=4 fetch=2 deleted=0 signed=0",
	}
	if len(lines) != 2 || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("lines = %q, want %q", lines, want)
	}

	// The window reopened at that line: a quiet pass just inside the next one
	// stays silent, and the one that ends it counts only what fell inside.
	clock.advance(workspaceSyncLogWindow - time.Second)
	sink.record(workspaceSyncQuietPass("/mirror/a"))
	if len(lines) != 2 {
		t.Fatalf("a pass inside the reopened window logged %q", lines)
	}
	clock.advance(time.Second)
	sink.record(workspaceSyncQuietPass("/mirror/a"))
	want = append(want, "erun: workspace sync: 2 no-op passes in the last 15m0s across 1 mirror")
	if len(lines) != 3 || lines[2] != want[2] {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
}

// A mirror whose silence is itself the diagnosis — a workspace that is not a
// git repo, a worktree listing that could not be read — says so when it enters
// that state, rather than on every pass or never.
func TestWorkspaceSyncPassSinkReportsOnlyOnEntryToAStandingQuietState(t *testing.T) {
	cases := []struct {
		name  string
		state func(*workspaceSyncPassLog)
		want  string
	}{
		{
			name:  "a workspace that is not a git repo",
			state: func(l *workspaceSyncPassLog) { l.notGitRepo = true },
			want:  "notGitRepo=true",
		},
		{
			name:  "a worktree listing that could not be read",
			state: func(l *workspaceSyncPassLog) { l.staleUnknown = true },
			want:  "staleIndex=unknown",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			clock := (&workspaceSyncTestClock{}).start()
			var lines []string
			sink := newWorkspaceSyncTestSink(clock, &lines)

			state := workspaceSyncQuietPass("/mirror/a")
			testCase.state(state)
			sink.record(state)
			if len(lines) != 1 || !strings.Contains(lines[0], testCase.want) {
				t.Fatalf("entering the state logged %q, want one line carrying %q", lines, testCase.want)
			}

			sink.record(state)
			if len(lines) != 1 {
				t.Fatalf("the same standing state logged again: %q", lines)
			}

			sink.record(workspaceSyncQuietPass("/mirror/a"))
			if len(lines) != 1 {
				t.Fatalf("an in-step pass logged %q", lines)
			}

			sink.record(state)
			if len(lines) != 2 || !strings.Contains(lines[1], testCase.want) {
				t.Fatalf("re-entering the state logged %q, want a second line carrying %q", lines, testCase.want)
			}
		})
	}
}

// The aggregate is fed by real passes, not only by the sink's own tests: a
// desktop loop over a steady-state mirror costs one line per window, and that
// line still proves how many passes it is standing in for.
func TestSyncWorkspaceOnceAggregatesNoOpPassesIntoLivenessLines(t *testing.T) {
	clock := (&workspaceSyncTestClock{}).start()
	logs := captureWorkspaceSyncPassLog(t, clock)

	mirror := t.TempDir()
	seedWorkspaceMirror(t, mirror, "app/keep.go")
	stubWorkspaceSyncSSH(t, []string{"app/keep.go"}, nil)
	t.Setenv(workspaceSyncStubStatEnv, workspaceSyncLocalStatLine(t, mirror, "app/keep.go"))
	pass := func() {
		t.Helper()
		if _, err := SyncWorkspaceOnce(context.Background(), WorkspaceSyncParams{
			HostAlias:  "pod",
			RemotePath: "/workspace",
			LocalPath:  mirror,
		}); err != nil {
			t.Fatalf("sync pass: %v", err)
		}
	}

	pass()
	clock.advance(time.Minute)
	pass()
	if got := logs.String(); got != "" {
		t.Fatalf("two in-step passes logged %q", got)
	}
	clock.advance(workspaceSyncLogWindow)
	pass()
	want := "erun: workspace sync: 3 no-op passes in the last 16m0s across 1 mirror\n"
	if got := logs.String(); got != want {
		t.Fatalf("liveness line = %q, want %q", got, want)
	}
}
