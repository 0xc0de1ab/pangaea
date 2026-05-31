ARG GO_IMAGE=golang:1.25-alpine

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pangaeactl ./cmd/pangaeactl

FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends bash ca-certificates curl git openssh-client tini \
	&& rm -rf /var/lib/apt/lists/* \
	&& tmp_home="$(mktemp -d)" \
	&& HOME="${tmp_home}" GROK_BIN_DIR=/usr/local/bin bash -c 'curl -fsSL https://x.ai/cli/install.sh | bash' \
	&& cp -L /usr/local/bin/grok /usr/local/bin/grok.bin \
	&& mv /usr/local/bin/grok.bin /usr/local/bin/grok \
	&& ln -sf /usr/local/bin/grok /usr/local/bin/agent \
	&& chmod 0755 /usr/local/bin/grok \
	&& rm -rf "${tmp_home}"

RUN existing_user="$(awk -F: '$3 == 1000 {print $1; exit}' /etc/passwd)" \
	&& if [ -n "${existing_user}" ]; then userdel "${existing_user}" 2>/dev/null || true; fi \
	&& existing_group="$(awk -F: '$3 == 1000 {print $1; exit}' /etc/group)" \
	&& if [ -n "${existing_group}" ]; then groupdel "${existing_group}" 2>/dev/null || true; fi \
	&& groupadd -g 1000 pangaea \
	&& useradd -m -u 1000 -g 1000 -d /home/pangaea -s /usr/sbin/nologin pangaea \
	&& mkdir -p /var/lib/pangaea/home/grok /run/pangaea /work \
	&& chown -R pangaea:pangaea /var/lib/pangaea /run/pangaea /work /home/pangaea

COPY --from=build /out/pangaeactl /usr/local/bin/pangaeactl
COPY providers/grok-build/provider-entrypoint.sh /usr/local/bin/provider-entrypoint
RUN chmod 0755 /usr/local/bin/provider-entrypoint /usr/local/bin/pangaeactl

ENV PANGAEA_SHIM_MODE=cli-container \
	PANGAEA_SERVICE=grok-build \
	PANGAEA_PROVIDER_MODE=acp \
	PANGAEA_AUTH_FORMAT=grok-auth-json-format \
	PANGAEA_UPSTREAM_DIALECT=openai \
	PANGAEA_GROK_CLI_EXE=/usr/local/bin/grok \
	PANGAEA_MODEL=grok-build \
	PANGAEA_MODELS=grok-build=grok-build-default|grok-build-0.1|grok-default \
	HOME=/var/lib/pangaea/home/grok \
	PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

USER pangaea:pangaea
WORKDIR /work
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/provider-entrypoint"]
