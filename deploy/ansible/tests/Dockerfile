FROM debian:12-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
       ansible-core nftables openssh-server python3 sudo systemd wireguard-tools \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /run/sshd \
    && systemd-machine-id-setup
ENV container=docker
STOPSIGNAL SIGRTMIN+3
WORKDIR /workspace/deploy/ansible
CMD ["/lib/systemd/systemd"]
