package jobs

import (
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// startEnvironmentJobAliveBeat stamps the job's alive fields immediately and
// then on a fixed cadence for as long as the supervisor runs, independent of
// the lease-renewal/progress ticker below: that one backs off to a 300s
// interval for a command job, which is 60x too slow to answer "is the
// supervisor still there" within the 5s bound a caller applies. Stopping it is
// best-effort like the activity lease — if the supervisor is killed outright,
// the beat simply stops, and that silence past EnvironmentJobAliveStaleMs is
// itself the failure signal a caller reads; nothing needs to be undone.
func startEnvironmentJobAliveBeat(recorder *jobRecorder) func() {
	beatOnce := func() {
		recorder.update(func(job *shared.EnvironmentJob) {
			job.LastAliveAt = time.Now()
			job.AliveSeq++
		})
	}
	beatOnce()

	done := make(chan struct{})
	var once sync.Once
	var stopped sync.WaitGroup
	stopped.Add(1)
	go func() {
		defer stopped.Done()
		ticker := time.NewTicker(EnvironmentJobAliveHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				beatOnce()
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		stopped.Wait()
	}
}

// agentJobProgressInterval is how often an agent job's stream is folded. It is
// short because the whole point is that a caller polling for progress sees the
// current activity, not one from minutes ago; each tick reads only the bytes
// that arrived since the last one.
const agentJobProgressInterval = 2 * time.Second

// jobHeartbeat renews the job's activity lease while the work runs and, for an
// agent job, folds the tool's event stream into the record's progress. One
// ticker does both so the lease's name can carry the current activity — which is
// what lets the desktop report "editing <file>" instead of only "running".
type jobHeartbeat struct {
	tenant      string
	environment string
	ttl         time.Duration
	recorder    *jobRecorder
	// agent is nil for a command job, whose log is not an event stream.
	agent *agentProgressReader
	// exclusive says this job holds the environment's exclusivity claim, so
	// each tick renews that claim as well as the presence lease. One ticker
	// drives both: two renewal cadences for two leases with one TTL would only
	// create a window where one has lapsed and the other has not.
	exclusive bool

	mu        sync.Mutex
	progress  shared.AgentJobProgress
	leaseName string
	renewedAt time.Time
}

// startEnvironmentJobHeartbeat takes the lease immediately and keeps it renewed
// for as long as the supervisor runs. Releasing on the way out is best-effort by
// design: if this process is killed instead, the lease records this pid, so the
// lease store reclaims it on the next read exactly as it would for any other
// abandoned holder.
//
// agent is the same reader the output writer feeds, so the heartbeat's snapshot
// reflects everything the process wrote, not only what the output cap retained.
func startEnvironmentJobHeartbeat(tenant, environment string, recorder *jobRecorder, ttl time.Duration, agent *agentProgressReader, exclusive bool) (*jobHeartbeat, func()) {
	job := recorder.snapshot()
	beat := &jobHeartbeat{tenant: tenant, environment: environment, ttl: ttl, recorder: recorder, agent: agent, exclusive: exclusive}
	interval := beat.leaseInterval()
	if agent != nil {
		interval = agentJobProgressInterval
	}
	beat.refresh(true)

	done := make(chan struct{})
	var once sync.Once
	var stopped sync.WaitGroup
	stopped.Add(1)
	go func() {
		defer stopped.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				beat.refresh(false)
			}
		}
	}()
	return beat, func() {
		once.Do(func() { close(done) })
		// The release must not race a tick that is already renewing, or the lease
		// would outlive the supervisor and keep the environment reading as busy.
		stopped.Wait()
		_, _ = activity.ReleaseEnvironmentActivityLease(tenant, environment, job.LeaseID)
		if exclusive {
			// Releasing the environment promptly is what lets the next gate
			// start immediately rather than waiting out a TTL. Best-effort like
			// the presence lease above: a supervisor killed outright releases
			// nothing, and the claim's own recorded pid is what reclaims it.
			_ = releaseEnvironmentJobExclusivityClaim(tenant, environment, job.ID)
		}
	}
}

// refresh folds whatever the agent emitted since the last tick and renews the
// lease. The record is rewritten only when progress actually moved, so a quiet
// agent costs one file read per tick and nothing else.
func (h *jobHeartbeat) refresh(force bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	job := h.recorder.snapshot()
	name := job.Name
	if h.agent != nil {
		progress := h.agent.snapshot()
		if progress != h.progress {
			h.progress = progress
			h.recorder.update(func(job *shared.EnvironmentJob) {
				folded := progress
				job.Progress = &folded
			})
		}
		if summary := progress.Summary(); summary != "" {
			name = job.Name + ": " + summary
		}
	}
	if !force && name == h.leaseName && time.Since(h.renewedAt) < h.leaseInterval() {
		return
	}
	_, _ = activity.TakeEnvironmentActivityLease(activity.TakeEnvironmentActivityLeaseParams{
		Tenant:      h.tenant,
		Environment: h.environment,
		Name:        name,
		ID:          job.LeaseID,
		PID:         job.PID,
		TTL:         h.ttl,
		Holder:      environmentJobHolder(h.tenant),
	})
	h.renewExclusiveClaim(job, name)
	h.leaseName = name
	h.renewedAt = time.Now()
}

// renewExclusiveClaim keeps this job's hold on the environment alive, and is
// also the first thing to record the supervisor's own pid on a claim the start
// took before that process existed — from this tick on, a supervisor that dies
// releases the environment on the next read instead of waiting out the TTL.
//
// A conflict here is ignored, matching the presence lease's own best-effort
// renewal. It can only happen if this supervisor was frozen past its own TTL
// and something else legitimately reclaimed the environment in the gap, which
// is the window every lease-based exclusion has; the alternative — killing a
// job's work over a renewal it lost — trades a rare, bounded overlap for a
// routine one.
func (h *jobHeartbeat) renewExclusiveClaim(job shared.EnvironmentJob, name string) {
	if !h.exclusive {
		return
	}
	_, _ = activity.TakeEnvironmentActivityLease(activity.TakeEnvironmentActivityLeaseParams{
		Tenant:      h.tenant,
		Environment: h.environment,
		Name:        name,
		ID:          environmentJobExclusiveLeaseID(job.ID),
		PID:         job.PID,
		TTL:         h.ttl,
		Exclusive:   true,
		Scope:       activity.EnvironmentActivityLeaseScopeEnvironment,
		Holder:      environmentJobHolder(h.tenant),
	})
}

// leaseInterval renews well inside the TTL, with a floor so a short TTL cannot
// turn the renewal into a busy loop.
func (h *jobHeartbeat) leaseInterval() time.Duration {
	interval := h.ttl / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	return interval
}
