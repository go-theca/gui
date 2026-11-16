// SPDX-License-Identifier: BSD-3-Clause

package vault

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-theca/theca"
	"github.com/go-theca/theca/fido2"
)

// fakeKey is a security key that answers like a hmac-secret authenticator:
// the output for a salt is a hash of it.
type fakeKey struct {
	enrolled int
	closed   bool
}

func (k *fakeKey) Enroll(_ context.Context, salts [][32]byte) ([][]byte, error) {
	k.enrolled++
	out := make([][]byte, len(salts))
	for i, s := range salts {
		h := sha256.Sum256(s[:])
		out[i] = h[:]
	}
	return out, nil
}

func (k *fakeKey) Derive(context.Context, [][32]byte) ([][][]byte, error) {
	return nil, theca.ErrNoCredential
}

func (k *fakeKey) Close() error { k.closed = true; return nil }

func opener(k *fakeKey) KeyOpener {
	return func(context.Context, func() (string, error)) (Key, error) { return k, nil }
}

var errNoKey = errors.New("no key attached")

func noKey(context.Context, func() (string, error)) (Key, error) { return nil, errNoKey }

// newContainer creates a 64 KiB container with the given policy and returns
// its path and recovery code.
func newContainer(t *testing.T, pol Policy) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "c.theca")
	code, err := (&Service{Hint: 1}).Create(CreateRequest{Path: path, Size: "64K", Policy: pol, Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	return path, code
}

func TestParseSize(t *testing.T) {
	for _, c := range []struct {
		in   string
		want uint64
		err  bool
	}{
		{"4096", 4096, false},
		{"64k", 64 << 10, false},
		{" 2M ", 2 << 20, false},
		{"1G", 1 << 30, false},
		{"1T", 1 << 40, false},
		{"", 0, true},
		{"x", 0, true},
		{"-1", 0, true},
		{"99999999999T", 0, true},
	} {
		got, err := ParseSize(c.in)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d, err=%v", c.in, got, err, c.want, c.err)
		}
	}
}

func TestCreateRefusals(t *testing.T) {
	dir := t.TempDir()
	s := &Service{Hint: 1}
	for name, req := range map[string]CreateRequest{
		"bad size":       {Path: filepath.Join(dir, "a"), Size: "lots", Password: []byte("x")},
		"zero size":      {Path: filepath.Join(dir, "b"), Size: "0", Password: []byte("x")},
		"unaligned size": {Path: filepath.Join(dir, "c"), Size: "5000", Password: []byte("x")},
		"no password":    {Path: filepath.Join(dir, "d"), Size: "64K"},
		"missing dir":    {Path: filepath.Join(dir, "no", "such", "e"), Size: "64K", Password: []byte("x")},
	} {
		if code, err := s.Create(req); err == nil || code != "" {
			t.Errorf("%s: got %q, %v; want a refusal", name, code, err)
		}
	}
}

func TestFailedCreationLeavesNothingBehind(t *testing.T) {
	boom := errors.New("format failed")
	orig := createContainer
	defer func() { createContainer = orig }()
	createContainer = func(theca.Backing, theca.CreateOptions) (*theca.Container, string, error) {
		return nil, "", boom
	}
	path := filepath.Join(t.TempDir(), "f")
	if _, err := (&Service{Hint: 1}).Create(CreateRequest{Path: path, Size: "64K", Password: []byte("x")}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the half-made file was left behind: %v", err)
	}
}

func TestCreateNeverOverwrites(t *testing.T) {
	path, _ := newContainer(t, PolicyAny)
	if _, err := (&Service{Hint: 1}).Create(CreateRequest{Path: path, Size: "64K", Password: []byte("x")}); !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want it to refuse an existing file", err)
	}
}

func TestCreateReturnsCodeOnce(t *testing.T) {
	_, code := newContainer(t, PolicyAny)
	if code == "" {
		t.Fatal("no recovery code")
	}
}

func TestOpenAndInfo(t *testing.T) {
	path, code := newContainer(t, PolicyAny)
	s := &Service{Hint: 1, OpenKey: opener(&fakeKey{})}

	sess, err := s.Open(context.Background(), UnlockRequest{Path: path, Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	i := sess.Info()
	if i.Size != 64<<10 || i.Sectors != 16 || i.RollbackProtected || i.SlotsUsed == 0 || i.JournalEntries == 0 {
		t.Errorf("Info = %+v", i)
	}
	if n, err := sess.Verify(); err != nil || n == 0 {
		t.Errorf("Verify = %d, %v", n, err)
	}
	if len(sess.partners) != 1 {
		t.Errorf("partners = %d, want 1", len(sess.partners))
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	// The recovery code alone opens a 1-of-n container, and is then the one partner.
	sess, err = s.Open(context.Background(), UnlockRequest{Path: path, Recovery: code})
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.partners) != 1 {
		t.Errorf("partners = %d, want 1", len(sess.partners))
	}
	sess.Close()
}

func TestOpenTwoOfThree(t *testing.T) {
	path, code := newContainer(t, PolicyTwoOfThree)
	s := &Service{Hint: 1, OpenKey: opener(&fakeKey{})}
	if _, err := s.Open(context.Background(), UnlockRequest{Path: path, Password: []byte("pw")}); !errors.Is(err, theca.ErrNoMatch) {
		t.Fatalf("password alone: err = %v, want ErrNoMatch", err)
	}
	sess, err := s.Open(context.Background(), UnlockRequest{Path: path, Password: []byte("pw"), Recovery: code})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if len(sess.partners) != 2 {
		t.Errorf("partners = %d, want 2", len(sess.partners))
	}
}

func TestOpenRefusals(t *testing.T) {
	path, _ := newContainer(t, PolicyAny)
	k := &fakeKey{}
	s := &Service{Hint: 1, OpenKey: opener(k)}
	ctx := context.Background()

	if _, err := s.Open(ctx, UnlockRequest{Path: path + ".none", Password: []byte("pw")}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
	short := filepath.Join(t.TempDir(), "short")
	if err := os.WriteFile(short, []byte("tiny"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, UnlockRequest{Path: short, Password: []byte("pw")}); err == nil {
		t.Error("a file shorter than the salt was accepted")
	}
	if _, err := s.Open(ctx, UnlockRequest{Path: path, Password: []byte("wrong")}); !errors.Is(err, theca.ErrNoMatch) {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := (&Service{Hint: 1, OpenKey: noKey}).Open(ctx, UnlockRequest{Path: path, Password: []byte("pw"), UseKey: true}); !errors.Is(err, errNoKey) {
		t.Errorf("no key: %v", err)
	}
}

func TestOpenWithKeyReleasesIt(t *testing.T) {
	path, _ := newContainer(t, PolicyAny)
	k := &fakeKey{}
	sess, err := (&Service{Hint: 1, OpenKey: opener(k)}).Open(context.Background(), UnlockRequest{Path: path, Password: []byte("pw"), UseKey: true})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if !k.closed {
		t.Error("the key was not released after unlocking")
	}
}

func TestFactorFailuresCloseTheSession(t *testing.T) {
	path, code := newContainer(t, PolicyAny)
	boom := errors.New("kdf failed")
	pf, rf := passwordFactor, recoveryFactor
	defer func() { passwordFactor, recoveryFactor = pf, rf }()

	passwordFactor = func(int, []byte, []byte) (theca.Factor, error) { return theca.Factor{}, boom }
	if _, err := (&Service{Hint: 1}).Open(context.Background(), UnlockRequest{Path: path, Password: []byte("pw")}); !errors.Is(err, boom) {
		t.Errorf("password factor: %v", err)
	}
	passwordFactor = pf
	recoveryFactor = func(string) (theca.Factor, error) { return theca.Factor{}, boom }
	if _, err := (&Service{Hint: 1}).Open(context.Background(), UnlockRequest{Path: path, Recovery: code}); !errors.Is(err, boom) {
		t.Errorf("recovery factor: %v", err)
	}
}

func TestImportExportRoundTrip(t *testing.T) {
	path, _ := newContainer(t, PolicyAny)
	sess, err := (&Service{Hint: 1}).Open(context.Background(), UnlockRequest{Path: path, Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	dir := t.TempDir()
	data := bytes.Repeat([]byte("theca!"), 5000) // 30000 bytes, within 64 KiB
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var last, total int64
	n, err := sess.Import(src, func(done, tot int64) { last, total = done, tot })
	if err != nil || n != int64(len(data)) {
		t.Fatalf("Import = %d, %v", n, err)
	}
	if last != n || total != n {
		t.Errorf("progress ended at %d of %d, want %d of %d", last, total, n, n)
	}
	if _, err := sess.Import(src, nil); err != nil { // nil progress is allowed
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "dst")
	last, total = 0, 0
	n, err = sess.Export(dst, func(done, tot int64) { last, total = done, tot })
	if err != nil || n != 64<<10 {
		t.Fatalf("Export = %d, %v", n, err)
	}
	if last != n || total != n {
		t.Errorf("progress ended at %d of %d", last, total)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:len(data)], data) {
		t.Error("what was exported is not what was imported")
	}
	if st, _ := os.Stat(dst); st.Mode().Perm() != 0o600 {
		t.Errorf("export mode = %v, want 0600", st.Mode().Perm())
	}
	// Plaintext is never written over an existing file.
	if _, err := sess.Export(dst, nil); !errors.Is(err, os.ErrExist) {
		t.Errorf("export over an existing file: %v", err)
	}
	if got2, _ := os.ReadFile(dst); !bytes.Equal(got, got2) {
		t.Error("the refused export changed the existing file")
	}
}

func TestImportRefusals(t *testing.T) {
	path, _ := newContainer(t, PolicyAny)
	sess, err := (&Service{Hint: 1}).Open(context.Background(), UnlockRequest{Path: path, Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	dir := t.TempDir()

	if _, err := sess.Import(filepath.Join(dir, "none"), nil); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing source: %v", err)
	}
	if _, err := sess.Import(dir, nil); err == nil {
		t.Error("a directory was imported")
	}
	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, make([]byte, 64<<10+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := sess.Import(big, nil); !errors.Is(err, theca.ErrRange) || n != 0 {
		t.Errorf("oversized: %d, %v", n, err)
	}
}

func TestExportRefusesAMissingDirectory(t *testing.T) {
	path, _ := newContainer(t, PolicyAny)
	sess, err := (&Service{Hint: 1}).Open(context.Background(), UnlockRequest{Path: path, Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := sess.Export(filepath.Join(t.TempDir(), "no", "dst"), nil); err == nil {
		t.Error("export into a missing directory succeeded")
	}
}

// brokenVolume fails where a real one rarely does.
type brokenVolume struct {
	volume
	readErr, writeErr, syncErr, enrollErr error
	size                                  int64
}

func (b *brokenVolume) ReadAt(p []byte, off int64) (int, error)  { return 0, b.readErr }
func (b *brokenVolume) WriteAt(p []byte, off int64) (int, error) { return 0, b.writeErr }
func (b *brokenVolume) Sync() error                              { return b.syncErr }
func (b *brokenVolume) Size() int64                              { return b.size }
func (b *brokenVolume) Close() error                             { return nil }
func (b *brokenVolume) EnrollFIDO2(context.Context, theca.FIDO2, ...theca.Factor) error {
	return b.enrollErr
}

func TestIOFailuresAreReported(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("disk on fire")

	sess := &Session{c: &brokenVolume{writeErr: boom, size: 4096}, f: f}
	if _, err := sess.Import(src, nil); !errors.Is(err, boom) {
		t.Errorf("write failure: %v", err)
	}
	sess.c = &brokenVolume{syncErr: boom, size: 4}
	// The write "succeeds" with zero bytes, which io.Copy reports as short.
	if _, err := sess.Import(src, nil); err == nil {
		t.Error("a write that stored nothing was accepted")
	}

	dst := filepath.Join(dir, "dst")
	sess.c = &brokenVolume{readErr: boom, size: 4096}
	if _, err := sess.Export(dst, nil); !errors.Is(err, boom) {
		t.Errorf("read failure: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("the partial plaintext export was left on disk: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Error(err)
	}
}

func TestSyncFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "empty")
	if err := os.WriteFile(src, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("sync failed")
	sess := &Session{c: &brokenVolume{syncErr: boom, size: 4096}}
	if n, err := sess.Import(src, nil); !errors.Is(err, boom) || n != 0 {
		t.Errorf("Import = %d, %v", n, err)
	}
}

func TestEnrollKey(t *testing.T) {
	path, code := newContainer(t, PolicyAny)
	k := &fakeKey{}
	s := &Service{Hint: 1, OpenKey: opener(k)}
	ctx := context.Background()

	sess, err := s.Open(ctx, UnlockRequest{Path: path, Password: []byte("pw"), Recovery: code})
	if err != nil {
		t.Fatal(err)
	}
	before := sess.Info().SlotsUsed
	n, err := sess.EnrollKey(ctx, nil)
	if err != nil || n != 2 || k.enrolled != 1 || !k.closed {
		t.Fatalf("EnrollKey = %d, %v (enrolled %d, closed %v)", n, err, k.enrolled, k.closed)
	}
	if after := sess.Info().SlotsUsed; after <= before {
		t.Errorf("slots used %d -> %d: nothing was stored", before, after)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	// What was stored survives a reopening.
	sess, err = s.Open(ctx, UnlockRequest{Path: path, Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if sess.Info().SlotsUsed <= before {
		t.Error("the enrolled slots were lost")
	}
}

func TestEnrollKeyRefusals(t *testing.T) {
	ctx := context.Background()
	// No partner factor: nothing to pair the key with.
	if _, err := (&Session{openKey: opener(&fakeKey{})}).EnrollKey(ctx, nil); err == nil {
		t.Error("enrolled with no partner")
	}
	one := []theca.Factor{theca.FIDO2Factor(make([]byte, 32))}
	if _, err := (&Session{partners: one, openKey: noKey}).EnrollKey(ctx, nil); !errors.Is(err, errNoKey) {
		t.Errorf("no key: %v", err)
	}
	boom := errors.New("refused")
	sess := &Session{partners: one, openKey: opener(&fakeKey{}), c: &brokenVolume{enrollErr: boom}}
	if _, err := sess.EnrollKey(ctx, nil); !errors.Is(err, boom) {
		t.Errorf("enroll failure: %v", err)
	}
}

func TestOpenSecurityKeyAnswersPlainly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	k, err := OpenSecurityKey(ctx, func() (string, error) { return "", io.EOF })
	if err == nil {
		k.Close() // a key happened to be attached
		return
	}
	if err.Error() == "" {
		t.Error("an empty error is no explanation")
	}
}

func TestKeyOrError(t *testing.T) {
	if k, err := keyOrError(&fido2.Device{}, nil); err != nil || k == nil {
		t.Errorf("a device was lost: %v, %v", k, err)
	}
	boom := errors.New("no key")
	if k, err := keyOrError(nil, boom); !errors.Is(err, boom) || k != nil {
		t.Errorf("a failure came back as a key: %v, %v", k, err)
	}
}

func TestNew(t *testing.T) {
	if New().OpenKey == nil {
		t.Fatal("New has no key opener")
	}
}
