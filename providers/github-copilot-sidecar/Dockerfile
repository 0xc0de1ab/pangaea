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

RUN apk add --no-cache bash ca-certificates git openssh-client tini \
  && addgroup -g 10001 pangaea \
  && adduser -D -u 10001 -G pangaea -h /home/pangaea pangaea \
  && mkdir -p /var/lib/pangaea/home/copilot /run/pangaea /work \
  && chown -R 10001:10001 /var/lib/pangaea /run/pangaea /work /home/pangaea

COPY --from=build /out/pangaeactl /usr/local/bin/pangaeactl
COPY providers/github-copilot-sidecar/provider-entrypoint.sh /usr/local/bin/provider-entrypoint
RUN chmod 0755 /usr/local/bin/provider-entrypoint /usr/local/bin/pangaeactl

ENV HOME=/var/lib/pangaea/home/copilot \
    PANGAEA_SHIM_MODE=sidecar-agent \
    PANGAEA_SERVICE=github-copilot \
    PANGAEA_UPSTREAM_DIALECT=openai \
    PANGAEA_UPSTREAM_BASE_URL=http://127.0.0.1:4141 \
    PANGAEA_MODEL=github-copilot-default \
    PANGAEA_MODEL_ALIAS=copilot-default

USER 10001:10001
WORKDIR /work
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/provider-entrypoint"]
