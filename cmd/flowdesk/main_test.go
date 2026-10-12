package main

import (
	"bytes"
	"errors"
	"flowdesk/internal/platform"
	"flowdesk/internal/repository"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupDiagnosticShowsSafeStageAndActualDataPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "测试 workspace")
	err := &startupFailure{dataDir: path, err: &repository.DatabaseError{Code: "DATABASE_OPEN_FAILED", Operation: "open_database", Err: errors.New("do not print raw secret=private")}}
	var out bytes.Buffer
	writeStartupError(&out, err)
	for _, want := range []string{"DATABASE_OPEN_FAILED", path, "flowdesk.db", "打开数据库", "--diagnose"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "private") {
		t.Fatal("raw driver error exposed")
	}
}

func TestDiagnosisDoesNotCreateMissingDataDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	if e := diagnose(platform.Config{Profile: "desktop", DataDir: dir}); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("diagnostic created data directory")
	}
}

func TestDiagnosisLeavesPristineFileAndKeyUntouched(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "flowdesk.db")
	key := filepath.Join(dir, "master.key")
	if e := os.WriteFile(db, nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(key, bytes.Repeat([]byte("k"), 32), 0600); e != nil {
		t.Fatal(e)
	}
	before, _ := os.Stat(db)
	if e := diagnose(platform.Config{Profile: "desktop", DataDir: dir}); e != nil {
		t.Fatal(e)
	}
	after, _ := os.Stat(db)
	actual, _ := os.ReadFile(key)
	if after.Size() != 0 || !before.ModTime().Equal(after.ModTime()) || !bytes.Equal(actual, bytes.Repeat([]byte("k"), 32)) {
		t.Fatal("diagnosis modified database/key")
	}
}

func TestPathUnderscoresAreNotParsedAsStartupCodes(t *testing.T) {
	var out bytes.Buffer
	writeStartupError(&out, errors.New("mkdir /tmp/data_dir: permission denied"))
	if !strings.HasPrefix(out.String(), "STARTUP_FAILED:") {
		t.Fatal("filesystem path interpreted as an error code", out.String())
	}
}
