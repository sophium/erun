ARG ERUN_VERSION=1.0.0

# `runtime` takes the migrations from `test`, so no image exists unless the check passed.
FROM --platform=$BUILDPLATFORM alpine:3.20 AS build

ARG BUILDARCH
ARG ATLAS_VERSION=v1.2.0

RUN apk add --no-cache ca-certificates curl && \
    curl -fsSL -o /usr/local/bin/atlas "https://release.ariga.io/atlas/atlas-linux-${BUILDARCH}-${ATLAS_VERSION}" && \
    chmod 0755 /usr/local/bin/atlas

COPY erun-backend/erun-backend-db /opt/erun-backend-db

FROM build AS test

RUN atlas migrate validate --dir file:///opt/erun-backend-db/migrations/default

# Not pinned to BUILDPLATFORM: the runtime image runs this atlas; downloading costs no emulation.
FROM alpine:3.20 AS atlas-fetcher

ARG TARGETARCH
ARG ATLAS_VERSION=v1.2.0

RUN apk add --no-cache ca-certificates curl && \
    case "${TARGETARCH}" in \
        amd64) atlas_arch=amd64 ;; \
        arm64) atlas_arch=arm64 ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /atlas "https://release.ariga.io/atlas/atlas-linux-${atlas_arch}-${ATLAS_VERSION}" && \
    chmod 0755 /atlas

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates postgresql16-client && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun && \
    mkdir -p /home/erun && \
    chown -R erun:erun /home/erun

COPY --from=atlas-fetcher /atlas /usr/local/bin/atlas
COPY --from=test --chown=erun:erun /opt/erun-backend-db /opt/erun-backend-db
COPY --from=test --chmod=0755 /opt/erun-backend-db/bin/migrate.sh /usr/local/bin/erun-backend-db-migrate
COPY --from=test --chmod=0755 /opt/erun-backend-db/bin/retention.sh /usr/local/bin/erun-backend-db-retention

USER erun
WORKDIR /opt/erun-backend-db

ENTRYPOINT ["erun-backend-db-migrate"]
