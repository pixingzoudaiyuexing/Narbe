package store

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Called only by scripts/test_upgrade.py on its own stopped-container snapshot.
// Uses the real vault encoding, rather than a fake plaintext credential row.
func TestDockerUpgradeFixture(t *testing.T) {
	dir := os.Getenv("NARBE_DOCKER_UPGRADE_FIXTURE")
	if dir == "" {
		t.Skip("Docker upgrade helper only")
	}
	clean := filepath.Clean(dir)
	if filepath.Base(clean) != "data" || filepath.Base(filepath.Dir(clean)) != "fixture" || !strings.HasPrefix(filepath.Base(filepath.Dir(filepath.Dir(clean))), "narbe-upgrade-") {
		t.Fatal("refuse non-disposable fixture directory")
	}
	// Open the stopped beta.5 database without running the candidate schema
	// initializer: the upgrade itself must prove that migration works.
	key, e := os.ReadFile(filepath.Join(clean, "master.key"))
	if e != nil {
		t.Fatal(e)
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		t.Fatal(e)
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", filepath.Join(clean, "narbe.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := &Store{DB: db, aead: aead}
	var tables int
	if e = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='screenings'").Scan(&tables); e != nil || tables != 0 {
		t.Fatal("fixture must start with actual beta.5 schema", tables, e)
	}
	raw := []byte(`{"token":"fixture-not-a-real-cloud-credential"}`)
	sealed, e := s.Seal(raw, "account:upgrade-fixture")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec("INSERT INTO accounts(id,name,provider,credentials,created) VALUES('upgrade-fixture','升级凭据测试','linode',?,?)", sealed, Now())
	if e != nil {
		t.Fatal(e)
	}
	network, e := s.Seal([]byte(`{"revision":7,"config":{"mode":"proxy","protocol":"socks5h","host":"9.9.9.9","port":65535,"username":"fixture","password":"fixture-proxy-secret"}}`), "settings:cloud_egress")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO settings(key,value) VALUES('cloud_egress',?)", base64.RawStdEncoding.EncodeToString(network)); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO jobs(id,account_id,action,region,target,name,payload,state,progress,created,updated) VALUES('historical-done','upgrade-fixture','start','fixture-region','fixture-instance','retained',?,'succeeded','historical-result',1,1)", sealed); e != nil {
		t.Fatal(e)
	}
	decoded, e := s.Unseal(sealed, "account:upgrade-fixture")
	if e != nil || !json.Valid(decoded) {
		t.Fatal("fixture encryption failed")
	}
}
