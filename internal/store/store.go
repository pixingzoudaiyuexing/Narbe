package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"
)

// MaxQueuedAge bounds how long a confirmed operation may wait before its first cloud write.
const MaxQueuedAge = 5 * time.Minute

type Store struct {
	DB   *sql.DB
	aead cipher.AEAD
	lock *os.File
}

func open(dir string) (*Store, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	if e := os.Chmod(dir, 0700); e != nil {
		return nil, e
	}
	keyPath := filepath.Join(dir, "master.key")
	key, e := os.ReadFile(keyPath)
	if errors.Is(e, os.ErrNotExist) {
		if _, statErr := os.Stat(filepath.Join(dir, "narbe.db")); statErr == nil {
			return nil, errors.New("已有数据库缺少 master.key，请恢复原密钥，不能生成替代密钥")
		}
		key = make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return nil, e
		}
		f, e := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		_, e = f.Write(key)
		syncErr := f.Sync()
		closeErr := f.Close()
		if syncErr != nil {
			return nil, syncErr
		}
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
	} else if e != nil {
		return nil, e
	}
	if len(key) != 32 {
		return nil, errors.New("master.key 必须为32字节，不能替换已有数据库的密钥")
	}
	if e = os.Chmod(keyPath, 0600); e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "narbe.db"))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, aead: aead}
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(hash TEXT PRIMARY KEY,csrf TEXT NOT NULL,expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS accounts(id TEXT PRIMARY KEY,name TEXT NOT NULL,provider TEXT NOT NULL,credentials BLOB NOT NULL,created INTEGER NOT NULL,retired INTEGER NOT NULL DEFAULT 0,revision INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,account_id TEXT NOT NULL REFERENCES accounts(id),action TEXT NOT NULL,region TEXT NOT NULL,target TEXT NOT NULL,name TEXT NOT NULL,payload BLOB NOT NULL,state TEXT NOT NULL,progress TEXT NOT NULL,result BLOB,error TEXT NOT NULL DEFAULT '',created INTEGER NOT NULL,updated INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS jobs_state ON jobs(state,created);
CREATE TABLE IF NOT EXISTS confirmations(id TEXT PRIMARY KEY,digest TEXT NOT NULL,expires INTEGER NOT NULL);
INSERT OR IGNORE INTO settings(key,value) VALUES('schema_version','1');
`)
	if e != nil {
		db.Close()
		return nil, e
	}
	var check string
	err := db.QueryRow("SELECT value FROM settings WHERE key='key_check'").Scan(&check)
	if err == sql.ErrNoRows {
		sealed, err := s.Seal([]byte("Narbe key verification v1"), "key-check")
		if err != nil {
			db.Close()
			return nil, err
		}
		if _, err = db.Exec("INSERT INTO settings(key,value) VALUES('key_check',?)", base64.RawStdEncoding.EncodeToString(sealed)); err != nil {
			db.Close()
			return nil, err
		}
	} else if err != nil {
		db.Close()
		return nil, err
	} else {
		sealed, err := base64.RawStdEncoding.DecodeString(check)
		if err != nil {
			db.Close()
			return nil, errors.New("数据库密钥校验信息损坏")
		}
		plain, err := s.Unseal(sealed, "key-check")
		if err != nil || string(plain) != "Narbe key verification v1" {
			db.Close()
			return nil, errors.New("master.key 与数据库不匹配，请恢复原密钥")
		}
	}
	recovery, e := db.Begin()
	if e != nil {
		db.Close()
		return nil, e
	}
	_, e = recovery.Exec("UPDATE jobs SET state='unknown',progress='应用重启；请核实云端结果，系统不会自动重复执行',updated=unixepoch() WHERE state='running'; UPDATE jobs SET state='cancelled',progress='应用重启，未派发任务已取消；请重新检查并确认操作',updated=unixepoch() WHERE state='queued'; DELETE FROM sessions WHERE expires<unixepoch(); DELETE FROM confirmations;")
	if e == nil {
		e = recovery.Commit()
	}
	if e != nil {
		recovery.Rollback()
		db.Close()
		return nil, e
	}
	if e = os.Chmod(filepath.Join(dir, "narbe.db"), 0600); e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Seal(plain []byte, context string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, e := rand.Read(nonce); e != nil {
		return nil, e
	}
	return s.aead.Seal(nonce, nonce, plain, []byte(context)), nil
}
func (s *Store) Unseal(b []byte, context string) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(b) < n {
		return nil, errors.New("加密数据损坏")
	}
	return s.aead.Open(nil, b[:n], b[n:], []byte(context))
}
func ID() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func Password(p string) (string, error) {
	if len(p) < 12 || len(p) > 256 {
		return "", errors.New("密码长度必须为 12–256 字符")
	}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	h := argon2.IDKey([]byte(p), salt, 2, 64*1024, 2, 32)
	return fmt.Sprintf("argon2id$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h)), nil
}
func CheckPassword(p, h string) bool {
	if len(p) > 256 {
		return false
	}
	parts := strings.Split(h, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[1])
	if e != nil || len(salt) != 16 {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(parts[2])
	if e != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(p), salt, 2, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}
func (s *Store) Setting(k string) string {
	var v string
	_ = s.DB.QueryRow("SELECT value FROM settings WHERE key=?", k).Scan(&v)
	return v
}
func (s *Store) InitPassword(passwordFile string) error {
	if s.Setting("password") != "" {
		return nil
	}
	b, e := os.ReadFile(passwordFile)
	if e != nil {
		return errors.New("首次启动需要 NARBE_ADMIN_PASSWORD_FILE 指向密码文件")
	}
	h, e := Password(strings.TrimSpace(string(b)))
	if e != nil {
		return e
	}
	_, e = s.DB.Exec("INSERT INTO settings(key,value) VALUES('password',?)", h)
	return e
}
func Now() int64 { return time.Now().Unix() }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("数据目录正由另一个 Narbe 进程使用")
	}
	s, err := open(dir)
	if err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, err
	}
	s.lock = f
	return s, nil
}
func (s *Store) Close() error {
	err := s.DB.Close()
	if s.lock != nil {
		syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
		s.lock.Close()
	}
	return err
}
