ARG ERUN_VERSION=1.0.0

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

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun && \
    mkdir -p /opt/erun-backend-db /home/erun && \
    chown -R erun:erun /opt/erun-backend-db /home/erun

COPY --from=atlas-fetcher /atlas /usr/local/bin/atlas
COPY erun-backend/erun-backend-db /opt/erun-backend-db
COPY --chmod=0755 erun-devops/docker/erun-backend-db/migrate.sh /usr/local/bin/erun-backend-db-migrate

RUN chown -R erun:erun /opt/erun-backend-db

USER erun
WORKDIR /opt/erun-backend-db

ENTRYPOINT ["erun-backend-db-migrate"]
