# Tech Debt — Phase 2 Integration

Locked decision at Phase 2 integration: the tracks' tested stdlib
equivalents stay as-is. The following production-grade replacements
were deliberately NOT added to `go.mod`:

- `river` (job queue) — replaced by in-process scheduler runner in
  `internal/adapter/scheduler` + `usecase/job` service.
- `goose` (migrations) — replaced by migrate runner in `cmd/manager`.
- `prometheus` client — replaced by metrics exposition in the http root.
- `go-oidc` — replaced by OIDC RP in `internal/adapter/oidc`.
- `aws-sdk-go` — replaced by stdlib SigV4 S3 client in
  `internal/adapter/s3`.

Revisit when durability, multi-replica operation, or richer provider
coverage is required.
