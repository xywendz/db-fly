# syntax=docker/dockerfile:1
# db-fly B/S mode: Vue frontend (--mode web) is embedded into the Go binary.
# Build: docker build -t db-fly-server .
# Run:   docker run -d --name db-fly -p 8080:8080 \
#          -v db-fly-data:/data \
#          -e DB_FLY_WEB_TOKEN=change-me \
#          -e DB_FLY_MASTER_KEY=change-me-too \
#          db-fly-server
# DB_FLY_WEB_TOKEN is the signup invite code (auto-generated if unset on
# non-loopback listeners); open /login to register the first account.
# Set DB_FLY_ALLOW_SIGNUP=false afterwards to close public registration.

# ---- Stage 1: Web frontend template ----
FROM node:22-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build:web

# ---- Stage 2: Go binary with embedded frontend ----
FROM golang:1.26-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Overwrite the placeholder index.html and add hashed JS/CSS assets.
COPY --from=frontend /app/frontend/dist-web/ ./internal/webui/dist/
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X github.com/db-fly/db-fly/internal/bridge.Version=${VERSION}" \
    -o /out/db-fly-server ./cmd/db-fly-server

# ---- Stage 3: minimal runtime ----
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S dbfly \
    && adduser -S -G dbfly dbfly \
    && mkdir -p /data \
    && chown dbfly:dbfly /data
COPY --from=backend /out/db-fly-server /usr/local/bin/db-fly-server
USER dbfly
WORKDIR /app
ENV DB_FLY_WEB_ADDR=":8080" \
    DB_FLY_DATA_DIR="/data" \
    TZ="Asia/Shanghai"
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O - http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["db-fly-server"]
