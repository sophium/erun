ARG ERUN_VERSION=1.0.0
# One atlas pin for both stages that fetch it, matching the erun-devops image's; each
# download is checked against its per-arch SHA-256 before use.
ARG ATLAS_VERSION=v1.3.5
ARG ATLAS_SHA256_AMD64=41d2a3c77fab370aead88b9a9faf2bab24d22e3e5728dc2dd7f87404adbce048
ARG ATLAS_SHA256_ARM64=bcdb097755995ef304a08635d38b65e9c2636489f45d0f7c04f2316bbd888ba9

# `runtime` takes the migrations from `test`, so no image exists unless the check passed.
FROM --platform=$BUILDPLATFORM alpine:3.24 AS build

ARG BUILDARCH
ARG ATLAS_VERSION
ARG ATLAS_SHA256_AMD64
ARG ATLAS_SHA256_ARM64

RUN apk add --no-cache ca-certificates curl && \
    case "${BUILDARCH}" in \
        amd64) atlas_sha256="${ATLAS_SHA256_AMD64}" ;; \
        arm64) atlas_sha256="${ATLAS_SHA256_ARM64}" ;; \
        *) echo "unsupported BUILDARCH: ${BUILDARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /tmp/atlas "https://release.ariga.io/atlas/atlas-linux-${BUILDARCH}-${ATLAS_VERSION}" && \
    echo "${atlas_sha256}  /tmp/atlas" | sha256sum -c - && \
    install -m 0755 /tmp/atlas /usr/local/bin/atlas && \
    rm /tmp/atlas

COPY erun-backend/erun-backend-db /opt/erun-backend-db

FROM build AS test

RUN atlas migrate validate --dir file:///opt/erun-backend-db/migrations/default

# Not pinned to BUILDPLATFORM: the runtime image runs this atlas; downloading costs no emulation.
FROM alpine:3.24 AS atlas-fetcher

ARG TARGETARCH
ARG ATLAS_VERSION
ARG ATLAS_SHA256_AMD64
ARG ATLAS_SHA256_ARM64

RUN apk add --no-cache ca-certificates curl && \
    case "${TARGETARCH}" in \
        amd64) atlas_arch=amd64; atlas_sha256="${ATLAS_SHA256_AMD64}" ;; \
        arm64) atlas_arch=arm64; atlas_sha256="${ATLAS_SHA256_ARM64}" ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac && \
    curl -fsSL -o /tmp/atlas "https://release.ariga.io/atlas/atlas-linux-${atlas_arch}-${ATLAS_VERSION}" && \
    echo "${atlas_sha256}  /tmp/atlas" | sha256sum -c - && \
    chmod 0755 /tmp/atlas && \
    mv /tmp/atlas /atlas

FROM alpine:3.24 AS runtime

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
