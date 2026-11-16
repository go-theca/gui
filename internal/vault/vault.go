// SPDX-License-Identifier: BSD-3-Clause

// Package vault is the thin layer between the GUI and the Theca library
// (github.com/go-theca/theca). It mirrors what the theca command does, so the
// two tools agree: the create flow, unlocking with a password and/or a recovery
// code and/or a FIDO2 key, info, verify, import at offset 0, export, and
// enrolling a security key.
//
// Nothing in here draws anything or knows about a widget; the ViewModels reach
// it through an interface, which is what lets them be tested without a file.
package vault

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/go-theca/theca"
	"github.com/go-theca/theca/fido2"
)

// RollbackNotice is the note the theca command prints at every unlock and at
// creation (DESIGN 5.4): without a hardware counter an older copy of the
// container would be accepted.
const RollbackNotice = "this container has no rollback counter: an older copy of it would be accepted"

// SectorSize is the logical sector size; a container's size is a multiple of it.
const SectorSize = theca.SectorSize

// saltSize is the length of the salt at the start of every container file,
// which the password factor is derived with.
const saltSize = 32

// chunk is how much is copied at a time by import and export, so that a long
// copy can report its progress.
const chunk = 1 << 20

// The library calls, replaceable by tests: a creation that fails after the file
// was made, or a derivation that fails after the container has opened with the
// same secrets, cannot be provoked otherwise.
var (
	createContainer = theca.Create
	passwordFactor  = theca.PasswordFactor
	recoveryFactor  = theca.RecoveryFactor
)

// Policy chooses how a new container is unlocked.
type Policy int

const (
	// PolicyAny: the password alone, or the recovery code alone, opens it.
	PolicyAny Policy = iota
	// PolicyTwoOfThree: the password and the recovery code are both needed.
	PolicyTwoOfThree
)

// Info describes an unlocked container.
type Info struct {
	Size              uint64
	Sectors           uint64
	Epoch             uint64
	SlotsUsed         int
	JournalEntries    uint32
	RollbackProtected bool
}

// Key is an attached FIDO2 security key that must be released.
type Key interface {
	theca.FIDO2
	Close() error
}

// KeyOpener opens the attached security key. pin is asked for the key's PIN,
// and only when the key has no built-in user verification.
type KeyOpener func(ctx context.Context, pin func() (string, error)) (Key, error)

// OpenSecurityKey is the real KeyOpener. The FIDO2 transport exists on macOS
// only for now; elsewhere the library returns an error, which is shown as is.
func OpenSecurityKey(ctx context.Context, pin func() (string, error)) (Key, error) {
	return keyOrError(fido2.Open(ctx, pin))
}

// keyOrError turns the library's (*Device, error) into a Key without the
// typed-nil trap: a failed open must yield a nil interface, not a nil pointer
// wearing one.
func keyOrError(d *fido2.Device, err error) (Key, error) {
	if err != nil {
		return nil, err
	}
	return d, nil
}

// Service creates and opens containers.
type Service struct {
	// Hint is the Argon2id profile the password is stretched with (DESIGN 6.1):
	// 0 is the theca command's default, and what a container made by it uses
	// (2 GiB of memory); 1 is RFC 9106's second option (64 MiB). A container
	// is opened with the profile it was made with, so the GUI keeps 0 to agree
	// with the command; tests use 1 to stay light.
	Hint int
	// OpenKey opens the security key; tests replace it.
	OpenKey KeyOpener
}

// New returns a Service wired to the real security key.
func New() *Service { return &Service{OpenKey: OpenSecurityKey} }

// ParseSize reads a byte count with an optional K, M, G or T suffix (powers of
// 1024), like the theca command's --size.
func ParseSize(s string) (uint64, error) {
	mult := uint64(1)
	t := strings.ToUpper(strings.TrimSpace(s))
	for i, suf := range []string{"K", "M", "G", "T"} {
		if strings.HasSuffix(t, suf) {
			mult = 1 << (10 * (i + 1))
			t = strings.TrimSuffix(t, suf)
			break
		}
	}
	n, err := strconv.ParseUint(t, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("size %q: %w", s, err)
	}
	if n > ^uint64(0)/mult {
		return 0, fmt.Errorf("size %q overflows", s)
	}
	return n * mult, nil
}

// CreateRequest describes a container to create. Password is wiped by the
// caller, not here.
type CreateRequest struct {
	Path     string
	Size     string
	Policy   Policy
	Password []byte
}

// Create formats a new container and returns its recovery code, which is shown
// once and never stored. The code is returned even when closing the file
// fails afterwards: the container is formatted, and the code is the only copy
// there will ever be.
func (s *Service) Create(req CreateRequest) (string, error) {
	n, err := ParseSize(req.Size)
	if err != nil {
		return "", err
	}
	if n == 0 || n%SectorSize != 0 {
		return "", fmt.Errorf("size must be a positive multiple of %d bytes", SectorSize)
	}
	pol := theca.PolicyAny
	if req.Policy == PolicyTwoOfThree {
		pol = theca.PolicyTwoOfThree
	}
	if len(req.Password) == 0 {
		return "", errors.New("a password is required")
	}
	f, err := os.OpenFile(req.Path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	c, code, err := createContainer(theca.FileBacking{File: f}, theca.CreateOptions{
		Sectors:  n / SectorSize,
		Hint:     s.Hint,
		Password: req.Password,
		Policy:   pol,
	})
	if err != nil {
		f.Close()
		os.Remove(req.Path)
		return "", err
	}
	return code, errors.Join(c.Close(), f.Close())
}

// UnlockRequest carries the factors the user presents. Password is wiped by the
// caller, not here.
type UnlockRequest struct {
	Path     string
	Password []byte
	Recovery string
	UseKey   bool
	// PIN is asked, from another goroutine, when the key wants its PIN.
	PIN func() (string, error)
}

// Open unlocks the container at req.Path for reading and writing.
func (s *Service) Open(ctx context.Context, req UnlockRequest) (*Session, error) {
	f, err := os.OpenFile(req.Path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	sess, err := s.unlock(ctx, f, req)
	if err != nil {
		f.Close()
		return nil, err
	}
	return sess, nil
}

func (s *Service) unlock(ctx context.Context, f *os.File, req UnlockRequest) (*Session, error) {
	salt := make([]byte, saltSize)
	if _, err := f.ReadAt(salt, 0); err != nil {
		return nil, fmt.Errorf("%s: not a container: %w", req.Path, err)
	}
	u := theca.Unlock{Hint: s.Hint, Password: req.Password, Recovery: req.Recovery}
	if req.UseKey {
		dev, err := s.OpenKey(ctx, req.PIN)
		if err != nil {
			return nil, err
		}
		defer dev.Close()
		u.FIDO = dev
	}
	c, err := theca.Open(ctx, theca.FileBacking{File: f}, u, theca.OpenOptions{})
	if err != nil {
		return nil, err
	}
	sess := &Session{c: c, f: f, openKey: s.OpenKey}
	// The partner factors, derived from the same secrets that opened it: they
	// are what enrolling a key pairs it with. They are key material, not the
	// secrets, and are wiped when the session closes.
	if len(req.Password) > 0 {
		pw, err := passwordFactor(u.Hint, req.Password, salt)
		if err != nil {
			sess.Close()
			return nil, err
		}
		sess.partners = append(sess.partners, pw)
	}
	if req.Recovery != "" {
		rc, err := recoveryFactor(req.Recovery)
		if err != nil {
			sess.Close()
			return nil, err
		}
		sess.partners = append(sess.partners, rc)
	}
	return sess, nil
}

// volume is what a Session needs of an open container; *theca.Container is
// one, and a test can stand in a failing one.
type volume interface {
	io.ReaderAt
	io.WriterAt
	Info() theca.Info
	Verify() (uint64, error)
	Sync() error
	Size() int64
	Close() error
	EnrollFIDO2(ctx context.Context, dev theca.FIDO2, partners ...theca.Factor) error
}

// Session is an unlocked container.
type Session struct {
	c        volume
	f        *os.File
	openKey  KeyOpener
	partners []theca.Factor
}

// Info describes the container.
func (s *Session) Info() Info {
	i := s.c.Info()
	return Info{
		Size:              i.Size,
		Sectors:           i.Sectors,
		Epoch:             i.Epoch,
		SlotsUsed:         i.SlotsUsed,
		JournalEntries:    i.JournalEntries,
		RollbackProtected: i.RollbackProtected,
	}
}

// Verify authenticates every sector, metadata block and tree node, and returns
// the number of sectors checked.
func (s *Session) Verify() (uint64, error) { return s.c.Verify() }

// Import writes the file at path into the volume at offset 0 and commits. A
// file larger than the volume is refused before anything is written. The copy
// is not one atomic transaction: on an error the bytes written before it are
// kept, as with the theca command. progress, which may be nil, is told how many
// bytes were written of the total.
func (s *Session) Import(path string, progress func(done, total int64)) (int64, error) {
	src, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return 0, fmt.Errorf("%s: not a regular file", path)
	}
	total := st.Size()
	if total > s.c.Size() {
		return 0, fmt.Errorf("%s is %d bytes, the volume holds %d: %w", path, total, s.c.Size(), theca.ErrRange)
	}
	buf := make([]byte, chunk)
	defer clear(buf)
	n, err := io.CopyBuffer(io.NewOffsetWriter(s.c, 0), &counter{r: src, total: total, fn: progress}, buf)
	if err != nil {
		return n, err
	}
	return n, s.c.Sync()
}

// counter reports how many bytes have gone through a reader.
type counter struct {
	r     io.Reader
	done  int64
	total int64
	fn    func(done, total int64)
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if n > 0 && c.fn != nil {
		c.fn(c.done, c.total)
	}
	return n, err
}

// Export writes the whole decrypted volume to a new file at path, which must
// not exist yet (it holds plaintext: it is created readable by its owner only,
// and never overwrites). On an error the partial file is removed. progress,
// which may be nil, is told how many bytes were written of the total.
func (s *Session) Export(path string, progress func(done, total int64)) (n int64, err error) {
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := dst.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(path)
		}
	}()
	total := s.c.Size()
	buf := make([]byte, chunk)
	defer clear(buf)
	return io.CopyBuffer(&countWriter{w: dst, total: total, fn: progress}, io.NewSectionReader(s.c, 0, total), buf)
}

// countWriter reports how many bytes have gone through a writer.
type countWriter struct {
	w     io.Writer
	done  int64
	total int64
	fn    func(done, total int64)
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.done += int64(n)
	if n > 0 && c.fn != nil {
		c.fn(c.done, c.total)
	}
	return n, err
}

// EnrollKey pairs the attached security key with the password and the recovery
// code that opened this session, and returns how many factors it is paired
// with. The key is touched twice: to register, then to confirm.
func (s *Session) EnrollKey(ctx context.Context, pin func() (string, error)) (int, error) {
	if len(s.partners) == 0 {
		return 0, errors.New("enrolling a key needs the password or the recovery code: unlock with one")
	}
	dev, err := s.openKey(ctx, pin)
	if err != nil {
		return 0, err
	}
	defer dev.Close()
	if err := s.c.EnrollFIDO2(ctx, dev, s.partners...); err != nil {
		return 0, err
	}
	return len(s.partners), nil
}

// Close commits, wipes the keys and releases the file.
func (s *Session) Close() error {
	for i := range s.partners {
		s.partners[i].Zeroize()
	}
	s.partners = nil
	return errors.Join(s.c.Close(), s.f.Close())
}
