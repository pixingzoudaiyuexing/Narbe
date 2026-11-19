package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConnectivityMigrationRetainsKeyAndExistingRows(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(dir, "master.key"))
	if e != nil {
		t.Fatal(e)
	}
	secret, e := s.Seal([]byte("fixture-account-token"), "account:existing")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec("INSERT INTO accounts(id,name,provider,credentials,created) VALUES('existing','existing','linode',?,1)", secret)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec("INSERT INTO connectivity_probes(id,name,role,expected_ip) VALUES('old-probe','old','domestic','223.5.5.5')")
	if e != nil {
		t.Fatal(e)
	}
	// Simulate the first connectivity-candidate schema before the additive columns.
	_, e = s.DB.Exec("DROP INDEX screening_due; ALTER TABLE screenings DROP COLUMN next_run; ALTER TABLE connectivity_probes DROP COLUMN deleted; DELETE FROM settings WHERE key='connectivity_schema_version'")
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	for n := 0; n < 2; n++ {
		s, e = Open(dir)
		if e != nil {
			t.Fatal(e)
		}
		var enabled, deleted int
		if e = s.DB.QueryRow("SELECT enabled,deleted FROM connectivity_probes WHERE id='old-probe'").Scan(&enabled, &deleted); e != nil || enabled != 1 || deleted != 0 {
			t.Fatal(enabled, deleted, e)
		}
		var enc []byte
		if e = s.DB.QueryRow("SELECT credentials FROM accounts WHERE id='existing'").Scan(&enc); e != nil {
			t.Fatal(e)
		}
		plain, e := s.Unseal(enc, "account:existing")
		if e != nil || string(plain) != "fixture-account-token" {
			t.Fatal("account changed", e)
		}
		var version string
		if e = s.DB.QueryRow("SELECT value FROM settings WHERE key='connectivity_schema_version'").Scan(&version); e != nil || version != "2" {
			t.Fatal("migration marker", version, e)
		}
		after, e := os.ReadFile(filepath.Join(dir, "master.key"))
		if e != nil || string(before) != string(after) {
			t.Fatal("master key changed", e)
		}
		s.Close()
	}
}
