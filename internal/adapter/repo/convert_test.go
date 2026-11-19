package repo

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
)

func TestParseUUIDRoundTrip(t *testing.T) {
	u, err := parseUUID("6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944")
	require.NoError(t, err)
	require.True(t, u.Valid)
	require.Equal(t, "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", uuidString(u))
}

func TestParseUUIDAcceptsBareHex(t *testing.T) {
	u, err := parseUUID("6e4a83b01e2d4c3b8a5f9d0c1b2a3944")
	require.NoError(t, err)
	require.Equal(t, "6e4a83b0-1e2d-4c3b-8a5f-9d0c1b2a3944", uuidString(u))
}

func TestParseUUIDRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "not-a-uuid", "zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz", "6e4a83b0", "user-1"} {
		_, err := parseUUID(bad)
		require.ErrorIs(t, err, domain.ErrValidation, bad)
	}
}

func TestOptUUIDEmptyIsNull(t *testing.T) {
	u, err := optUUID("")
	require.NoError(t, err)
	require.False(t, u.Valid)
	require.Equal(t, "", uuidString(u))
}

func TestTextHelpers(t *testing.T) {
	require.False(t, textOrNull("").Valid)
	require.Equal(t, "", textOrEmpty(textOrNull("")))
	v := textOrNull("x")
	require.True(t, v.Valid)
	require.Equal(t, "x", textOrEmpty(v))
}

func TestInt4Helpers(t *testing.T) {
	require.False(t, int4OrNull(0).Valid)
	require.Equal(t, 0, intOrZero(int4OrNull(0)))
	v := int4OrNull(7)
	require.True(t, v.Valid)
	require.Equal(t, 7, intOrZero(v))
}

func TestTimestamptzHelpers(t *testing.T) {
	require.False(t, timestamptz(time.Time{}).Valid)
	require.False(t, timestamptzPtr(nil).Valid)
	now := time.Now().Truncate(time.Microsecond)
	require.True(t, timestamptz(now).Valid)
	require.True(t, timestamptzPtr(&now).Valid)
	require.True(t, timeOrZero(timestamptz(time.Time{})).IsZero())
	require.Nil(t, timePtr(timestamptz(time.Time{})))
	got := timePtr(timestamptz(now))
	require.NotNil(t, got)
	require.True(t, got.Equal(now))
}

func TestByteCounterHelpers(t *testing.T) {
	require.Equal(t, int64(0), uint64OrZero(nil))
	require.Nil(t, uint64Ptr(0))
	require.Nil(t, uint64Ptr(-5))
	n := uint64(42)
	require.Equal(t, int64(42), uint64OrZero(&n))
	got := uint64Ptr(42)
	require.NotNil(t, got)
	require.Equal(t, uint64(42), *got)
	huge := uint64(1) << 63
	require.Equal(t, int64(9223372036854775807), uint64OrZero(&huge))
}

func TestProgressHelpers(t *testing.T) {
	require.Equal(t, int32(0), progressToPct(nil))
	require.Nil(t, pctToProgress(0))
	half := 0.5
	require.Equal(t, int32(50), progressToPct(&half))
	got := pctToProgress(50)
	require.NotNil(t, got)
	require.InDelta(t, 0.5, *got, 1e-9)
	over := 99.0
	require.Equal(t, int32(100), progressToPct(&over))
	neg := -1.0
	require.Equal(t, int32(0), progressToPct(&neg))
}

func TestPageDefaults(t *testing.T) {
	limit, offset := page(domain.ListOptions{})
	require.Equal(t, int32(50), limit)
	require.Equal(t, int32(0), offset)
	limit, offset = page(domain.ListOptions{Limit: 10, Offset: 20})
	require.Equal(t, int32(10), limit)
	require.Equal(t, int32(20), offset)
	limit, offset = page(domain.ListOptions{Limit: 100000, Offset: -5})
	require.Equal(t, int32(500), limit)
	require.Equal(t, int32(0), offset)
}

func TestMapErrNoRows(t *testing.T) {
	require.ErrorIs(t, mapErr(pgx.ErrNoRows), domain.ErrNotFound)
	require.NoError(t, mapErr(nil))
}

func TestMapErrConstraintMapping(t *testing.T) {
	conflict := &pgconn.PgError{Code: "23505", ConstraintName: "users_email_key", Detail: "Key (email)=(secret@example.com) already exists."}
	err := mapErr(conflict)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.NotContains(t, err.Error(), "secret@example.com", "row values must not leak into errors")

	fk := &pgconn.PgError{Code: "23503", ConstraintName: "backup_jobs_agent_id_fkey"}
	require.ErrorIs(t, mapErr(fk), domain.ErrConflict)

	check := &pgconn.PgError{Code: "23514", ConstraintName: "chk_snapshots_state"}
	require.ErrorIs(t, mapErr(check), domain.ErrValidation)

	other := errors.New("boom")
	require.Equal(t, other, mapErr(other))
}

func TestStringSliceToleratesObjectDefault(t *testing.T) {
	require.Nil(t, stringSlice(nil))
	require.Nil(t, stringSlice([]byte("{}")))
	require.Equal(t, []string{"a", "b"}, stringSlice([]byte(`["a","b"]`)))
}

func TestStringMapToleratesNonObjects(t *testing.T) {
	require.Empty(t, stringMap(nil))
	require.Empty(t, stringMap([]byte("[]")))
	require.Equal(t, map[string]string{"k": "v"}, stringMap([]byte(`{"k":"v"}`)))
}

func TestDecodeJSONEmptyLeavesZero(t *testing.T) {
	var v struct{ A string }
	require.NoError(t, decodeJSON(nil, &v))
	require.NoError(t, decodeJSON([]byte(""), &v))
	require.Error(t, decodeJSON([]byte("{oops"), &v))
}
