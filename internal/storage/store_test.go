package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func openTestStore(t *testing.T) (*Files, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func TestRecordsPersistAndPrefixListingIsExactAndSorted(t *testing.T) {
	s, dir := openTestStore(t)
	ctx := context.Background()
	for _, key := range []string{"printers/v1/z", "credentials/v1", "printers/v10/no", "printers/v1/a"} {
		if err := s.Save(ctx, key, map[string]string{"key": key}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, recordsDirectory, ".pending-interrupted"), []byte("partial secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	keys, err := reopened.Keys(ctx, "printers/v1/")
	if err != nil || !reflect.DeepEqual(keys, []string{"printers/v1/a", "printers/v1/z"}) {
		t.Fatalf("keys = %v, %v", keys, err)
	}
	var got map[string]string
	if err = reopened.Load(ctx, "credentials/v1", &got); err != nil || got["key"] != "credentials/v1" {
		t.Fatalf("reopened record = %v, %v", got, err)
	}
	if err = reopened.Load(ctx, "missing", &got); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestDataGrantRequiredAndHostOwnerUntouched(t *testing.T) {
	for _, path := range []string{"", ".", "records", filepath.Join(t.TempDir(), "missing")} {
		if s, err := NewFiles(path); err == nil {
			s.Close()
			t.Fatalf("accepted missing/relative grant %q", path)
		}
	}
	dir := t.TempDir()
	owner := filepath.Join(dir, ".owner")
	if err := os.WriteFile(owner, []byte("host-owned marker"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Save(context.Background(), "credentials/v1", map[string]string{"secret": "sensitive"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(owner)
	if err != nil || string(body) != "host-owned marker" {
		t.Fatalf("host owner changed: %q %v", body, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("unexpected files in grant: %v %v", entries, err)
	}
	if runtime.GOOS == "windows" {
		return
	} // Privacy comes from the host ACL here.
	d, err := os.Stat(filepath.Join(dir, recordsDirectory))
	if err != nil || d.Mode().Perm() != 0700 {
		t.Fatalf("directory privacy: %v %v", d, err)
	}
	name, _ := recordName("credentials/v1")
	f, err := os.Stat(filepath.Join(dir, recordsDirectory, name))
	if err != nil || f.Mode().Perm() != 0600 {
		t.Fatalf("file privacy: %v %v", f, err)
	}
}

func TestFailedWritesPreservePreviousRecordAndLeaveNoTemporaryFiles(t *testing.T) {
	s, dir := openTestStore(t)
	ctx := context.Background()
	key := "credentials/v1"
	if err := s.Save(ctx, key, "original"); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, tc := range []struct {
		ctx   context.Context
		value any
	}{
		{canceled, "replacement"},
		{ctx, strings.Repeat("x", maxRecordBytes)},
		{ctx, make(chan int)},
	} {
		if err := s.Save(tc.ctx, key, tc.value); err == nil {
			t.Fatal("invalid write succeeded")
		}
		var got string
		if err := s.Load(ctx, key, &got); err != nil || got != "original" {
			t.Fatalf("failed write replaced record: %q %v", got, err)
		}
	}
	// A target which cannot be replaced exercises cleanup after the file was
	// fully written and synced, unlike validation failures before writing.
	blocked, _ := recordName("blocked")
	if err := os.Mkdir(filepath.Join(dir, recordsDirectory, blocked), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, "blocked", "replacement"); err == nil {
		t.Fatal("replaced a directory with a record")
	}
	entries, err := os.ReadDir(filepath.Join(dir, recordsDirectory))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			t.Fatalf("temporary file leaked: %s", entry.Name())
		}
	}
}

func TestInvalidAndOversizedStoredJSONFailsClosed(t *testing.T) {
	s, dir := openTestStore(t)
	name, _ := recordName("credentials/v1")
	for _, content := range []string{"{secret", `{} {}`, strings.Repeat(" ", maxRecordBytes+1)} {
		if err := os.WriteFile(filepath.Join(dir, recordsDirectory, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		var out any
		err := s.Load(context.Background(), "credentials/v1", &out)
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), dir) {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestKeysCannotAddressPathsOutsideRecords(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	for _, key := range []string{"", "../credentials", "a/../b", "/absolute", `C:\secret`, "a//b", "a/", "a\x00b", strings.Repeat("a", 161)} {
		if err := s.Save(ctx, key, "data"); err == nil {
			t.Fatalf("accepted key %q", key)
		}
		var out any
		if err := s.Load(ctx, key, &out); err == nil {
			t.Fatalf("loaded invalid key %q", key)
		}
	}
	for _, prefix := range []string{"../", "/", "a//", `C:\`} {
		if _, err := s.Keys(ctx, prefix); err == nil {
			t.Fatalf("accepted prefix %q", prefix)
		}
	}
}

func TestSymlinkRecordsCannotEscapeGrant(t *testing.T) {
	s, dir := openTestStore(t)
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`"private outside data"`), 0600); err != nil {
		t.Fatal(err)
	}
	name, _ := recordName("credentials/v1")
	if err := os.Symlink(outside, filepath.Join(dir, recordsDirectory, name)); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	var out any
	if err := s.Load(context.Background(), "credentials/v1", &out); err == nil {
		t.Fatal("read a symlink record")
	}
	if _, err := s.Keys(context.Background(), "credentials/"); err == nil {
		t.Fatal("listed a symlink record")
	}
	if err := s.Save(context.Background(), "credentials/v1", "replacement"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(outside)
	if err != nil || string(body) != `"private outside data"` {
		t.Fatalf("modified symlink target: %q %v", body, err)
	}
}

func TestConcurrentReplacementAlwaysReadsACompleteRecord(t *testing.T) {
	s, dir := openTestStore(t)
	// An independent reader has no shared mutex with the writer. This verifies
	// replacement semantics on disk, including concurrent backup-style readers.
	reader, err := NewFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx := context.Background()
	key := "printers/v1/concurrent"
	initial := strings.Repeat("x", 20000)
	if err := s.Save(ctx, key, initial); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 8)
	for worker := range 8 {
		workers.Go(func() {
			for round := range 30 {
				if worker%2 == 0 {
					if err := s.Save(ctx, key, strings.Repeat(fmt.Sprint(round%10), 20000)); err != nil {
						failures <- err
						return
					}
				} else {
					var got string
					if err := reader.Load(ctx, key, &got); err != nil {
						failures <- err
						return
					}
					if len(got) != 20000 || got != strings.Repeat(got[:1], 20000) {
						failures <- errors.New("observed a partial or mixed record")
						return
					}
				}
			}
		})
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestCloseAndCancellationStopAccess(t *testing.T) {
	s, _ := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out json.RawMessage
	if err := s.Load(ctx, "credentials/v1", &out); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled load: %v", err)
	}
	if _, err := s.Keys(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(context.Background(), "credentials/v1", &out); err == nil {
		t.Fatal("load after close")
	}
	if err := s.Save(context.Background(), "credentials/v1", "x"); err == nil {
		t.Fatal("save after close")
	}
	if _, err := s.Keys(context.Background(), ""); err == nil {
		t.Fatal("list after close")
	}
}
