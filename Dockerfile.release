# Debian 13.7 (trixie), image build 2026-09-18.
FROM debian:trixie-slim@sha256:a99cfc517144bc59b1978475ec53b46ecabec7e43635402ee5b77cc54cd1b20a
RUN apt-get update && apt-get install -y --no-install-recommends bash ca-certificates tini \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /workspace /home/agent /state \
    && chmod 1777 /state \
    && chown 10001:10001 /workspace /home/agent
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/unreal-agent-runner /usr/local/bin/unreal-agent-runner
COPY LICENSE /usr/share/doc/unreal-agent/LICENSE
ENV HOME=/home/agent SHELL=/bin/bash XDG_STATE_HOME=/state
USER 10001:10001
WORKDIR /workspace
ENTRYPOINT ["/usr/bin/tini", "--", "unreal-agent-runner"]
