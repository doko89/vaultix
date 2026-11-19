# Vaultix manager release image (referenced by deploy/docker-compose.yml).
#
# Multi-stage: node builds the web UI (web/dist/), Go builds the manager,
# the runtime stage ships only the binary + migrations + built UI.
# Local equivalent: `make release-build`.
#
# Runtime layout is /app so the manager's defaults resolve without flags:
# migrations_dir "db/migrations" and static_dir "./web/dist" (cmd/manager).

# --- Stage 1: web UI -------------------------------------------------------
FROM node:22-bookworm-slim AS webbuild
WORKDIR /web
# Dependency layer first for cache reuse (package-lock.json is committed).
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# --- Stage 2: manager binary -----------------------------------------------
FROM golang:1.27-bookworm AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Release builds embed the UI from disk (./web/dist); the SPA handler 503s
# on UI routes when it is absent (API still works), so copy it in.
COPY --from=webbuild /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /out/manager ./cmd/manager

# --- Stage 3: runtime ------------------------------------------------------
FROM debian:bookworm-slim
# curl backs the compose healthcheck (GET /healthz).
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*
RUN useradd --system --no-log-init --create-home --home-dir /var/lib/vaultix vaultix
WORKDIR /app
COPY --from=gobuild /out/manager /usr/local/bin/manager
# Migrations ship with the image: `manager serve` auto-applies them on boot
# and `manager migrate up|down|status` works with no volume mounts.
COPY --from=gobuild /src/db/migrations ./db/migrations
COPY --from=webbuild /web/dist ./web/dist
RUN chown -R vaultix:vaultix /app
USER vaultix
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/manager"]
CMD ["serve"]
