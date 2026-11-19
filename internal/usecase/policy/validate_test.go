package policy

import (
	"errors"
	"testing"

	"github.com/vaultix/vaultix/internal/domain"
)

// TestValidateTargetMatrix covers all 8 target types: one valid case plus
// every invalid shape per type.
func TestValidateTargetMatrix(t *testing.T) {
	valid := map[domain.TargetType]TargetInput{
		domain.TargetHostFilesystem: {
			Type: domain.TargetHostFilesystem, Selector: "/var/lib/app",
			Include: []string{"*.db", "data/**"}, Exclude: []string{"*.tmp"},
			Options: map[string]string{"symlink": "record", "one_file_system": "true"},
		},
		domain.TargetDockerConfig: {
			Type: domain.TargetDockerConfig, Selector: "webapp",
			Options: map[string]string{"include_compose": "true"},
		},
		domain.TargetDockerVolume: {
			Type: domain.TargetDockerVolume, Selector: "pgdata",
		},
		domain.TargetPostgres: {
			Type: domain.TargetPostgres, Selector: "appdb",
			CredentialRef: "sek_pg", Options: map[string]string{"scope": "database"},
		},
		domain.TargetMySQL: {
			Type: domain.TargetMySQL, Selector: "shop",
			CredentialRef: "sek_my",
		},
		domain.TargetMSSQL: {
			Type: domain.TargetMSSQL, Selector: "ledger",
			CredentialRef: "sek_ms", Options: map[string]string{"backup_type": "full"},
		},
		domain.TargetRedis: {
			Type: domain.TargetRedis, Selector: "cache-01",
			Options: map[string]string{"persistence": "both", "verify": "true"},
		},
		domain.TargetAppStack: {
			Type: domain.TargetAppStack, Selector: "stk_1",
		},
	}
	if len(valid) != 8 {
		t.Fatalf("matrix must cover 8 target types, got %d", len(valid))
	}
	for typ, in := range valid {
		if err := validateTarget(in); err != nil {
			t.Errorf("valid %s rejected: %v", typ, err)
		}
		// Explicit default method must also validate.
		withMethod := in
		withMethod.Method = defaultMethod(typ)
		if err := validateTarget(withMethod); err != nil {
			t.Errorf("valid %s with explicit method rejected: %v", typ, err)
		}
	}

	invalid := []struct {
		name string
		in   TargetInput
	}{
		{"unknown type", TargetInput{Type: "tape", Selector: "x"}},
		{"empty selector", TargetInput{Type: domain.TargetHostFilesystem}},
		{"blank selector", TargetInput{Type: domain.TargetDockerVolume, Selector: "  "}},

		// Host filesystem.
		{"host relative path", TargetInput{Type: domain.TargetHostFilesystem, Selector: "var/lib/app"}},
		{"host bad method", TargetInput{Type: domain.TargetHostFilesystem, Selector: "/x", Method: domain.BackupMethodLogicalDump}},
		{"host bad glob", TargetInput{Type: domain.TargetHostFilesystem, Selector: "/x", Include: []string{"[unclosed"}}},
		{"host blank glob", TargetInput{Type: domain.TargetHostFilesystem, Selector: "/x", Exclude: []string{"  "}}},
		{"host credential", TargetInput{Type: domain.TargetHostFilesystem, Selector: "/x", CredentialRef: "sek"}},
		{"host bad symlink opt", TargetInput{Type: domain.TargetHostFilesystem, Selector: "/x", Options: map[string]string{"symlink": "maybe"}}},
		{"host bad bool opt", TargetInput{Type: domain.TargetHostFilesystem, Selector: "/x", Options: map[string]string{"one_file_system": "yes"}}},

		// Docker config.
		{"docker cfg bad method", TargetInput{Type: domain.TargetDockerConfig, Selector: "c", Method: domain.BackupMethodNativeFull}},
		{"docker cfg patterns", TargetInput{Type: domain.TargetDockerConfig, Selector: "c", Include: []string{"*.y"}}},
		{"docker cfg credential", TargetInput{Type: domain.TargetDockerConfig, Selector: "c", CredentialRef: "sek"}},
		{"docker cfg bad opt", TargetInput{Type: domain.TargetDockerConfig, Selector: "c", Options: map[string]string{"include_compose": "1"}}},

		// Docker volume.
		{"docker vol bad method", TargetInput{Type: domain.TargetDockerVolume, Selector: "v", Method: domain.BackupMethodLogicalDump}},
		{"docker vol patterns", TargetInput{Type: domain.TargetDockerVolume, Selector: "v", Exclude: []string{"*.y"}}},
		{"docker vol credential", TargetInput{Type: domain.TargetDockerVolume, Selector: "v", CredentialRef: "sek"}},

		// Postgres.
		{"pg no credential", TargetInput{Type: domain.TargetPostgres, Selector: "appdb"}},
		{"pg blank credential", TargetInput{Type: domain.TargetPostgres, Selector: "appdb", CredentialRef: " "}},
		{"pg path selector", TargetInput{Type: domain.TargetPostgres, Selector: "/var/db", CredentialRef: "s"}},
		{"pg spaced selector", TargetInput{Type: domain.TargetPostgres, Selector: "my db", CredentialRef: "s"}},
		{"pg padded selector", TargetInput{Type: domain.TargetPostgres, Selector: " appdb", CredentialRef: "s"}},
		{"pg full method", TargetInput{Type: domain.TargetPostgres, Selector: "appdb", CredentialRef: "s", Method: domain.BackupMethodFull}},
		{"pg native method", TargetInput{Type: domain.TargetPostgres, Selector: "appdb", CredentialRef: "s", Method: domain.BackupMethodNativeFull}},
		{"pg patterns", TargetInput{Type: domain.TargetPostgres, Selector: "appdb", CredentialRef: "s", Include: []string{"*"}}},
		{"pg bad scope", TargetInput{Type: domain.TargetPostgres, Selector: "appdb", CredentialRef: "s", Options: map[string]string{"scope": "table"}}},

		// MySQL.
		{"mysql no credential", TargetInput{Type: domain.TargetMySQL, Selector: "shop"}},
		{"mysql full method", TargetInput{Type: domain.TargetMySQL, Selector: "shop", CredentialRef: "s", Method: domain.BackupMethodFull}},

		// MSSQL.
		{"mssql no credential", TargetInput{Type: domain.TargetMSSQL, Selector: "ledger"}},
		{"mssql logical method", TargetInput{Type: domain.TargetMSSQL, Selector: "ledger", CredentialRef: "s", Method: domain.BackupMethodLogicalDump}},
		{"mssql diff backup", TargetInput{Type: domain.TargetMSSQL, Selector: "ledger", CredentialRef: "s", Options: map[string]string{"backup_type": "differential"}}},
		{"mssql log backup", TargetInput{Type: domain.TargetMSSQL, Selector: "ledger", CredentialRef: "s", Options: map[string]string{"backup_type": "log"}}},

		// Redis.
		{"redis full method", TargetInput{Type: domain.TargetRedis, Selector: "c", Method: domain.BackupMethodFull}},
		{"redis bad persistence", TargetInput{Type: domain.TargetRedis, Selector: "c", Options: map[string]string{"persistence": "snapshot"}}},
		{"redis bad verify", TargetInput{Type: domain.TargetRedis, Selector: "c", Options: map[string]string{"verify": "yes"}}},
		{"redis patterns", TargetInput{Type: domain.TargetRedis, Selector: "c", Exclude: []string{"*"}}},

		// App stack.
		{"stack credential", TargetInput{Type: domain.TargetAppStack, Selector: "stk", CredentialRef: "sek"}},
		{"stack bad method", TargetInput{Type: domain.TargetAppStack, Selector: "stk", Method: domain.BackupMethodRDPSnapshot}},
		{"stack patterns", TargetInput{Type: domain.TargetAppStack, Selector: "stk", Include: []string{"*"}}},
	}
	for _, tc := range invalid {
		if err := validateTarget(tc.in); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", tc.name, err)
		}
	}
}

func TestValidateCompression(t *testing.T) {
	for _, c := range []domain.Compression{
		{}, {Algorithm: ""}, {Algorithm: "zstd"}, {Algorithm: "ZSTD", Level: 3}, {Level: 22},
	} {
		if err := validateCompression(c); err != nil {
			t.Errorf("compression %+v rejected: %v", c, err)
		}
	}
	for _, c := range []domain.Compression{
		{Algorithm: "gzip"}, {Algorithm: "none"}, {Level: -1}, {Level: 23},
	} {
		if err := validateCompression(c); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("compression %+v: want ErrValidation, got %v", c, err)
		}
	}
}
