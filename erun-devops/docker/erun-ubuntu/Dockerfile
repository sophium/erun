FROM ubuntu:noble-20260217

LABEL org.opencontainers.image.source=https://github.com/sophium/erun

ENV LANG=en_US.utf8
ENV DEBIAN_FRONTEND=noninteractive
ENV TZ=Europe

RUN apt-get update && \
    apt-get install -y less zip apt-transport-https tzdata curl unzip jq sudo wget gnupg software-properties-common locales ca-certificates dnsutils iputils-ping openssh-server nano && \
    localedef -i en_US -c -f UTF-8 -A /usr/share/locale/locale.alias en_US.UTF-8 && \
    if ! id -u ubuntu >/dev/null 2>&1; then useradd -ms /bin/bash ubuntu; fi && \
    install -d -o ubuntu -g ubuntu /home/ubuntu && \
    usermod -aG sudo ubuntu && \
    echo "ubuntu ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/ubuntu && \
    chmod 0440 /etc/sudoers.d/ubuntu

USER ubuntu
WORKDIR /home/ubuntu
