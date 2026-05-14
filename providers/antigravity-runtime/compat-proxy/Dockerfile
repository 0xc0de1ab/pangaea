# Multi-stage Dockerfile for Antigravity Compat Proxy

# Stage 1: Build the Go Proxy (Statically Linked)
FROM golang:latest AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN apt-get update && apt-get install -y make && make linux-amd64-release

# Stage 2: Final Runtime Image (Minimal)
FROM debian:bookworm-slim
WORKDIR /app

# Install dependencies for Node.js and Antigravity (shared libs)
RUN apt-get update && apt-get install -y \
    ca-certificates \
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
    xdg-utils \
    curl \
    sqlite3 \
    procps \
    && rm -rf /var/lib/apt/lists/*

# Copy the server bundle from host (Ensure this is prepared before build)
COPY server-bundle/ /opt/antigravity-server/
RUN if [ -f /opt/antigravity-server/node ]; then chmod +x /opt/antigravity-server/node; fi
RUN if [ -f /opt/antigravity-server/extensions/antigravity/bin/language_server_linux_x64 ]; then chmod +x /opt/antigravity-server/extensions/antigravity/bin/language_server_linux_x64; fi

# Copy our Go proxy
COPY --from=builder /app/build/linux-amd64/release/antigravity-compat-proxy /usr/local/bin/
RUN existing_user="$(awk -F: '$3 == 1000 {print $1; exit}' /etc/passwd)" \
    && if [ -n "${existing_user}" ]; then userdel "${existing_user}" 2>/dev/null || true; fi \
    && existing_group="$(awk -F: '$3 == 1000 {print $1; exit}' /etc/group)" \
    && if [ -n "${existing_group}" ]; then groupdel "${existing_group}" 2>/dev/null || true; fi \
    && groupadd -g 1000 pangaea \
    && useradd -u 1000 -g 1000 -m -d /home/pangaea -s /usr/sbin/nologin pangaea \
    && mkdir -p /var/lib/antigravity/home/.antigravity-server/data/User/globalStorage /run/pangaea /work \
    && chown -R 1000:1000 /var/lib/antigravity /run/pangaea /work /home/pangaea

# Set environment variables
ENV ANTIGRAVITY_SERVER_DIR=/opt/antigravity-server
# For WSL users, the host state.vscdb is usually at:
# /mnt/c/Users/<USER>/AppData/Roaming/Antigravity/User/globalStorage/state.vscdb
ENV HOME=/home/pangaea
ENV VSCDB_PATH=/var/lib/antigravity/home/.antigravity-server/data/User/globalStorage/state.vscdb
ENV PATH="/opt/antigravity-server:${PATH}"
ENV SERVER_PATH=/opt/antigravity-server/out/server-main.js
ENV CORE_PATH=/opt/antigravity-server/extensions/antigravity/bin/language_server_linux_x64

EXPOSE 8080

USER pangaea:pangaea

ENTRYPOINT ["antigravity-compat-proxy"]
CMD ["serve", "--proxy-addr", ":8080"]
