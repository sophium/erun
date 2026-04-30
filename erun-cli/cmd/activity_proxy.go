package cmd

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newActivitySSHProxyCmd() *cobra.Command {
	var tenant string
	var environment string
	var listenAddress string
	var targetAddress string
	var idleTrafficBytes int64
	cmd := &cobra.Command{
		Use:    "ssh-proxy",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runActivitySSHProxy(activitySSHProxyParams{
				Tenant:           tenant,
				Environment:      environment,
				ListenAddress:    listenAddress,
				TargetAddress:    targetAddress,
				IdleTrafficBytes: idleTrafficBytes,
			})
		},
	}
	addActivityTargetFlags(cmd, &tenant, &environment)
	cmd.Flags().StringVar(&listenAddress, "listen", "", "Proxy listen address")
	cmd.Flags().StringVar(&targetAddress, "target", "", "Proxy target address")
	cmd.Flags().Int64Var(&idleTrafficBytes, "idle-traffic-bytes", 0, "Idle traffic threshold in bytes")
	return cmd
}

type activitySSHProxyParams struct {
	Tenant           string
	Environment      string
	ListenAddress    string
	TargetAddress    string
	IdleTrafficBytes int64
}

func runActivitySSHProxy(params activitySSHProxyParams) error {
	params.Tenant = strings.TrimSpace(params.Tenant)
	params.Environment = strings.TrimSpace(params.Environment)
	params.ListenAddress = strings.TrimSpace(params.ListenAddress)
	params.TargetAddress = strings.TrimSpace(params.TargetAddress)
	if params.Tenant == "" || params.Environment == "" {
		return fmt.Errorf("tenant and environment are required")
	}
	if params.ListenAddress == "" || params.TargetAddress == "" {
		return fmt.Errorf("listen and target addresses are required")
	}
	if params.IdleTrafficBytes < 0 {
		return fmt.Errorf("idle traffic threshold must not be negative")
	}

	listener, err := net.Listen("tcp", params.ListenAddress)
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
	}()

	recorder := &sshActivityRecorder{
		tenant:           params.Tenant,
		environment:      params.Environment,
		idleTrafficBytes: params.IdleTrafficBytes,
	}
	for {
		client, err := listener.Accept()
		if err != nil {
			return err
		}
		go proxySSHActivityConnection(client, params.TargetAddress, recorder)
	}
}

func proxySSHActivityConnection(client net.Conn, targetAddress string, recorder *sshActivityRecorder) {
	defer func() {
		_ = client.Close()
	}()
	target, err := net.Dial("tcp", targetAddress)
	if err != nil {
		return
	}
	defer func() {
		_ = target.Close()
	}()

	var wg sync.WaitGroup
	closeBoth := func() {
		_ = client.Close()
		_ = target.Close()
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer closeBoth()
		_, _ = io.Copy(&activityRecordingWriter{writer: target, recorder: recorder}, client)
	}()
	go func() {
		defer wg.Done()
		defer closeBoth()
		_, _ = io.Copy(&activityRecordingWriter{writer: client, recorder: recorder}, target)
	}()
	wg.Wait()
	recorder.Flush()
}

type activityRecordingWriter struct {
	writer   io.Writer
	recorder *sshActivityRecorder
}

func (w *activityRecordingWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	if n > 0 {
		w.recorder.Record(int64(n))
	}
	return n, err
}

type sshActivityRecorder struct {
	tenant           string
	environment      string
	idleTrafficBytes int64

	mu       sync.Mutex
	pending  int64
	lastSave time.Time
}

func (r *sshActivityRecorder) Record(bytes int64) {
	if bytes <= 0 {
		return
	}
	r.mu.Lock()
	r.pending += bytes
	if !r.lastSave.IsZero() && time.Since(r.lastSave) < time.Second {
		r.mu.Unlock()
		return
	}
	pending := r.pending
	r.pending = 0
	r.lastSave = time.Now()
	r.mu.Unlock()
	r.save(pending)
}

func (r *sshActivityRecorder) Flush() {
	r.mu.Lock()
	pending := r.pending
	r.pending = 0
	if pending > 0 {
		r.lastSave = time.Now()
	}
	r.mu.Unlock()
	r.save(pending)
}

func (r *sshActivityRecorder) save(bytes int64) {
	if bytes <= r.idleTrafficBytes {
		return
	}
	_ = common.RecordEnvironmentActivity(common.EnvironmentActivityParams{
		Tenant:      r.tenant,
		Environment: r.environment,
		Kind:        common.ActivityKindSSH,
		Bytes:       bytes,
	})
}
