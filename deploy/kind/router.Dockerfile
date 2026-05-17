ARG GO_IMAGE=golang:1.25-alpine
ARG RUNTIME_IMAGE=alpine:3.22

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=kind
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pangaeactl ./cmd/pangaeactl

FROM ${RUNTIME_IMAGE}
RUN apk add --no-cache ca-certificates tzdata \
  && addgroup -g 1000 pangaea \
  && adduser -D -u 1000 -G pangaea -h /home/pangaea pangaea
COPY --from=build /out/pangaeactl /usr/local/bin/pangaeactl
RUN chmod 0755 /usr/local/bin/pangaeactl

USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/pangaeactl"]
