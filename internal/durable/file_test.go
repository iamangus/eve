package durable

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncedFilePermissionsAndExclusiveProcessLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := JSON(path, map[string]string{"value": "one"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
	lock, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Lock(dir); err == nil {
		second.Close()
		t.Fatal("second process lock acquired")
	}
	lock.Close()
	second, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
}
