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
ARG GEMINI_NPM_PACKAGE=@google/gemini-cli
ARG GEMINI_NPM_VERSION=latest

RUN apk add --no-cache bash ca-certificates git openssh-client tini \
  && npm install -g "${GEMINI_NPM_PACKAGE}@${GEMINI_NPM_VERSION}" \
  && addgroup -g 10001 pangaea \
  && adduser -D -u 10001 -G pangaea -h /home/pangaea pangaea \
  && mkdir -p /var/lib/pangaea/home/gemini/.gemini /run/pangaea /work \
  && chown -R 10001:10001 /var/lib/pangaea /run/pangaea /work /home/pangaea

COPY --from=build /out/pangaeactl /usr/local/bin/pangaeactl
COPY providers/gemini/provider-entrypoint.sh /usr/local/bin/provider-entrypoint
RUN chmod 0755 /usr/local/bin/provider-entrypoint /usr/local/bin/pangaeactl

ENV HOME=/var/lib/pangaea/home/gemini \
    GEMINI_CLI_TRUST_WORKSPACE=true \
    PANGAEA_SHIM_MODE=cli-container \
    PANGAEA_SERVICE=gemini \
    PANGAEA_UPSTREAM_ADAPTER=cli-oneshot \
    PANGAEA_UPSTREAM_DIALECT=gemini \
    PANGAEA_MODEL=gemini-2.5-flash \
    PANGAEA_MODEL_ALIAS=gemini-default

USER 10001:10001
WORKDIR /work
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/provider-entrypoint"]
