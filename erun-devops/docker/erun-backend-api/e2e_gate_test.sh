#!/bin/sh

# The venue for erun-backend-api's opt-in ERUN_E2E_* database suites.
#
# Those suites are gated on a database URL in the environment and skip cleanly
# without one, which is what let them stay green-but-inert: the repository's own
# gate reached this module through the root Makefile's bare
# `go test -count=1 ./...`, no ERUN_E2E_* variable was ever set for it, and
# every database-backed case in it reported `SKIP` under a package line that
# still read `ok`. A `Regression-Test:` trailer naming one of those cases
# therefore named a test no gate ran.
#
# This script is the database. It starts a real postgres:18.3, applies
# erun-backend-db's real Atlas migrations, points every
# ERUN_E2E_*_DATABASE_URL the module reads at the result, and runs the module's
# suite -- so the suites that need a database execute rather than skip, and a
# failure in one of them exits nonzero.
#
# Two venues run it, and both are real:
#
#   1. In the component's own Dockerfile `test` stage, through a
#      `RUN --network=host` step with DOCKER_HOST pointing at the daemon erun's
#      builder already runs in. `erun build` grants that entitlement to exactly
#      this build -- one whose Dockerfile declares a `test` stage
#      (dockerBuildEntitlementArgs) -- so the stage can start its own container
#      fixture even though a `docker build` RUN step has no daemon of its own.
#      The `COPY --from=test` in the stage that follows is what makes it a
#      required part of the build graph: the image cannot be produced if this
#      failed.
#   2. By hand, via `make test-erun-backend-api-e2e` in an environment that
#      carries docker and the atlas CLI, the same way the erun-backend-db
#      end-to-end scripts are run.
#
# One database per *package*, not one for the module. Most of these suites seed
# their own tenants and are mutually isolated, but the platform-bootstrap ones
# are not and cannot be: they enrol the platform tenant and then assert on the
# database's whole bootstrap state (internal/repository/identity_e2e_test.go,
# cmd/eapi/main_e2e_test.go's "tenants=N users=M issuers=K"), which a sibling
# package's leftovers make wrong. Measured on this tree: the root package is
# green in one shared database, internal/repository's identity suite and
# cmd/eapi's bootstrap suite are not, and each of the two is green alone.
# Handing every package its own database is what makes the whole module
# runnable in one gate, and it is what a developer running one suite by hand
# gets. A migrated template database is what keeps that cheap -- each package's
# database is created from it rather than re-migrated.
#
# The variable set is derived from the module's own test sources rather than
# listed here, so a database-backed suite added later is covered by this gate
# the first time it lands instead of waiting for someone to remember this file.
# The suites whose real gate is a separate flag -- ERUN_E2E_PROVISION,
# ERUN_E2E_ENV_DEPLOY, ERUN_E2E_PROVISIONER_RBAC -- need a cluster, DBOS or a
# cloud provider on top of a database, not a database instead of one. They stay
# skipped here because their own flag is never set, which is deliberate: this
# script supplies a database, not the rest of the world.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "${script_dir}/../../.." && pwd)"
api_module="${repo_root}/erun-backend/erun-backend-api"
db_module="${repo_root}/erun-backend/erun-backend-db"

# 15433, not the erun-backend-db scripts' 15432: those run against this same
# daemon, and a gate that ran both at once must not have them collide.
port=15433
container="erun-backend-api-e2e-$$"
volume="erun-backend-api-e2e-$$"
template_db="erun_e2e_template"
result_dir="$(mktemp -d)"

cleanup() {
    docker rm -f "${container}" >/dev/null 2>&1 || true
    docker volume rm "${volume}" >/dev/null 2>&1 || true
    rm -rf "${result_dir}"
}
trap cleanup EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

# Checked rather than assumed: without a client, a migrator or a toolchain this
# script cannot stand up the database it exists to provide, and a silent skip
# here is the exact failure mode it was written to remove.
command -v docker >/dev/null 2>&1 || fail "docker is required (the suites this gate runs need a real PostgreSQL)"
command -v atlas >/dev/null 2>&1 || fail "atlas is required to apply erun-backend-db's migrations"
command -v go >/dev/null 2>&1 || fail "go is required to run the erun-backend-api suite"

# The official postgres image's first boot runs a temporary server to run init
# scripts, stops it, then starts the real server -- both print "database system
# is ready to accept connections", so pg_isready alone can catch the temporary
# server's brief window and report ready moments before it shuts down for the
# real restart, resetting any connection made in that gap. Waiting for the line
# to appear a given number of times in the container's own log is the reliable
# signal (the same one erun-backend-db/migrate_test.sh uses).
wait_for_log_count() {
    target="$1"
    i=0
    while [ "$i" -lt 60 ]; do
        count="$(docker logs "${container}" 2>&1 | grep -c 'database system is ready to accept connections')"
        [ "${count}" -ge "${target}" ] && return 0
        i=$((i + 1))
        sleep 1
    done
    return 1
}

# psql_in <database> <psql args...>: the server's own client, inside the
# container, so the fixture needs no postgres client of its own. Only used for
# CREATE DATABASE -- the suites themselves connect with pgx over the published
# port.
psql_in() {
    psql_db="$1"
    shift
    docker exec -e PGPASSWORD=testpass "${container}" psql -v ON_ERROR_STOP=1 -U erun -d "${psql_db}" "$@"
}

# migrate_database <database>: applies erun-backend-db's real migrations with
# the real Atlas CLI -- the same command erun-devops/k8s's migration Job and
# its repair CronJob run, against the same directory the erun-backend-db image
# bakes. The suites are exercised against the schema a deployed API actually
# serves, not against a hand-built approximation of it.
migrate_database() {
    migrate_db="$1"
    (cd "${db_module}" && ERUN_DATABASE_URL="postgres://erun:testpass@127.0.0.1:${port}/${migrate_db}?sslmode=disable" sh -c '
        set -eu
        if ! atlas migrate apply --env default --url "${ERUN_DATABASE_URL}" 2>/tmp/erun-backend-api-e2e-apply.log; then
            cat /tmp/erun-backend-api-e2e-apply.log >&2
            if grep -q "connected database is not clean" /tmp/erun-backend-api-e2e-apply.log; then
                atlas migrate set "${ERUN_DATABASE_BASELINE_VERSION:-20260503143000}" --env default --url "${ERUN_DATABASE_URL}"
                atlas migrate apply --env default --url "${ERUN_DATABASE_URL}"
            else
                exit 1
            fi
        fi
    ')
}

echo ">> starting postgres:18.3 for the erun-backend-api opt-in suites"
docker run -d --name "${container}" \
    -e POSTGRES_DB=erun -e POSTGRES_USER=erun -e POSTGRES_PASSWORD=testpass \
    -e PGDATA=/var/lib/postgresql/data/pgdata \
    -p "127.0.0.1:${port}:5432" \
    -v "${volume}:/var/lib/postgresql/data" \
    postgres:18.3 >/dev/null

wait_for_log_count 2 || fail "postgres did not become ready"

echo ">> applying erun-backend-db migrations to the template database"
psql_in postgres -c "CREATE DATABASE ${template_db} OWNER erun;"
migrate_database "${template_db}"

# Derived, not listed: every ERUN_E2E_*_DATABASE_URL the module's tests read is
# pointed at a migrated database, so a suite added later is covered when it
# lands.
e2e_vars="$(grep -rhoE 'ERUN_E2E_[A-Z0-9_]*_DATABASE_URL' --include='*_test.go' "${api_module}" | sort -u)"
[ -n "${e2e_vars}" ] ||
    fail "found no ERUN_E2E_*_DATABASE_URL in ${api_module}: this gate derives the suite's variables from the module's own test sources, so finding none means the derivation has gone stale and every database-backed case would skip unnoticed"

packages="$(cd "${api_module}" && go list ./...)" ||
    fail "go list ./... failed in ${api_module}"
[ -n "${packages}" ] || fail "go list ./... listed no packages in ${api_module}"

# -count=1 is load-bearing, the same reasoning as the Makefile's own
# test-erun-backend-api target: this module's Dockerfile mounts a persistent
# BuildKit go-build cache, so Go's test cache would otherwise survive across
# separate builds of different commits, and this module's tests read paths
# outside it at run time.
#
# -v is what makes the check below possible: without it `go test` prints a bare
# `ok <package>` for a package whose every case skipped, which is the shape this
# gate exists to eliminate. Each package's output goes to its own log so the
# counts can be read off it and a failure can be shown without burying the rest.
index=0
passed=0
skipped=0
for pkg in ${packages}; do
    index=$((index + 1))
    package_db="erun_e2e_p${index}"
    psql_in postgres -c "CREATE DATABASE ${package_db} OWNER erun TEMPLATE ${template_db};" >/dev/null

    package_url="postgres://erun:testpass@127.0.0.1:${port}/${package_db}?sslmode=disable"
    for e2e_var in ${e2e_vars}; do
        export "${e2e_var}=${package_url}"
    done

    package_log="${result_dir}/package-${index}.log"
    echo ">> go test ${pkg}"
    if ! (cd "${api_module}" && go test -v -count=1 "${pkg}") >"${package_log}" 2>&1; then
        # The failing cases first, then the tail: a package's own log runs to
        # thousands of lines, and a bare tail of it can miss the failure
        # entirely while still looking like an explanation.
        grep -A 8 -E '^[[:space:]]*--- FAIL' "${package_log}" >&2 || true
        tail -n 40 "${package_log}" >&2
        fail "${pkg} failed against a migrated PostgreSQL (its failing cases above)"
    fi

    # The suites are supposed to run here. A case that skipped for want of one
    # of the variables this script sets means the wiring above did not reach it,
    # and without this check the package would still report `ok`.
    for e2e_var in ${e2e_vars}; do
        if grep -q "opt-in: set ${e2e_var} " "${package_log}"; then
            grep -n "opt-in: set ${e2e_var} " "${package_log}" >&2
            fail "${pkg} skipped a case with ${e2e_var} set: the database this gate provides did not reach it"
        fi
    done

    passed=$((passed + $(grep -c '^--- PASS' "${package_log}" || true)))
    skipped=$((skipped + $(grep -c '^--- SKIP' "${package_log}" || true)))
done

# The skips are reported rather than described: every remaining opt-in skip
# names the flag it wants, so the shape of what this gate does not cover is
# read off the run instead of asserted by the line below it.
echo ">> remaining opt-in skips, by the flag each one wants:"
grep -ho 'opt-in: set ERUN_E2E_[A-Z0-9_]*' "${result_dir}"/package-*.log | sort | uniq -c |
    sed 's/^/   /'

echo "OK: erun-backend-api ran against a migrated PostgreSQL across ${index} package databases -- ${passed} passed, ${skipped} skipped"
