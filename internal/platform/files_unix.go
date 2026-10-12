//go:build !windows

package platform

import (
	"fmt"
	"os"
	"syscall"
)

func PrivateFile(p string) error        { return os.Chmod(p, 0600) }
func ReplaceFile(from, to string) error { return os.Rename(from, to) }
func SyncDir(p string) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func PrivateDir(p string) error {
	if e := os.MkdirAll(p, 0700); e != nil {
		return e
	}
	return os.Chmod(p, 0700)
}

type Lock struct{ f *os.File }

func AcquireLock(p string) (*Lock, error) {
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("INSTANCE_LOCK_FAILED")
	}
	return &Lock{f}, nil
}
func (l *Lock) Close() error { syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); return l.f.Close() }
