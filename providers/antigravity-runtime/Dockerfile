# syntax=docker/dockerfile:1.7
ARG GO_IMAGE=golang:1.25-bookworm
ARG RUNTIME_IMAGE=debian:bookworm-slim

FROM ${GO_IMAGE} AS proxy-build
WORKDIR /src
COPY providers/antigravity-runtime/compat-proxy/go.mod providers/antigravity-runtime/compat-proxy/go.sum ./
RUN go mod download
COPY providers/antigravity-runtime/compat-proxy/ .
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/antigravity-compat-proxy ./cmd/proxy

FROM ${RUNTIME_IMAGE}
WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    libasound2 \
    libatk-bridge2.0-0 \
    libatk1.0-0 \
    libatspi2.0-0 \
    libc6 \
    libcairo2 \
    libcups2 \
    libdbus-1-3 \
    libexpat1 \
    libgbm1 \
    libglib2.0-0 \
    libgtk-3-0 \
    libnspr4 \
    libnss3 \
    libpango-1.0-0 \
    libstdc++6 \
    libudev1 \
    libx11-6 \
    libxcb1 \
    libxcomposite1 \
    libxdamage1 \
    libxext6 \
    libxfixes3 \
    libxkbcommon0 \
    libxkbfile1 \
    libxrandr2 \
    procps \
    sqlite3 \
    tini \
    xdg-utils \
  && rm -rf /var/lib/apt/lists/*

COPY providers/antigravity-runtime/server-bundle/ /opt/antigravity-server/
COPY --from=proxy-build /out/antigravity-compat-proxy /usr/local/bin/antigravity-compat-proxy
COPY providers/antigravity-runtime/runtime-entrypoint.sh /usr/local/bin/antigravity-runtime-entrypoint

RUN chmod 0755 /usr/local/bin/antigravity-compat-proxy /usr/local/bin/antigravity-runtime-entrypoint \
  && if [ -f /opt/antigravity-server/node ]; then chmod +x /opt/antigravity-server/node; fi \
  && if [ -f /opt/antigravity-server/extensions/antigravity/bin/language_server_linux_x64 ]; then chmod +x /opt/antigravity-server/extensions/antigravity/bin/language_server_linux_x64; fi \
  && if [ -f /opt/antigravity-server/extensions/antigravity/bin/language_server_linux_arm64 ]; then chmod +x /opt/antigravity-server/extensions/antigravity/bin/language_server_linux_arm64; fi

ENV ANTIGRAVITY_SERVER_DIR=/opt/antigravity-server \
    SERVER_PATH=/opt/antigravity-server/out/server-main.js \
    INSTALL_DIR=/opt/antigravity-server \
    ANTIGRAVITY_GEMINI_DIR=/root/.antigravity-server \
    ANTIGRAVITY_APP_DATA_DIR=data \
    STATE_VSCDB_PATH=/var/lib/antigravity/state/User/globalStorage/state.vscdb \
    VSCDB_PATH=/var/lib/antigravity/state/User/globalStorage/state.vscdb \
    PATH="/opt/antigravity-server:${PATH}"

EXPOSE 8080

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/antigravity-runtime-entrypoint"]
CMD ["serve", "--proxy-addr", "0.0.0.0:8080"]
