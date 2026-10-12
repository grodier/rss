package migrations

import (
	"io/fs"
	"testing"
)

func TestMigrationsFS(t *testing.T) {
	if _, err := fs.Stat(FS, "00001_init.sql"); err != nil {
		t.Fatalf("FS doesn't contain 00001_init.sql: %v", err)
	}
}
