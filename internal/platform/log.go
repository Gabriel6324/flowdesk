package platform

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

type RotatingLog struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func NewLog(dir string) (*slog.Logger, *RotatingLog, error) {
	p := filepath.Join(dir, "flowdesk.log")
	f, e := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		return nil, nil, e
	}
	if e = PrivateFile(p); e != nil {
		f.Close()
		return nil, nil, e
	}
	st, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, nil, e
	}
	w := &RotatingLog{path: p, f: f, size: st.Size()}
	return slog.New(slog.NewJSONHandler(io.MultiWriter(w, os.Stderr), nil)), w, nil
}
func (w *RotatingLog) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(b)) > 5*1024*1024 {
		if e := w.f.Close(); e != nil {
			return 0, e
		}
		_ = os.Remove(w.path + ".2")
		_ = os.Rename(w.path+".1", w.path+".2")
		_ = os.Rename(w.path, w.path+".1")
		f, e := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return 0, e
		}
		if e = PrivateFile(w.path); e != nil {
			f.Close()
			return 0, e
		}
		w.f = f
		w.size = 0
	}
	if len(b) > 65536 {
		return 0, fmt.Errorf("LOG_ENTRY_TOO_LARGE")
	}
	n, e := w.f.Write(b)
	w.size += int64(n)
	return n, e
}
func (w *RotatingLog) Close() error { w.mu.Lock(); defer w.mu.Unlock(); return w.f.Close() }
