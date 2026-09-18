FROM ubuntu:noble-20260217

ENV LANG=en_US.utf8
ENV DEBIAN_FRONTEND=noninteractive
ENV TZ=Europe

# build-essential/pkg-config/libgtk-3-dev/libwebkit2gtk-4.1-dev/libsoup-3.0-dev
# (Wails/webkit CGO build deps) and dtach live here, not in erun-devops's own
# Dockerfile: they are stable, rarely-changing OS libraries, and erun-devops's
# test stage and final stage were each apt-get installing this same set on
# every build (measured at 247s + 145s of a 7m50s erun-devops build). Baking
# them into this base, which is rebuilt far less often, pays that cost once
# instead of twice per build.
#
# Removing the docker-clean apt hook and setting Keep-Downloaded-Packages is
# what makes the cache mounts below actually retain .deb archives and package
# lists across builds -- Docker's stock apt.conf.d/docker-clean hook purges
# both after every apt-get invocation specifically to keep container images
# small, which defeats a cache mount's whole purpose. Safe here because
# nothing under /var/cache/apt or /var/lib/apt/lists is a cache-mount target
# ever lands in the committed image layer regardless of this setting.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists \
    rm -f /etc/apt/apt.conf.d/docker-clean && \
    echo 'Binary::apt::APT::Keep-Downloaded-Packages "true";' > /etc/apt/apt.conf.d/keep-cache && \
    apt-get update && \
    apt-get install -y less zip apt-transport-https tzdata curl unzip jq sudo wget gnupg software-properties-common locales ca-certificates dnsutils iputils-ping openssh-server nano \
        dtach build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev && \
    localedef -i en_US -c -f UTF-8 -A /usr/share/locale/locale.alias en_US.UTF-8 && \
    if ! id -u ubuntu >/dev/null 2>&1; then useradd -ms /bin/bash ubuntu; fi && \
    install -d -o ubuntu -g ubuntu /home/ubuntu && \
    usermod -aG sudo ubuntu && \
    echo "ubuntu ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/ubuntu && \
    chmod 0440 /etc/sudoers.d/ubuntu

USER ubuntu
WORKDIR /home/ubuntu
