// Package storage keeps versioned extension records in the host-granted directory.
package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

var ErrNotFound = errors.New("record not found")

const maxRecordBytes = 1 << 20
const recordsDirectory = "records-v1"

// Store writes one record atomically. Its caller serializes updates to each key.
type Store interface {
	Load(context.Context, string, any) error
	Save(context.Context, string, any) error
	Keys(context.Context, string) ([]string, error)
}

// Files owns only records-v1 beneath grants.Data; the host's .owner is untouched.
// The host must give each module instance its own directory. os.Root keeps every
// operation contained even if another process replaces a path with a symlink.
type Files struct {
	mu   sync.RWMutex
	root *os.Root
}

func NewFiles(dataDir string) (*Files, error) {
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		return nil, errors.New("approve the extension's data directory in Connect before starting")
	}
	grant, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, errors.New("cannot open the extension's granted data directory")
	}
	defer grant.Close()
	if err = grant.Mkdir(recordsDirectory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, errors.New("cannot create private extension records in the granted data directory")
	}
	info, err := grant.Lstat(recordsDirectory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("extension records directory is invalid")
	}
	root, err := grant.OpenRoot(recordsDirectory)
	if err != nil {
		return nil, errors.New("cannot open the extension records directory")
	}
	s := &Files{root: root}
	// Windows inherits the private ACL of the host grant. Unix permissions are
	// also explicitly restricted, including for an existing restored directory.
	if runtime.GOOS != "windows" {
		if err = root.Chmod(".", 0700); err != nil {
			s.Close()
			return nil, errors.New("cannot make the extension records directory private")
		}
	}
	// Fail configuration immediately for a read-only or exhausted grant, before
	// reporting the extension ready. Never create a probe containing credentials.
	f, temp, err := s.temporary()
	if err != nil {
		s.Close()
		return nil, errors.New("extension data directory is not writable")
	}
	_, err = f.WriteString("{}")
	if err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	removed := root.Remove(temp)
	if err != nil || closed != nil || removed != nil {
		s.Close()
		return nil, errors.New("cannot initialize the extension data directory")
	}
	return s, nil
}

func (s *Files) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil
	}
	err := s.root.Close()
	s.root = nil
	return err
}

func recordName(key string) (string, error) {
	if !validKey(key, false) {
		return "", errors.New("invalid extension record key")
	}
	return base64.RawURLEncoding.EncodeToString([]byte(key)) + ".json", nil
}

func validKey(key string, prefix bool) bool {
	if len(key) > 160 {
		return false
	}
	if prefix && key == "" {
		return true
	}
	if prefix {
		key = strings.TrimSuffix(key, "/")
	}
	if key == "" {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, c := range segment {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.') {
				return false
			}
		}
	}
	return true
}

func (s *Files) Load(ctx context.Context, key string, out any) error {
	name, err := recordName(key)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err = ctx.Err(); err != nil {
		return err
	}
	if s.root == nil {
		return errors.New("extension records are closed")
	}
	info, err := s.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return errors.New("cannot read extension record")
	}
	if !info.Mode().IsRegular() || info.Size() > maxRecordBytes {
		return errors.New("extension record is invalid or too large; restore its backup before continuing")
	}
	f, err := s.root.Open(name)
	if err != nil {
		return errors.New("cannot open extension record")
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxRecordBytes+1))
	if err != nil {
		return errors.New("cannot read extension record")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if len(body) > maxRecordBytes || json.Unmarshal(body, out) != nil {
		return errors.New("extension record is invalid or too large; restore its backup before continuing")
	}
	return nil
}

func (s *Files) Save(ctx context.Context, key string, value any) error {
	name, err := recordName(key)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	body, err := json.Marshal(value)
	if err != nil {
		return errors.New("cannot encode extension record")
	}
	if len(body) > maxRecordBytes {
		return errors.New("extension record exceeds storage limit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return err
	}
	if s.root == nil {
		return errors.New("extension records are closed")
	}
	f, temp, err := s.temporary()
	if err != nil {
		return errors.New("cannot create extension record; check free space and directory permissions")
	}
	defer s.root.Remove(temp)
	_, err = f.Write(body)
	if err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err != nil || closed != nil {
		return errors.New("cannot write extension record; check free space and directory permissions")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Replace only after the entire private temporary file is durable. Never
	// remove the previous record first: a failed replacement preserves it.
	if err = s.root.Rename(temp, name); err != nil {
		return errors.New("cannot replace extension record; the previous record was preserved")
	}
	// Unix needs the containing directory flushed to make the rename survive a
	// power failure. Windows does not support directory Sync; the file itself
	// was flushed before the native replacement above.
	if runtime.GOOS != "windows" {
		dir, openErr := s.root.Open(".")
		if openErr != nil {
			return errors.New("extension record was replaced but its directory could not be flushed")
		}
		syncErr := dir.Sync()
		closeErr := dir.Close()
		if syncErr != nil || closeErr != nil {
			return errors.New("extension record was replaced but its directory could not be flushed")
		}
	}
	return nil
}

func (s *Files) temporary() (*os.File, string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, "", err
	}
	name := ".pending-" + hex.EncodeToString(nonce[:])
	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	return f, name, err
}

func (s *Files) Keys(ctx context.Context, prefix string) ([]string, error) {
	if !validKey(prefix, true) {
		return nil, errors.New("invalid extension record prefix")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.root == nil {
		return nil, errors.New("extension records are closed")
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return nil, errors.New("cannot list extension records")
	}
	defer dir.Close()
	result := []string{}
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		entries, readErr := dir.ReadDir(100)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, errors.New("cannot list extension records")
		}
		for _, entry := range entries {
			encoded, ok := strings.CutSuffix(entry.Name(), ".json")
			if !ok {
				continue
			} // Never treat interrupted temporary writes as records.
			decoded, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
			key := string(decoded)
			if decodeErr != nil || !validKey(key, false) || !strings.HasPrefix(key, prefix) {
				continue
			}
			if !entry.Type().IsRegular() {
				return nil, errors.New("extension records contain a non-regular file")
			}
			result = append(result, key)
			if len(result) > 10000 {
				return nil, errors.New("extension record count exceeds storage limit")
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	sort.Strings(result)
	return result, nil
}
