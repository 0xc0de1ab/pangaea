ARG GO_IMAGE=golang:1.25-alpine

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pangaeactl ./cmd/pangaeactl

FROM debian:bookworm-slim AS cursor-install
RUN apt-get update \
	&& apt-get install -y --no-install-recommends curl ca-certificates bash \
	&& rm -rf /var/lib/apt/lists/* \
	&& curl https://cursor.com/install -fsS | bash \
	&& mkdir -p /export/bin /export/share \
	&& cp -a /root/.local/bin/cursor-agent /export/bin/cursor-agent \
	&& cp -a /root/.local/share/cursor-agent /export/share/cursor-agent

FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates tini git \
	&& rm -rf /var/lib/apt/lists/*

RUN groupadd -g 10001 pangaea \
	&& useradd -m -u 10001 -g pangaea -d /home/pangaea pangaea \
	&& mkdir -p /var/lib/pangaea/auth/cursor /var/lib/pangaea/home/cursor /run/pangaea /work \
	&& chown -R pangaea:pangaea /var/lib/pangaea /run/pangaea /work /home/pangaea

COPY --from=cursor-install /export/share/cursor-agent /home/pangaea/.local/share/cursor-agent
RUN mkdir -p /home/pangaea/.local/bin \
	&& cursor_agent="$(find /home/pangaea/.local/share/cursor-agent/versions -maxdepth 2 -type f -name cursor-agent | sort | tail -n 1)" \
	&& test -n "${cursor_agent}" \
	&& chmod 0755 "${cursor_agent}" "$(dirname "${cursor_agent}")/node" \
	&& ln -sf "${cursor_agent}" /home/pangaea/.local/bin/cursor-agent \
	&& ln -sf cursor-agent /home/pangaea/.local/bin/agent \
	&& chown -R pangaea:pangaea /home/pangaea/.local

COPY --from=build /out/pangaeactl /usr/local/bin/pangaeactl
COPY providers/cursor/provider-entrypoint.sh /usr/local/bin/provider-entrypoint
RUN chmod 0755 /usr/local/bin/provider-entrypoint /usr/local/bin/pangaeactl

ENV PANGAEA_SHIM_MODE=cli-container \
	PANGAEA_SERVICE=cursor \
	PANGAEA_PROVIDER_MODE=acp \
	PANGAEA_AUTH_FORMAT=cursor-auth-json-format \
	PANGAEA_UPSTREAM_DIALECT=openai \
	PANGAEA_CURSOR_AGENT_EXE=/home/pangaea/.local/bin/agent \
	HOME=/var/lib/pangaea/home/cursor \
	PATH=/home/pangaea/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

USER pangaea:pangaea
WORKDIR /work
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/provider-entrypoint"]
