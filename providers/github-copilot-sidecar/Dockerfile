ARG GO_IMAGE=golang:1.25-alpine
ARG NODE_IMAGE=node:24-bookworm-slim

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pangaeactl ./cmd/pangaeactl

FROM ${NODE_IMAGE}
ARG COPILOT_NPM_PACKAGE=@github/copilot
ARG COPILOT_NPM_VERSION=latest
ARG COPILOT_SDK_NPM_PACKAGE=@github/copilot-sdk
ARG COPILOT_SDK_NPM_VERSION=latest

RUN apt-get update \
  && apt-get install -y --no-install-recommends bash ca-certificates git openssh-client tini \
  && rm -rf /var/lib/apt/lists/* \
  && npm install -g "${COPILOT_NPM_PACKAGE}@${COPILOT_NPM_VERSION}" \
  && mkdir -p /opt/copilot-relay \
  && npm install --prefix /opt/copilot-relay "${COPILOT_SDK_NPM_PACKAGE}@${COPILOT_SDK_NPM_VERSION}" \
  && groupadd -g 10001 pangaea \
  && useradd -m -u 10001 -g 10001 -d /home/pangaea -s /usr/sbin/nologin pangaea \
  && mkdir -p /var/lib/pangaea/home/copilot /run/pangaea /work \
  && chown -R 10001:10001 /var/lib/pangaea /run/pangaea /work /home/pangaea

COPY --from=build /out/pangaeactl /usr/local/bin/pangaeactl
COPY providers/github-copilot-sidecar/provider-entrypoint.sh /usr/local/bin/provider-entrypoint
COPY providers/github-copilot-sidecar/copilot-relay.mjs /usr/local/bin/copilot-relay
RUN chmod 0755 /usr/local/bin/provider-entrypoint /usr/local/bin/pangaeactl /usr/local/bin/copilot-relay

ENV HOME=/var/lib/pangaea/home/copilot \
    NODE_PATH=/opt/copilot-relay/node_modules \
    COPILOT_RELAY_NODE_MODULES=/opt/copilot-relay/node_modules \
    PANGAEA_SHIM_MODE=sidecar-agent \
    PANGAEA_SERVICE=github-copilot \
    PANGAEA_PROVIDER_MODE=sdk \
    PANGAEA_UPSTREAM_DIALECT=openai \
    PANGAEA_UPSTREAM_BASE_URL=http://127.0.0.1:4141 \
    PANGAEA_AUTH_FORMAT=github-copilot-config-json-format

USER 10001:10001
WORKDIR /work
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/provider-entrypoint"]
