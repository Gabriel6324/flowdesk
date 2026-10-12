package platform

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"flowdesk/internal/auth"
	"fmt"
	"os"
	"path/filepath"
)

type Keystore struct{ key []byte }

func LoadKey(dir string, create bool) (*Keystore, error) {
	p := filepath.Join(dir, "master.key")
	b, e := os.ReadFile(p)
	if os.IsNotExist(e) && create {
		b = make([]byte, 32)
		if _, e = rand.Read(b); e != nil {
			return nil, e
		}
		if e = AtomicPrivateWrite(p, b); e != nil {
			return nil, e
		}
	} else if e != nil {
		return nil, e
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("KEYSTORE_UNAVAILABLE")
	}
	if e = PrivateFile(p); e != nil {
		return nil, e
	}
	return &Keystore{b}, nil
}
func (k *Keystore) Available() bool { return k != nil && len(k.key) == 32 }
func (k *Keystore) Encrypt(plain string) ([]byte, error) {
	if !k.Available() {
		return nil, fmt.Errorf("KEYSTORE_UNAVAILABLE")
	}
	b, e := aes.NewCipher(k.key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, e
	}
	return g.Seal(n, n, []byte(plain), nil), nil
}
func (k *Keystore) Decrypt(v []byte) (string, error) {
	if !k.Available() {
		return "", fmt.Errorf("KEYSTORE_UNAVAILABLE")
	}
	b, e := aes.NewCipher(k.key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil || len(v) < g.NonceSize() {
		return "", fmt.Errorf("KEYSTORE_UNAVAILABLE")
	}
	p, e := g.Open(nil, v[:g.NonceSize()], v[g.NonceSize():], nil)
	if e != nil {
		return "", fmt.Errorf("KEYSTORE_UNAVAILABLE")
	}
	return string(p), nil
}
func AtomicPrivateWrite(path string, b []byte) error {
	tmp := path + "." + auth.RandomHex(8) + ".tmp"
	f, e := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer os.Remove(tmp)
	if e = PrivateFile(tmp); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = ReplaceFile(tmp, path); e != nil {
		return e
	}
	return SyncDir(filepath.Dir(path))
}
