package auth

import (
	"zhan/backend/internal/database"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"golang.org/x/crypto/argon2"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type Auth struct {
	DB    *database.Store
	key   []byte
	Hours int
}
type User struct {
	MustChangePassword bool   `json:"must_change_password"`
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	CSRF               string `json:"csrf_token"`
}

func New(db *database.Store, dir string, hours int) (*Auth, error) {
	path := filepath.Join(dir, "secret.key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		_, err = f.Write(key)
		f.Close()
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("secret.key must contain exactly 32 bytes")
	}
	return &Auth{db, key, hours}, nil
}
func Hash(password string) (string, error) {
	if utf8.RuneCountInString(password) < 6 || len(password) > 128 {
		return "", errors.New("密码至少 6 个字符，最多 128 字节")
	}
	return hashPassword(password)
}
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 2, 32*1024, 1, 32)
	return "argon2id$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func Verify(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 3 || parts[0] != "argon2id" || len(password) > 128 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 2, 32*1024, 1, 32)
	return subtle.ConstantTimeCompare(want, got) == 1
}
func (a *Auth) Init(username, password string) error {
	if len(username) < 1 || len(username) > 64 || strings.TrimSpace(username) != username {
		return errors.New("invalid username")
	}
	hash, err := Hash(password)
	if err != nil {
		return err
	}
	result, err := a.DB.Exec("INSERT INTO users(username,password_hash) SELECT ?,? WHERE NOT EXISTS(SELECT 1 FROM users)", username, hash)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("管理员已存在，初始化不会覆盖账户")
	}
	a.DB.Audit(username, "admin_init", "system", "success")
	return nil
}
func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (a *Auth) Login(username, password string) (string, User, error) {
	var u User
	var hash string
	err := a.DB.QueryRow("SELECT id,username,password_hash,must_change_password FROM users WHERE username=?", username).Scan(&u.ID, &u.Username, &hash, &u.MustChangePassword)
	if err != nil {
		_ = argon2.IDKey([]byte(password), make([]byte, 16), 2, 32*1024, 1, 32)
		return "", u, errors.New("用户名或密码错误")
	}
	if !Verify(hash, password) {
		return "", u, errors.New("用户名或密码错误")
	}
	token := random()
	u.CSRF = random()
	_, _ = a.DB.Exec("DELETE FROM sessions WHERE expires_at<?", time.Now().Unix())
	_, err = a.DB.Exec("INSERT INTO sessions(token_hash,user_id,csrf,expires_at) VALUES(?,?,?,?)", digest(token), u.ID, u.CSRF, time.Now().Add(time.Duration(a.Hours)*time.Hour).Unix())
	return token, u, err
}
func (a *Auth) Session(token string) (User, error) {
	var u User
	if len(token) != 64 {
		return u, errors.New("unauthorized")
	}
	err := a.DB.QueryRow("SELECT u.id,u.username,s.csrf,u.must_change_password FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?", digest(token), time.Now().Unix()).Scan(&u.ID, &u.Username, &u.CSRF, &u.MustChangePassword)
	return u, err
}
func (a *Auth) Logout(token string) {
	_, _ = a.DB.Exec("DELETE FROM sessions WHERE token_hash=?", digest(token))
}
func (a *Auth) Encrypt(value string) (string, error) {
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return "", err
	}
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(value), nil)), nil
}
func (a *Auth) Decrypt(value string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	block, _ := aes.NewCipher(a.key)
	gcm, _ := cipher.NewGCM(block)
	if len(data) < gcm.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	return string(plain), err
}

// InitDefault is only used for first-run bootstrap; normal passwords use Hash.
func (a *Auth) InitDefault(username string) (bool, error) {
	if len(username) < 1 || len(username) > 64 || strings.TrimSpace(username) != username {
		return false, errors.New("invalid username")
	}
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	hash, err := hashPassword("123456")
	if err != nil {
		return false, err
	}
	result, err := a.DB.Exec("INSERT INTO users(username,password_hash,must_change_password) SELECT ?,?,1 WHERE NOT EXISTS(SELECT 1 FROM users)", username, hash)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 1 {
		a.DB.Audit(username, "admin_bootstrap", "system", "password_change_required")
	}
	return n == 1, err
}
func (a *Auth) ChangePassword(id int64, current, next string) error {
	var oldHash string
	if err := a.DB.QueryRow("SELECT password_hash FROM users WHERE id=?", id).Scan(&oldHash); err != nil {
		return err
	}
	if !Verify(oldHash, current) {
		return errors.New("当前密码错误")
	}
	if current == next {
		return errors.New("新密码不能与当前密码相同")
	}
	hash, err := Hash(next)
	if err != nil {
		return err
	}
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE users SET password_hash=?,must_change_password=0 WHERE id=? AND password_hash=?", hash, id, oldHash)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("密码已被修改，请重新登录")
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE user_id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
