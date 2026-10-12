package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"flowdesk/internal/auth"
	"flowdesk/internal/buildinfo"
	"flowdesk/internal/platform"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type BackupManifest struct {
	SoftwareVersion string `json:"software_version"`
	SourceHash      string `json:"source_hash"`
	CreatedAt       string `json:"created_at"`
	Schema          int    `json:"schema_version"`
	SQLite          string `json:"sqlite_version"`
	WorkspaceID     string `json:"workspace_id"`
	Profile         string `json:"profile"`
	DBHash          string `json:"database_sha256"`
	KeyFingerprint  string `json:"key_fingerprint"`
	KeyUnavailable  bool   `json:"key_unavailable"`
}

func (s *Store) Backup(ctx context.Context, requireKey bool) (string, error) {
	root := filepath.Join(s.Dir, "backups")
	if e := platform.PrivateDir(root); e != nil {
		return "", e
	}
	name := "backup-" + time.Now().UTC().Format("20060102T150405.000000000") + "-" + auth.RandomHex(4)
	tmp := filepath.Join(root, name+".tmp")
	final := filepath.Join(root, name)
	if e := platform.PrivateDir(tmp); e != nil {
		return "", e
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	key, e := os.ReadFile(filepath.Join(s.Dir, "master.key"))
	valid := e == nil && len(key) == 32
	if requireKey && !valid {
		return "", fmt.Errorf("KEYSTORE_UNAVAILABLE")
	}
	path := filepath.Join(tmp, "flowdesk.db")
	if _, e = s.DB.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(path, "'", "''")+"'"); e != nil {
		return "", e
	}
	if e = platform.PrivateFile(path); e != nil {
		return "", e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	m := BackupManifest{SoftwareVersion: buildinfo.Version, SourceHash: buildinfo.SourceHash, CreatedAt: Now(), Schema: SchemaVersion, SQLite: s.SQLiteVersion, WorkspaceID: s.WorkspaceID, Profile: s.Profile, DBHash: Sum(string(b)), KeyUnavailable: !valid}
	if valid {
		m.KeyFingerprint = Sum(string(key))
		if e = platform.AtomicPrivateWrite(filepath.Join(tmp, "master.key"), key); e != nil {
			return "", e
		}
	}
	if e = platform.AtomicPrivateWrite(filepath.Join(tmp, "manifest.json"), []byte(JSON(m))); e != nil {
		return "", e
	}
	if e = os.Rename(tmp, final); e != nil {
		return "", e
	}
	if e = platform.SyncDir(root); e != nil {
		return "", e
	}
	ok = true
	entries, e := os.ReadDir(root)
	if e != nil {
		return "", e
	}
	groups := []string{}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "backup-") && !strings.HasSuffix(entry.Name(), ".tmp") {
			groups = append(groups, entry.Name())
		}
	}
	sort.Strings(groups)
	for len(groups) > 3 {
		if e = os.RemoveAll(filepath.Join(root, groups[0])); e != nil {
			return "", e
		}
		groups = groups[1:]
	}
	return final, nil
}

type recoveryIntent struct {
	ActionID       string `json:"action_id"`
	NewFingerprint string `json:"new_key_fingerprint"`
	Stage          string `json:"stage"`
}

func (s *Store) RecoverKeystore(ctx context.Context, hook func(string) error) error {
	marker := filepath.Join(s.Dir, "keystore-recovery.json")
	temp := filepath.Join(s.Dir, "master.key.recovery")
	target := filepath.Join(s.Dir, "master.key")
	var in recoveryIntent
	b, e := os.ReadFile(marker)
	if os.IsNotExist(e) {
		if _, e = s.Backup(ctx, false); e != nil {
			return e
		}
		key := make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return e
		}
		if e = platform.AtomicPrivateWrite(temp, key); e != nil {
			return e
		}
		in = recoveryIntent{auth.RandomHex(16), Sum(string(key)), "prepared"}
		if e = platform.AtomicPrivateWrite(marker, []byte(JSON(in))); e != nil {
			return e
		}
		if hook != nil {
			if e = hook("prepared"); e != nil {
				return e
			}
		}
	} else if e != nil {
		return e
	} else if e = json.Unmarshal(b, &in); e != nil || in.Stage != "prepared" || len(in.ActionID) != 32 || len(in.NewFingerprint) != 64 {
		return fmt.Errorf("KEYSTORE_RECOVERY_INCOMPLETE")
	}
	newKey, e := os.ReadFile(temp)
	current, readErr := os.ReadFile(target)
	alreadyInstalled := readErr == nil && len(current) == 32 && Sum(string(current)) == in.NewFingerprint
	if !alreadyInstalled && (e != nil || len(newKey) != 32 || Sum(string(newKey)) != in.NewFingerprint) {
		return fmt.Errorf("KEYSTORE_RECOVERY_INCOMPLETE")
	}
	e = s.Tx(ctx, func(tx *sql.Tx) error {
		var fingerprint string
		e := tx.QueryRowContext(ctx, "SELECT new_key_fingerprint FROM keystore_recoveries WHERE action_id=?", in.ActionID).Scan(&fingerprint)
		if e == nil {
			if fingerprint != in.NewFingerprint {
				return fmt.Errorf("KEYSTORE_RECOVERY_INCOMPLETE")
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE ai_settings SET encrypted_key=NULL,config_json=json_set(config_json,'$.mode','demo'),version=version+1 WHERE singleton_id=1"); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE jobs SET status='failed',error_code='KEYSTORE_UNAVAILABLE',error_stage='configuration',finished_at=? WHERE status IN ('queued','running') AND json_extract(config_json,'$.mode')='live'", Now()); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE jobs SET encrypted_secret=NULL"); e != nil {
			return e
		}
		if e = ClearTerminatedActive(ctx, tx); e != nil {
			return e
		}
		if e = Audit(ctx, tx, nil, nil, "keystore_recovered", map[string]any{"action_id": in.ActionID}); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO keystore_recoveries VALUES(?,?,?)", in.ActionID, in.NewFingerprint, Now())
		return e
	})
	if e != nil {
		var ce *CommitError
		if !errors.As(e, &ce) {
			return e
		}
		var got string
		check, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		e = s.DB.QueryRowContext(check, "SELECT new_key_fingerprint FROM keystore_recoveries WHERE action_id=?", in.ActionID).Scan(&got)
		cancel()
		if e != nil || got != in.NewFingerprint {
			return fmt.Errorf("KEYSTORE_RECOVERY_INCOMPLETE")
		}
	}
	if hook != nil {
		if e = hook("committed"); e != nil {
			return e
		}
	}
	if !alreadyInstalled {
		if e = platform.ReplaceFile(temp, target); e != nil {
			return e
		}
		if e = platform.PrivateFile(target); e != nil {
			return e
		}
		if e = platform.SyncDir(s.Dir); e != nil {
			return e
		}
	}
	if hook != nil {
		if e = hook("installed"); e != nil {
			return e
		}
	}
	var secrets int
	if e = s.DB.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM jobs WHERE encrypted_secret IS NOT NULL)+(SELECT COUNT(*) FROM ai_settings WHERE encrypted_key IS NOT NULL)").Scan(&secrets); e != nil || secrets != 0 {
		return fmt.Errorf("KEYSTORE_RECOVERY_INCOMPLETE")
	}
	if e = os.Remove(marker); e != nil {
		return e
	}
	_ = os.Remove(temp)
	return platform.SyncDir(s.Dir)
}
