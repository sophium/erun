ARG ERUN_VERSION=1.0.0

FROM ghcr.io/rihards-freimanis/erun-devops:${ERUN_VERSION}

LABEL org.opencontainers.image.source=https://github.com/sophium/erun

USER root

COPY erun-backend/erun-backend-db /opt/erun-backend-db
COPY --chmod=0755 erun-devops/docker/erun-backend-db/migrate.sh /usr/local/bin/erun-backend-db-migrate

RUN chown -R erun:erun /opt/erun-backend-db

USER erun
WORKDIR /opt/erun-backend-db

ENTRYPOINT ["erun-backend-db-migrate"]
