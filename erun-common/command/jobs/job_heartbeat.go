package jobs

import (
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

// startEnvironmentJobAliveBeat stamps the alive fields on their own fast cadence;
// the lease ticker backs off far too slowly to answer liveness within 5s.
func startEnvironmentJobAliveBeat(recorder *jobRecorder) func() {
	beatOnce := func() {
		recorder.update(func(job *jobstate.EnvironmentJob) {
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

// agentJobProgressInterval is short so polled progress shows current activity.
const agentJobProgressInterval = 2 * time.Second

// jobHeartbeat renews the job's lease and folds agent progress into its name.
type jobHeartbeat struct {
	tenant      string
	environment string
	ttl         time.Duration
	recorder    *jobRecorder
	agent       *agentProgressReader
	// exclusive also renews the environment claim on the same tick, so the two
	// leases never lapse independently.
	exclusive bool

	mu        sync.Mutex
	progress  jobstate.AgentJobProgress
	leaseName string
	renewedAt time.Time
}

// startEnvironmentJobHeartbeat takes the lease now and renews it until stopped.
// Release is best-effort; a killed supervisor's lease is reclaimed by pid.
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
		// Wait for any in-flight renewal so it cannot outlive the release.
		stopped.Wait()
		_, _ = activity.ReleaseEnvironmentActivityLease(tenant, environment, job.LeaseID)
		if exclusive {
			_ = releaseEnvironmentJobExclusivityClaim(tenant, environment, job.ID)
		}
	}
}

// refresh rewrites the record only when progress moved.
func (h *jobHeartbeat) refresh(force bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	job := h.recorder.snapshot()
	name := job.Name
	if h.agent != nil {
		progress := h.agent.snapshot()
		if progress != h.progress {
			h.progress = progress
			h.recorder.update(func(job *jobstate.EnvironmentJob) {
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

// renewExclusiveClaim also records the supervisor's pid on a claim taken before
// it existed. A lost renewal is ignored rather than killing the job's work.
func (h *jobHeartbeat) renewExclusiveClaim(job jobstate.EnvironmentJob, name string) {
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

func (h *jobHeartbeat) leaseInterval() time.Duration {
	interval := h.ttl / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	return interval
}
