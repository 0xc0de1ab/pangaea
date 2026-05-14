ARG GO_IMAGE=golang:1.25-alpine
ARG NODE_IMAGE=node:23-alpine

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pangaeactl ./cmd/pangaeactl

FROM ${NODE_IMAGE}
ARG CLAUDE_NPM_PACKAGE=@anthropic-ai/claude-code
ARG CLAUDE_NPM_VERSION=latest

RUN apk add --no-cache bash ca-certificates git openssh-client tini \
  && npm install -g "${CLAUDE_NPM_PACKAGE}@${CLAUDE_NPM_VERSION}" \
  && existing_user="$(awk -F: '$3 == 1000 {print $1; exit}' /etc/passwd)" \
  && if [ -n "${existing_user}" ]; then deluser "${existing_user}" 2>/dev/null || true; fi \
  && existing_group="$(awk -F: '$3 == 1000 {print $1; exit}' /etc/group)" \
  && if [ -n "${existing_group}" ]; then delgroup "${existing_group}" 2>/dev/null || true; fi \
  && addgroup -g 1000 pangaea \
  && adduser -D -u 1000 -G pangaea -h /home/pangaea pangaea \
  && mkdir -p /var/lib/pangaea/auth/claude /run/pangaea /work \
  && chown -R 1000:1000 /var/lib/pangaea /run/pangaea /work /home/pangaea

COPY --from=build /out/pangaeactl /usr/local/bin/pangaeactl
COPY providers/claude/provider-entrypoint.sh /usr/local/bin/provider-entrypoint
RUN chmod 0755 /usr/local/bin/provider-entrypoint /usr/local/bin/pangaeactl

ENV CLAUDE_CONFIG_DIR=/var/lib/pangaea/auth/claude \
    PANGAEA_SHIM_MODE=cli-container \
    PANGAEA_SERVICE=claude \
    PANGAEA_PROVIDER_MODE=cli-adapter \
    PANGAEA_UPSTREAM_DIALECT=anthropic \
    PANGAEA_MODEL=claude-default \
    PANGAEA_MODEL_ALIAS=claude-default

USER pangaea:pangaea
WORKDIR /work
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/provider-entrypoint"]
