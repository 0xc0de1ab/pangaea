ARG GO_IMAGE=golang:1.25-bookworm
ARG RUNTIME_IMAGE=debian:bookworm-slim

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pangaeactl ./cmd/pangaeactl

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

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
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
    openssh-client \
    procps \
    sqlite3 \
    tini \
    xdg-utils \
  && rm -rf /var/lib/apt/lists/* \
  && existing_user="$(awk -F: '$3 == 1000 {print $1; exit}' /etc/passwd)" \
  && if [ -n "${existing_user}" ]; then userdel "${existing_user}" 2>/dev/null || true; fi \
  && existing_group="$(awk -F: '$3 == 1000 {print $1; exit}' /etc/group)" \
  && if [ -n "${existing_group}" ]; then groupdel "${existing_group}" 2>/dev/null || true; fi \
  && groupadd -g 1000 pangaea \
  && useradd -m -u 1000 -g 1000 -d /home/pangaea -s /usr/sbin/nologin pangaea \
  && mkdir -p /var/lib/pangaea/home/antigravity /var/lib/antigravity/state/User/globalStorage /run/pangaea /work /opt/antigravity-server \
  && chown -R 1000:1000 /var/lib/pangaea /var/lib/antigravity /run/pangaea /work /home/pangaea /opt/antigravity-server

COPY providers/antigravity-runtime/server-bundle/ /opt/antigravity-server/
COPY --from=build /out/pangaeactl /usr/local/bin/pangaeactl
COPY --from=proxy-build /out/antigravity-compat-proxy /usr/local/bin/antigravity-compat-proxy
COPY providers/antigravity-sidecar/provider-entrypoint.sh /usr/local/bin/provider-entrypoint
RUN chmod 0755 /usr/local/bin/provider-entrypoint /usr/local/bin/pangaeactl /usr/local/bin/antigravity-compat-proxy \
  && if [ -f /opt/antigravity-server/node ]; then chmod +x /opt/antigravity-server/node; fi \
  && if [ -f /opt/antigravity-server/extensions/antigravity/bin/language_server_linux_x64 ]; then chmod +x /opt/antigravity-server/extensions/antigravity/bin/language_server_linux_x64; fi \
  && if [ -f /opt/antigravity-server/extensions/antigravity/bin/language_server_linux_arm64 ]; then chmod +x /opt/antigravity-server/extensions/antigravity/bin/language_server_linux_arm64; fi

ENV HOME=/var/lib/pangaea/home/antigravity \
    ANTIGRAVITY_SERVER_DIR=/opt/antigravity-server \
    SERVER_PATH=/opt/antigravity-server/out/server-main.js \
    INSTALL_DIR=/opt/antigravity-server \
    ANTIGRAVITY_GEMINI_DIR=/var/lib/antigravity/state \
    ANTIGRAVITY_APP_DATA_DIR=. \
    STATE_VSCDB_PATH=/var/lib/antigravity/state/User/globalStorage/state.vscdb \
    VSCDB_PATH=/var/lib/antigravity/state/User/globalStorage/state.vscdb \
    PANGAEA_SHIM_MODE=sidecar-agent \
    PANGAEA_SERVICE=antigravity \
    PANGAEA_UPSTREAM_DIALECT=openai \
    PANGAEA_UPSTREAM_BASE_URL=http://127.0.0.1:8080 \
    PANGAEA_SHIM_PROTOCOLS=openai,anthropic,gemini \
    PANGAEA_MODEL=antigravity-default \
    PANGAEA_MODEL_ALIAS=antigravity-default \
    PATH="/opt/antigravity-server:${PATH}"

USER pangaea:pangaea
WORKDIR /work
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/provider-entrypoint"]
