ARG GO_IMAGE=golang:1.25-alpine

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pangaeactl ./cmd/pangaeactl

FROM alpine:3.22

RUN apk add --no-cache bash ca-certificates tini \
  && existing_user="$(awk -F: '$3 == 1000 {print $1; exit}' /etc/passwd)" \
  && if [ -n "${existing_user}" ]; then deluser "${existing_user}" 2>/dev/null || true; fi \
  && existing_group="$(awk -F: '$3 == 1000 {print $1; exit}' /etc/group)" \
  && if [ -n "${existing_group}" ]; then delgroup "${existing_group}" 2>/dev/null || true; fi \
  && addgroup -g 1000 pangaea \
  && adduser -D -u 1000 -G pangaea -h /home/pangaea pangaea \
  && mkdir -p /run/pangaea /work \
  && chown -R 1000:1000 /run/pangaea /work /home/pangaea

COPY --from=build /out/pangaeactl /usr/local/bin/pangaeactl
COPY providers/api-compatible/provider-entrypoint.sh /usr/local/bin/provider-entrypoint
RUN chmod 0755 /usr/local/bin/provider-entrypoint /usr/local/bin/pangaeactl

ENV PANGAEA_SHIM_MODE=api-compatible \
    PANGAEA_SERVICE=openai \
    PANGAEA_UPSTREAM_DIALECT=openai

USER pangaea:pangaea
WORKDIR /work
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/provider-entrypoint"]
