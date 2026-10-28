package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaInitializationAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version=%d error=%v", version, err)
	}
	if _, err := s.db.Exec(`INSERT INTO auth_credentials(id,token_hash,subject,issuer,kind,name,expires_at,created_at,last_used_at) VALUES('test','hash','alice','issuer','token','test',1,1,1)`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var subject string
	if err := s.db.QueryRow(`SELECT subject FROM auth_credentials WHERE id='test'`).Scan(&subject); err != nil || subject != "alice" {
		t.Fatalf("subject=%q error=%v", subject, err)
	}
}

func TestUnsupportedSchemaIsPreserved(t *testing.T) {
	for _, version := range []int{0, 1, 3, 4, 5, 6, 7, 8, schemaVersion + 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(fmt.Sprintf(`CREATE TABLE original(value TEXT); INSERT INTO original VALUES('preserved'); PRAGMA user_version=%d`, version)); err != nil {
				t.Fatal(err)
			}
			s, err := Open(path)
			if err == nil {
				s.Close()
				t.Fatal("unsupported database was accepted")
			}
			if !strings.Contains(err.Error(), "use a new state_path") {
				t.Fatalf("unexpected error: %v", err)
			}
			var gotVersion, tables int
			var value string
			if err := db.QueryRow(`PRAGMA user_version`).Scan(&gotVersion); err != nil || gotVersion != version {
				t.Fatalf("version=%d error=%v", gotVersion, err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil || tables != 1 {
				t.Fatalf("tables=%d error=%v", tables, err)
			}
			if err := db.QueryRow(`SELECT value FROM original`).Scan(&value); err != nil || value != "preserved" {
				t.Fatalf("value=%q error=%v", value, err)
			}
		})
	}
}
