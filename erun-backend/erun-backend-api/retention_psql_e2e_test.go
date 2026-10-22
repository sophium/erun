package backendapi

import (
	"context"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

// Retention policies are psql scripts (:dry_run, \if), so the retention
// suites run them through the real psql of a postgres:18.3 container, laid
// out the way the erun-backend-db image lays them out for retention.sh.
const (
	retentionPolicyDir           = "../erun-backend-db/retention"
	retentionScriptPath          = "../erun-backend-db/bin/retention.sh"
	retentionContainerPolicyDir  = "/opt/erun-backend-db/retention"
	retentionContainerEntrypoint = "/usr/local/bin/erun-backend-db-retention"
)

// retentionReportRow matches a "<table> | <count>" row of psql's aligned
// output, the shape of each policy's eligible_for_deletion report.
var retentionReportRow = regexp.MustCompile(`(?m)^\s*(\w+)\s*\|\s*(\d+)\s*$`)

// retentionPsql is a postgres:18.3 container used only for its psql client.
type retentionPsql struct {
	container testcontainers.Container
	// url reaches the database from inside the container.
	url string
}

// startRetentionPsql starts the psql container for databaseURL with the named
// policy files in retention.sh's retention/ directory and retention.sh as the
// image's entrypoint, so the entrypoint sweeps exactly those policies.
func startRetentionPsql(t *testing.T, ctx context.Context, databaseURL string, policies ...string) retentionPsql {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	mustNoErr(t, err, "parse the database URL")
	port, err := strconv.Atoi(parsed.Port())
	mustNoErr(t, err, "read the database port")
	inside := *parsed
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		inside.Host = net.JoinHostPort(testcontainers.HostInternal, parsed.Port())
	}

	script, err := filepath.Abs(retentionScriptPath)
	mustNoErr(t, err, "resolve the retention.sh path")
	files := []testcontainers.ContainerFile{{HostFilePath: script, ContainerFilePath: retentionContainerEntrypoint, FileMode: 0o755}}
	for _, policy := range policies {
		sqlFile, err := filepath.Abs(filepath.Join(retentionPolicyDir, policy))
		mustNoErr(t, err, "resolve the retention SQL path")
		files = append(files, testcontainers.ContainerFile{HostFilePath: sqlFile, ContainerFilePath: retentionPolicyPath(policy), FileMode: 0o644})
	}

	container, err := testcontainers.Run(ctx, "postgres:18.3",
		testcontainers.WithEntrypoint("sleep", "infinity"),
		testcontainers.WithHostPortAccess(port),
		testcontainers.WithFiles(files...),
	)
	testcontainers.CleanupContainer(t, container)
	mustNoErr(t, err, "start the psql container")
	return retentionPsql{container: container, url: inside.String()}
}

// retentionPolicyPath is a policy file's path inside the psql container.
func retentionPolicyPath(policy string) string {
	return retentionContainerPolicyDir + "/" + policy
}

// run executes cmd in the psql container and returns its combined output and
// exit code.
func (p retentionPsql) run(t *testing.T, ctx context.Context, env []string, cmd ...string) (string, int) {
	t.Helper()
	opts := []tcexec.ProcessOption{tcexec.Multiplexed()}
	if len(env) > 0 {
		opts = append(opts, tcexec.WithEnv(env))
	}
	code, reader, err := p.container.Exec(ctx, cmd, opts...)
	mustNoErr(t, err, "exec "+strings.Join(cmd, " "))
	var out []byte
	if reader != nil {
		out, err = io.ReadAll(reader)
		mustNoErr(t, err, "read output of "+cmd[0])
	}
	return string(out), code
}

// sweep runs retention.sh itself with ERUN_RETENTION_DRY_RUN set, failing the
// test unless it exits 0, and returns its output.
func (p retentionPsql) sweep(t *testing.T, ctx context.Context, dryRun bool) string {
	t.Helper()
	env := []string{"ERUN_DATABASE_URL=" + p.url, "ERUN_RETENTION_DRY_RUN=" + strconv.FormatBool(dryRun)}
	out, code := p.run(t, ctx, env, retentionContainerEntrypoint)
	if code != 0 {
		t.Fatalf("retention.sh (dry_run=%t) exited %d:\n%s", dryRun, code, out)
	}
	return out
}

// retentionReport parses the eligible_for_deletion rows from psql's output,
// keyed by table name.
func retentionReport(out string) map[string]string {
	report := map[string]string{}
	for _, m := range retentionReportRow.FindAllStringSubmatch(out, -1) {
		report[m[1]] = m[2]
	}
	return report
}
