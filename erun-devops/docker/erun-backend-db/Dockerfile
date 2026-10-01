ARG ERUN_VERSION=1.0.0

# Ptah Compat retains the atlas command interface, including migration checksums.
FROM stokaro/ptah:0.11.4@sha256:98cc4799c9e368dc8495eddcb4389ec628cab52f9a369808b49ce26331628378 AS ptah

FROM alpine:3.20

RUN apk add --no-cache ca-certificates postgresql16-client && \
    addgroup -S erun && \
    adduser -S -G erun -h /home/erun erun && \
    mkdir -p /opt/erun-backend-db /home/erun && \
    chown -R erun:erun /opt/erun-backend-db /home/erun

COPY --from=ptah /usr/local/bin/ptah-compat /usr/local/bin/atlas
COPY --from=ptah /usr/share/licenses/ptah/LICENSE /usr/share/licenses/ptah/LICENSE
COPY erun-backend/erun-backend-db /opt/erun-backend-db
COPY --chmod=0755 erun-devops/docker/erun-backend-db/migrate.sh /usr/local/bin/erun-backend-db-migrate
COPY --chmod=0755 erun-devops/docker/erun-backend-db/retention.sh /usr/local/bin/erun-backend-db-retention

# Refuse to build an image whose baked migration directory is internally
# inconsistent (v1.0.247 shipped with an atlas.sum entry that did not match
# its own migration file, undetected until deploy time). This runs
# on every build of this image regardless of which gate ran beforehand, so
# the image itself cannot be produced -- and therefore cannot be published --
# from a directory `atlas migrate apply` would refuse at deploy time.
RUN atlas migrate validate --dir file:///opt/erun-backend-db/migrations/default

RUN chown -R erun:erun /opt/erun-backend-db

USER erun
WORKDIR /opt/erun-backend-db

ENTRYPOINT ["erun-backend-db-migrate"]
