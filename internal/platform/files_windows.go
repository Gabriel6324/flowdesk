//go:build windows

package platform

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/user"
	"syscall"
	"unsafe"
)

func securePath(p string) error {
	u, e := user.Current()
	if e != nil {
		return e
	}
	s, e := syscall.UTF16PtrFromString("D:P(A;;FA;;;SY)(A;;FA;;;" + u.Uid + ")")
	if e != nil {
		return e
	}
	var sd uintptr
	var size uint32
	a := syscall.NewLazyDLL("advapi32.dll")
	r, _, er := a.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW").Call(uintptr(unsafe.Pointer(s)), 1, uintptr(unsafe.Pointer(&sd)), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return er
	}
	defer syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree").Call(sd)
	pp, e := syscall.UTF16PtrFromString(p)
	if e != nil {
		return e
	}
	r, _, er = a.NewProc("SetFileSecurityW").Call(uintptr(unsafe.Pointer(pp)), 4|0x80000000, sd)
	if r == 0 {
		return er
	}
	return nil
}
func PrivateFile(p string) error { return securePath(p) }
func ReplaceFile(from, to string) error {
	f, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	t, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(f, t, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func SyncDir(p string) error { return nil } // File content is flushed before replacement; native durability validation remains pending.
func PrivateDir(p string) error {
	if e := os.MkdirAll(p, 0700); e != nil {
		return e
	}
	return securePath(p)
}

type Lock struct {
	f *os.File
	o windows.Overlapped
}

func AcquireLock(p string) (*Lock, error) {
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	l := &Lock{f: f}
	e = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &l.o)
	if e != nil {
		f.Close()
		return nil, fmt.Errorf("INSTANCE_LOCK_FAILED")
	}
	return l, nil
}
func (l *Lock) Close() error {
	windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &l.o)
	return l.f.Close()
}
