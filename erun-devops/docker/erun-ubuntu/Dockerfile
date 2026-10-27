FROM ubuntu:noble-20261024.1

ENV LANG=en_US.utf8
ENV DEBIAN_FRONTEND=noninteractive
ENV TZ=Europe

# Wails/webkit CGO build deps live in this rarely rebuilt base, not in erun-devops.
# Dropping docker-clean lets the apt cache mounts keep packages; they never reach the layer.
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
