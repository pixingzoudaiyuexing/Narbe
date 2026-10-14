package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptionContextTamperAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	plain := []byte("fixture-only-cloud-secret")
	sealed, e := s.Seal(plain, "account:a")
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("plaintext stored")
	}
	if _, e = s.Unseal(sealed, "account:b"); e == nil {
		t.Fatal("cross-row ciphertext accepted")
	}
	bad := append([]byte{}, sealed...)
	bad[len(bad)-1] ^= 1
	if _, e = s.Unseal(bad, "account:a"); e == nil {
		t.Fatal("tamper accepted")
	}
	if _, e = Open(dir); e == nil {
		t.Fatal("second process lock accepted")
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got, e := s.Unseal(sealed, "account:a")
	if e != nil || !bytes.Equal(got, plain) {
		t.Fatal("encrypted data did not survive reopen", e)
	}
	info, _ := os.Stat(filepath.Join(dir, "master.key"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("key mode", info.Mode())
	}
}
func TestMissingKeyFailsClosedAndRestoringKeyWorks(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	sealed, _ := s.Seal([]byte("fixture"), "account:a")
	s.Close()
	keyPath := filepath.Join(dir, "master.key")
	key, _ := os.ReadFile(keyPath)
	if e = os.Remove(keyPath); e != nil {
		t.Fatal(e)
	}
	if s, e = Open(dir); e == nil {
		s.Close()
		t.Fatal("missing existing database key accepted")
	}
	if _, e = os.Stat(keyPath); !os.IsNotExist(e) {
		t.Fatal("replacement key generated")
	}
	os.WriteFile(keyPath, key, 0600)
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.Unseal(sealed, "account:a"); e != nil {
		t.Fatal(e)
	}
}
func TestStartupNeverRequeuesRunningJobs(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec("INSERT INTO accounts(id,name,provider,credentials,created) VALUES('a','test','aws',x'00',0); INSERT INTO jobs(id,account_id,action,region,target,name,payload,state,progress,created,updated) VALUES('running','a','create','r','','x',x'00','running','',0,0),('queued','a','start','r','i','x',x'00','queued','',0,0)")
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for id, want := range map[string]string{"running": "unknown", "queued": "cancelled"} {
		var got string
		s.DB.QueryRow("SELECT state FROM jobs WHERE id=?", id).Scan(&got)
		if got != want {
			t.Fatalf("%s: %s", id, got)
		}
	}
}
func TestPasswordHashBoundary(t *testing.T) {
	if _, e := Password("short"); e == nil {
		t.Fatal("short accepted")
	}
	h, e := Password("fixture-password-12")
	if e != nil {
		t.Fatal(e)
	}
	if !CheckPassword("fixture-password-12", h) || CheckPassword("wrong", h) || CheckPassword("fixture-password-12", "argon2id$garbage$broken") {
		t.Fatal("password verification broken")
	}
}
func FuzzUnsealNeverPanics(f *testing.F) {
	s, e := Open(f.TempDir())
	if e != nil {
		f.Fatal(e)
	}
	f.Cleanup(func() { s.Close() })
	f.Add([]byte("malformed"), "account:a")
	f.Fuzz(func(t *testing.T, b []byte, context string) {
		if len(b) > 4096 || len(context) > 256 {
			t.Skip()
		}
		_, _ = s.Unseal(b, context)
	})
}

func TestWrongMasterKeyRejectedBeforeRecovery(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	path := filepath.Join(dir, "master.key")
	key, _ := os.ReadFile(path)
	os.WriteFile(path, bytes.Repeat([]byte{1}, 32), 0600)
	if s, e = Open(dir); e == nil {
		s.Close()
		t.Fatal("wrong master key accepted")
	}
	os.WriteFile(path, key, 0600)
	s, e = Open(dir)
	if e != nil {
		t.Fatal("restored key rejected", e)
	}
	s.Close()
}
