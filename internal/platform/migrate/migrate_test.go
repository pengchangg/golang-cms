package migrate

import (
	"testing"
	"testing/fstest"
)

func TestLoadSortsAndChecksumsMigrations(t *testing.T) {
	files := fstest.MapFS{
		"000002_second.up.sql": {Data: []byte("SELECT 2;")},
		"000001_first.up.sql":  {Data: []byte("SELECT 1;")},
		"README.md":            {Data: []byte("ignored")},
	}
	migrations, err := Load(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 || migrations[0].Version != 1 || migrations[1].Version != 2 {
		t.Fatalf("unexpected migrations: %+v", migrations)
	}
	if migrations[0].Checksum == "" {
		t.Fatal("missing checksum")
	}
}

func TestLoadRejectsInvalidName(t *testing.T) {
	_, err := Load(fstest.MapFS{"1_bad.up.sql": {Data: []byte("SELECT 1;")}})
	if err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestLoadRejectsNonSnakeCaseName(t *testing.T) {
	_, err := Load(fstest.MapFS{"000001_Bad-Name.up.sql": {Data: []byte("SELECT 1;")}})
	if err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestLoadRejectsMissingFirstAndIntermediateVersions(t *testing.T) {
	for name, files := range map[string]fstest.MapFS{
		"missing first": {"000002_second.up.sql": {Data: []byte("SELECT 2;")}},
		"missing intermediate": {
			"000001_first.up.sql": {Data: []byte("SELECT 1;")},
			"000003_third.up.sql": {Data: []byte("SELECT 3;")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(files); err == nil {
				t.Fatal("Load() expected an error")
			}
		})
	}
}

func TestValidateAppliedRejectsUnknownVersion(t *testing.T) {
	err := validateApplied(map[uint64]appliedMigration{
		2: {name: "000002_unknown.up.sql", checksum: "checksum"},
	}, []Migration{{Version: 1, Name: "000001_first.up.sql", Checksum: "checksum"}})
	if err == nil {
		t.Fatal("validateApplied() expected an error")
	}
}

func TestValidateAppliedRejectsDirtyMigration(t *testing.T) {
	err := validateApplied(map[uint64]appliedMigration{
		1: {name: "000001_first.up.sql", checksum: "checksum", dirty: true},
	}, []Migration{{Version: 1, Name: "000001_first.up.sql", Checksum: "checksum"}})
	if err == nil {
		t.Fatal("validateApplied() expected an error")
	}
}

func TestValidateAppliedRejectsNonContiguousHistory(t *testing.T) {
	migrations := []Migration{
		{Version: 1, Name: "000001_first.up.sql", Checksum: "one"},
		{Version: 2, Name: "000002_second.up.sql", Checksum: "two"},
	}
	err := validateApplied(map[uint64]appliedMigration{
		2: {name: "000002_second.up.sql", checksum: "two"},
	}, migrations)
	if err == nil {
		t.Fatal("validateApplied() expected an error")
	}
}
