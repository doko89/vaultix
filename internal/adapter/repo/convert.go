package repo

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vaultix/vaultix/internal/domain"
)

// Pagination defaults for list queries.
const (
	defaultPageLimit = 50
	maxPageLimit     = 500
)

// page converts ListOptions to sqlc limit/offset, applying sane defaults.
func page(opts domain.ListOptions) (limit, offset int32) {
	limit = saturatingInt32(opts.Limit)
	offset = saturatingInt32(opts.Offset)
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// mapErr converts persistence failures to domain sentinels. Constraint
// names (never row values) are included so messages stay secret-free.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("repo: no such row: %w", domain.ErrNotFound)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("repo: duplicate violates %s: %w", pgErr.ConstraintName, domain.ErrConflict)
		case "23503": // foreign_key_violation
			return fmt.Errorf("repo: dangling reference violates %s: %w", pgErr.ConstraintName, domain.ErrConflict)
		case "23514", // check_violation
			"23502", // not_null_violation
			"22P02": // invalid_text_representation
			return fmt.Errorf("repo: violates %s: %w", pgErr.ConstraintName, domain.ErrValidation)
		}
	}
	return err
}

// notFound builds a secret-free not-found error for an entity kind.
func notFound(kind string) error {
	return fmt.Errorf("repo: %s: %w", kind, domain.ErrNotFound)
}

// validationError builds a secret-free validation error. Never pass secret
// material (hashes, tokens, ciphertext) as detail.
func validationError(detail string) error {
	return fmt.Errorf("repo: %s: %w", detail, domain.ErrValidation)
}

// parseUUID parses a domain ID string into pgtype.UUID. Hyphens are
// optional; anything else is a validation failure (never NotFound: the
// caller passed a malformed identifier, not a missing row).
func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	clean := strings.ReplaceAll(s, "-", "")
	if len(clean) != 32 {
		return u, validationError("malformed id")
	}
	raw, err := hex.DecodeString(clean)
	if err != nil {
		return u, validationError("malformed id")
	}
	copy(u.Bytes[:], raw)
	u.Valid = true
	return u, nil
}

// mustParseUUID parses s and reports which reference was malformed.
func mustParseUUID(s, what string) (pgtype.UUID, error) {
	u, err := parseUUID(s)
	if err != nil {
		return u, validationError("malformed " + what)
	}
	return u, err
}

// optUUID maps "" to NULL and parses anything else.
func optUUID(s string) (pgtype.UUID, error) {
	if s == "" {
		return pgtype.UUID{}, nil
	}
	return parseUUID(s)
}

// mustOptUUID maps "" to NULL and reports which reference was malformed.
func mustOptUUID(s, what string) (pgtype.UUID, error) {
	if s == "" {
		return pgtype.UUID{}, nil
	}
	u, err := parseUUID(s)
	if err != nil {
		return u, validationError("malformed " + what)
	}
	return u, nil
}

// uuidString renders a pgtype.UUID as canonical text, or "" when NULL.
func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// timeOrZero maps NULL to the zero time.
func timeOrZero(v pgtype.Timestamptz) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return v.Time
}

// timePtr maps NULL to nil.
func timePtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

// timestamptz maps the zero time to NULL.
func timestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// timestamptzPtr maps nil to NULL.
func timestamptzPtr(p *time.Time) pgtype.Timestamptz {
	if p == nil || p.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *p, Valid: true}
}

// textOrEmpty maps NULL to "".
func textOrEmpty(v pgtype.Text) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

// textOrNull maps "" to NULL (all pgtype.Text parameters in this package
// target nullable columns).
func textOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// intOrZero maps NULL to 0.
func intOrZero(v pgtype.Int4) int {
	if !v.Valid {
		return 0
	}
	return int(v.Int32)
}

// int4OrNull maps 0 to NULL (0 means "unset" for every nullable int4 in
// this package: policy versions, keep_last_n/keep_days, interval minutes).
func int4OrNull(n int) pgtype.Int4 {
	if n == 0 {
		return pgtype.Int4{}
	}
	if n > math.MaxInt32 || n < math.MinInt32 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(n), Valid: true} //nolint:gosec // range-checked above
}

// saturatingInt32 converts an int without overflow.
func saturatingInt32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n) //nolint:gosec // range-checked above
}

// int32OrZero converts an int with clamping to int32 range.
func int32OrZero(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < 0 {
		return 0
	}
	return int32(n) //nolint:gosec // clamped above
}

// uint64OrZero maps nil to 0 for NOT NULL bigint byte counters.
func uint64OrZero(p *uint64) int64 {
	if p == nil {
		return 0
	}
	if *p > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(*p) //nolint:gosec // range-checked above
}

// uint64Ptr maps 0 to nil (0 means "unknown" for every byte counter read
// in this package, mirroring uint64OrZero on the way in).
func uint64Ptr(n int64) *uint64 {
	if n <= 0 {
		return nil
	}
	u := uint64(n) //nolint:gosec // guarded above
	return &u
}

// progressToPct maps 0..1 (nil = unknown) to 0..100, clamped.
func progressToPct(p *float64) int32 {
	if p == nil {
		return 0
	}
	pct := *p * 100
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return int32(math.Round(pct)) //nolint:gosec // clamped above
}

// pctToProgress maps 0 to nil (unknown) and anything else to 0..1.
func pctToProgress(pct int32) *float64 {
	if pct <= 0 {
		return nil
	}
	if pct > 100 {
		pct = 100
	}
	p := float64(pct) / 100
	return &p
}

// encodeJSON marshals v for a jsonb column.
func encodeJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("repo: encode json: %w", err)
	}
	return raw, nil
}

// mustEncodeJSON marshals v, falling back to "{}" when v cannot be
// represented (used only for maps/slices built by this package, where
// failure is impossible in practice).
func mustEncodeJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

// decodeJSON unmarshals a jsonb column into v. Empty/NULL input leaves v
// untouched so reads degrade to the zero value instead of failing.
func decodeJSON(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("repo: decode json: %w", err)
	}
	return nil
}

// stringMap decodes a jsonb object column into map[string]string, tolerating
// legacy non-object values as empty.
func stringMap(data []byte) map[string]string {
	out := map[string]string{}
	if len(data) == 0 {
		return out
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]string{}
	}
	return out
}

// stringSlice decodes a jsonb array column, tolerating legacy non-array
// values (the agents.capabilities default is '{}') as empty.
func stringSlice(data []byte) []string {
	var out []string
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}
