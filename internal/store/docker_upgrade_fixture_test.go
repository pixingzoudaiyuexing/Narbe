package store

import (
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
	s, e := Open(clean)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	raw := []byte(`{"token":"fixture-not-a-real-cloud-credential"}`)
	sealed, e := s.Seal(raw, "account:upgrade-fixture")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec("INSERT INTO accounts(id,name,provider,credentials,created) VALUES('upgrade-fixture','升级凭据测试','linode',?,?)", sealed, Now())
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := s.Unseal(sealed, "account:upgrade-fixture")
	if e != nil || !json.Valid(decoded) {
		t.Fatal("fixture encryption failed")
	}
}
