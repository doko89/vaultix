# Vaultix manager dev image (referenced by deploy/docker-compose.yml).
# Release builds embed the web UI: run `npm --prefix web run build` first so
# web/dist/ exists for go:embed (TECH_SPEC section 7). Dev compose skips it.

FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# web/dist/ is git-ignored; release pipeline must generate it before this step.
RUN CGO_ENABLED=0 go build -o /out/manager ./cmd/manager

FROM debian:bookworm-slim
RUN useradd --system --no-log-init --create-home --home-dir /var/lib/vaultix vaultix
COPY --from=build /out/manager /usr/local/bin/manager
USER vaultix
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/manager"]
# Backend provides `serve` (TECH_SPEC section 7: `manager migrate`, `manager serve`).
CMD ["serve"]
