// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-widgets/mvvm"

	"github.com/go-theca/gui/internal/vault"
)

// --- fakes ------------------------------------------------------------------

type fakeSession struct {
	info vault.Info

	verifyN                                    uint64
	verifyErr, importErr, exportErr, enrollErr error
	closeErr                                   error
	enrolled                                   int
	enrollPW, enrollRec                        string
	enrollRaw                                  []byte

	imported, exported string
	closed             int

	sh         *fakeShare
	shareErr   error
	shareCalls int
	events     *[]string // what was stopped and closed, in order
}

func (s *fakeSession) Info() vault.Info { return s.info }
func (s *fakeSession) Verify() (uint64, error) {
	return s.verifyN, s.verifyErr
}
func (s *fakeSession) Import(path string, progress func(done, total int64)) (int64, error) {
	s.imported = path
	progress(0, 0) // an empty file
	progress(50, 100)
	progress(100, 100)
	return 100, s.importErr
}
func (s *fakeSession) Export(path string, progress func(done, total int64)) (int64, error) {
	s.exported = path
	progress(4096, 4096)
	return 4096, s.exportErr
}
func (s *fakeSession) EnrollKey(ctx context.Context, pw []byte, rec string, pin func() (string, error)) (int, error) {
	s.enrollPW, s.enrollRaw, s.enrollRec = string(pw), pw, rec
	if s.enrollErr != nil {
		return 0, s.enrollErr
	}
	got, err := pin()
	if err != nil {
		return 0, err
	}
	if got != "1234" {
		return 0, errors.New("wrong pin " + got)
	}
	s.enrolled++
	return 2, nil
}
func (s *fakeSession) Close() error {
	s.closed++
	if s.events != nil {
		*s.events = append(*s.events, "close")
	}
	return s.closeErr
}
func (s *fakeSession) Share(context.Context) (vault.Share, error) {
	s.shareCalls++
	if s.shareErr != nil {
		return nil, s.shareErr
	}
	return s.sh, nil
}

type fakeService struct {
	createReq vault.CreateRequest
	createPW  []byte // what the password was when Create was called
	createRaw []byte // the slice itself, to see it wiped afterwards
	code      string
	createErr error

	openReq vault.UnlockRequest
	openPW  []byte
	openRaw []byte
	sess    *fakeSession
	openErr error
	onOpen  func(ctx context.Context, req vault.UnlockRequest)
}

func (f *fakeService) Create(req vault.CreateRequest) (string, error) {
	f.createReq = req
	f.createRaw = req.Password
	f.createPW = append([]byte(nil), req.Password...)
	return f.code, f.createErr
}

func (f *fakeService) Open(ctx context.Context, req vault.UnlockRequest) (Session, error) {
	f.openReq = req
	f.openRaw = req.Password
	f.openPW = append([]byte(nil), req.Password...)
	if f.onOpen != nil {
		f.onOpen(ctx, req)
	}
	if f.openErr != nil {
		return nil, f.openErr
	}
	return f.sess, nil
}

// newApp builds an App whose background work runs at once, so a test only has
// to drain the queue.
func newApp(svc Service) (*App, *mvvm.Queue) {
	q := mvvm.NewQueue(nil)
	return New(svc, q, func(f func()) { f() }), q
}

func messages(a *App) string { return strings.Join(a.Messages.Slice(), "\n") }

// flat is the status area read as one line, wrapping undone.
func flat(a *App) string { return strings.Join(a.Messages.Slice(), " ") }

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// waitFor drains the queue until cond holds; the PIN tests run real goroutines.
func waitFor(t *testing.T, q *mvvm.Queue, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		q.Drain()
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

// --- Wrap -------------------------------------------------------------------

func TestWrap(t *testing.T) {
	for _, c := range []struct {
		in    string
		width int
		want  []string
	}{
		{"", 10, nil},
		{"short", 10, []string{"short"}},
		{"one two three", 7, []string{"one two", "three"}},
		{"exactly ten", 11, []string{"exactly ten"}},
		{"abcdefghijkl", 5, []string{"abcde", "fghij", "kl"}},
		{"ab abcdefghijkl", 5, []string{"ab", "abcde", "fghij", "kl"}},
		{"first\nsecond line", 20, []string{"first", "second line"}},
		{"\n\n", 5, nil},
	} {
		if got := Wrap(c.in, c.width); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Wrap(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
}

// --- Home -------------------------------------------------------------------

func TestHome(t *testing.T) {
	a, _ := newApp(&fakeService{})
	if a.Screen.Get() != ScreenHome {
		t.Fatalf("starts on %q", a.Screen.Get())
	}
	if a.GoUnlock.CanExecute() {
		t.Error("Unlock is offered with no path")
	}
	a.Path.Set("  ")
	if a.GoUnlock.CanExecute() {
		t.Error("Unlock is offered with a blank path")
	}
	a.Path.Set("/x/y.theca")
	if !a.GoUnlock.CanExecute() {
		t.Fatal("Unlock is not offered with a path")
	}
	a.GoUnlock.Execute()
	if a.Screen.Get() != ScreenUnlock {
		t.Errorf("screen = %q", a.Screen.Get())
	}
	a.Unlock.Back.Execute()
	a.GoCreate.Execute()
	if a.Screen.Get() != ScreenCreate || a.Create.Path.Get() != "/x/y.theca" {
		t.Errorf("screen = %q, create path = %q", a.Screen.Get(), a.Create.Path.Get())
	}
	a.Create.Back.Execute()
	if a.Screen.Get() != ScreenHome {
		t.Errorf("screen = %q", a.Screen.Get())
	}
}

// --- Create -----------------------------------------------------------------

func fillCreate(a *App) {
	a.Create.Path.Set(" /tmp/new.theca ")
	a.Create.Size.Set("64K")
	a.Create.Password.Set("hunter2")
	a.Create.Confirm.Set("hunter2")
}

func TestCreateFlow(t *testing.T) {
	svc := &fakeService{code: "AAAA-BBBB-CCCC"}
	a, q := newApp(svc)
	a.GoCreate.Execute()
	c := a.Create
	if c.Size.Get() != DefaultSize || c.Policy.Get() != PolicyAny {
		t.Errorf("defaults: %q, %d", c.Size.Get(), c.Policy.Get())
	}
	if c.Submit.CanExecute() {
		t.Fatal("an empty form can be submitted")
	}
	fillCreate(a)
	c.Policy.Set(PolicyTwoOfThree)
	if !c.Submit.CanExecute() {
		t.Fatal("a full form cannot be submitted")
	}
	c.Submit.Execute()
	if !c.Busy.Get() || c.Submit.CanExecute() || c.Back.CanExecute() {
		t.Error("the form is not locked while creating")
	}
	if c.Password.Get() != "" || c.Confirm.Get() != "" {
		t.Error("the entries still hold the secret after submit")
	}
	q.Drain()

	if got := svc.createReq; got.Path != "/tmp/new.theca" || got.Size != "64K" || got.Policy != vault.PolicyTwoOfThree {
		t.Errorf("request = %+v", got)
	}
	if string(svc.createPW) != "hunter2" {
		t.Errorf("the library got password %q", svc.createPW)
	}
	if !allZero(svc.createRaw) {
		t.Error("the password bytes were not wiped after use")
	}
	if a.Screen.Get() != ScreenRecovery || c.Recovery.Get() != "AAAA-BBBB-CCCC" {
		t.Fatalf("screen = %q, recovery = %q", a.Screen.Get(), c.Recovery.Get())
	}
	if !strings.Contains(flat(a), vault.RollbackNotice) {
		t.Errorf("the rollback notice is missing: %q", flat(a))
	}

	// The screen cannot be left until the code is acknowledged.
	if c.Done.CanExecute() {
		t.Fatal("Done is offered before the acknowledgement")
	}
	c.Done.Execute() // refused
	if a.Screen.Get() != ScreenRecovery {
		t.Fatal("left the Recovery screen without acknowledging")
	}
	c.Acked.Set(true)
	if !c.Done.CanExecute() {
		t.Fatal("Done is not offered after the acknowledgement")
	}
	c.Done.Execute()
	if a.Screen.Get() != ScreenHome || c.Recovery.Get() != "" || c.Acked.Get() {
		t.Errorf("screen = %q, recovery %q, acked %v: the code outlived its screen", a.Screen.Get(), c.Recovery.Get(), c.Acked.Get())
	}
	if a.Path.Get() != "/tmp/new.theca" || !strings.Contains(messages(a), "/tmp/new.theca") {
		t.Errorf("path = %q, messages = %q", a.Path.Get(), messages(a))
	}
	if strings.Contains(messages(a), "AAAA") {
		t.Error("the recovery code is in the status area")
	}
}

func TestCreateDefaultPolicyIsAny(t *testing.T) {
	svc := &fakeService{code: "X"}
	a, q := newApp(svc)
	fillCreate(a)
	a.Create.Submit.Execute()
	q.Drain()
	if svc.createReq.Policy != vault.PolicyAny {
		t.Errorf("policy = %v", svc.createReq.Policy)
	}
}

func TestCreateMismatchedPasswords(t *testing.T) {
	svc := &fakeService{code: "X"}
	a, _ := newApp(svc)
	fillCreate(a)
	a.Create.Confirm.Set("hunter3")
	a.Create.Submit.Execute()
	if got := messages(a); got != "error: the passwords differ" {
		t.Errorf("messages = %q", got)
	}
	if svc.createRaw != nil || a.Create.Busy.Get() {
		t.Error("a mismatched pair reached the library")
	}
}

func TestCreateError(t *testing.T) {
	svc := &fakeService{createErr: errors.New("theca: no space")}
	a, q := newApp(svc)
	a.GoCreate.Execute()
	fillCreate(a)
	a.Create.Submit.Execute()
	q.Drain()
	if a.Screen.Get() != ScreenCreate || a.Create.Busy.Get() {
		t.Errorf("screen = %q, busy = %v", a.Screen.Get(), a.Create.Busy.Get())
	}
	if got := messages(a); got != "error: theca: no space" {
		t.Errorf("messages = %q", got)
	}
}

func TestCreateShowsTheCodeEvenWhenClosingFailed(t *testing.T) {
	svc := &fakeService{code: "AAAA-BBBB", createErr: errors.New("close failed")}
	a, q := newApp(svc)
	fillCreate(a)
	a.Create.Submit.Execute()
	q.Drain()
	if a.Screen.Get() != ScreenRecovery || a.Create.Recovery.Get() != "AAAA-BBBB" {
		t.Fatalf("the only copy of the code was not shown: %q, %q", a.Screen.Get(), a.Create.Recovery.Get())
	}
	m := flat(a)
	if !strings.Contains(m, "error: close failed") || !strings.Contains(m, vault.RollbackNotice) {
		t.Errorf("messages = %q", m)
	}
}

// --- Unlock -----------------------------------------------------------------

func fillUnlock(a *App) {
	a.Path.Set("/tmp/c.theca")
	a.GoUnlock.Execute()
	a.Unlock.Password.Set("hunter2")
}

func TestUnlockFlow(t *testing.T) {
	sess := &fakeSession{info: vault.Info{Size: 65536, Sectors: 16, Epoch: 3, SlotsUsed: 2, JournalEntries: 64}}
	svc := &fakeService{sess: sess}
	a, q := newApp(svc)
	u := a.Unlock
	a.Path.Set("/tmp/c.theca")
	a.GoUnlock.Execute()
	if u.Submit.CanExecute() {
		t.Fatal("unlocking is offered with no factor")
	}
	u.Recovery.Set("  ")
	if u.Submit.CanExecute() {
		t.Fatal("a blank recovery code counts as a factor")
	}
	u.Recovery.Set(" ABCD-EFGH ")
	u.Password.Set("hunter2")
	u.UseKey.Set(true)
	u.Submit.Execute()
	if !u.Busy.Get() || u.Submit.CanExecute() || u.Back.CanExecute() || !u.Cancel.CanExecute() {
		t.Error("the form is not locked while unlocking")
	}
	if u.Password.Get() != "" || u.Recovery.Get() != "" {
		t.Error("the entries still hold the secrets after submit")
	}
	if messages(a) != KeyTouchNotice {
		t.Errorf("messages = %q", messages(a))
	}
	q.Drain()

	r := svc.openReq
	if r.Path != "/tmp/c.theca" || r.Recovery != "ABCD-EFGH" || !r.UseKey || r.PIN == nil {
		t.Errorf("request = %+v", r)
	}
	if string(svc.openPW) != "hunter2" || !allZero(svc.openRaw) {
		t.Errorf("password %q, wiped afterwards: %v", svc.openPW, allZero(svc.openRaw))
	}
	if a.Screen.Get() != ScreenContainer {
		t.Fatalf("screen = %q", a.Screen.Get())
	}
	c := a.Container
	for got, want := range map[string]string{
		c.Size.Get():     "size: 65536 bytes (16 sectors)",
		c.Epoch.Get():    "epoch: 3",
		c.Slots.Get():    "slots used: 2 of 16",
		c.Journal.Get():  "journal entries: 64",
		c.Rollback.Get(): "rollback protected: false",
	} {
		if got != want {
			t.Errorf("info line %q, want %q", got, want)
		}
	}
	if got := flat(a); got != "note: "+vault.RollbackNotice {
		t.Errorf("the rollback notice is missing: %q", got)
	}
}

func TestUnlockRollbackProtectedHasNoNotice(t *testing.T) {
	svc := &fakeService{sess: &fakeSession{info: vault.Info{RollbackProtected: true}}}
	a, q := newApp(svc)
	fillUnlock(a)
	a.Unlock.Submit.Execute()
	q.Drain()
	if a.Screen.Get() != ScreenContainer || messages(a) != "" {
		t.Errorf("screen = %q, messages = %q", a.Screen.Get(), messages(a))
	}
	if a.Container.Rollback.Get() != "rollback protected: true" {
		t.Errorf("rollback line = %q", a.Container.Rollback.Get())
	}
}

func TestUnlockError(t *testing.T) {
	svc := &fakeService{openErr: errors.New("theca: no factor opened a keyslot")}
	a, q := newApp(svc)
	fillUnlock(a)
	a.Unlock.Submit.Execute()
	q.Drain()
	if a.Screen.Get() != ScreenUnlock || a.Unlock.Busy.Get() {
		t.Errorf("screen = %q, busy = %v", a.Screen.Get(), a.Unlock.Busy.Get())
	}
	if got := messages(a); got != "error: theca: no factor opened a keyslot" {
		t.Errorf("messages = %q", got)
	}
	if !allZero(svc.openRaw) {
		t.Error("the password bytes were not wiped after a failed unlock")
	}
	if svc.openReq.UseKey {
		t.Error("the key was used without being asked for")
	}
}

func TestUnlockCancel(t *testing.T) {
	var ctx context.Context
	svc := &fakeService{onOpen: func(c context.Context, _ vault.UnlockRequest) { ctx = c }, openErr: context.Canceled}
	a, q := newApp(svc)
	fillUnlock(a)
	a.Unlock.Submit.Execute()
	a.Unlock.Cancel.Execute()
	if ctx.Err() == nil {
		t.Error("Cancel did not cancel the unlock")
	}
	q.Drain()
	if a.Unlock.Cancel.CanExecute() {
		t.Error("Cancel is offered with nothing to cancel")
	}
}

func TestUnlockAsksForThePINOnlyWhenTheKeyDoes(t *testing.T) {
	sess := &fakeSession{}
	svc := &fakeService{sess: sess}
	svc.onOpen = func(_ context.Context, req vault.UnlockRequest) {
		pin, err := req.PIN()
		if err != nil || pin != "1234" {
			svc.openErr = errors.New("bad pin")
		}
	}
	q := mvvm.NewQueue(nil)
	a := New(svc, q, nil) // real goroutines
	fillUnlock(a)
	a.Unlock.UseKey.Set(true)
	if a.PIN.Needed.Get() || a.PIN.Submit.CanExecute() {
		t.Fatal("a PIN is asked for before the key wants one")
	}
	a.Unlock.Submit.Execute()
	waitFor(t, q, func() bool { return a.PIN.Needed.Get() })
	if !a.PIN.Submit.CanExecute() {
		t.Error("the PIN cannot be submitted while the key waits")
	}
	a.PIN.Value.Set("1234")
	a.PIN.Submit.Execute()
	if a.PIN.Value.Get() != "" || a.PIN.Needed.Get() {
		t.Error("the PIN was not cleared on submit")
	}
	waitFor(t, q, func() bool { return a.Screen.Get() == ScreenContainer })
	if a.PIN.Submit.CanExecute() {
		t.Error("Submit is still offered")
	}
}

func TestPINWaitEndsWhenCancelled(t *testing.T) {
	q := mvvm.NewQueue(nil)
	p := newPINPrompt(q)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		_, err := p.Ask(ctx)()
		res <- err
	}()
	waitFor(t, q, func() bool { return p.Needed.Get() })
	p.Value.Set("half-typed")
	cancel()
	if err := <-res; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	waitFor(t, q, func() bool { return !p.Needed.Get() })
	if p.Value.Get() != "" {
		t.Error("a half-typed PIN was kept")
	}
}

// --- Container --------------------------------------------------------------

func openContainer(t *testing.T, sess *fakeSession) (*App, *mvvm.Queue) {
	t.Helper()
	a, q := newApp(&fakeService{sess: sess})
	fillUnlock(a)
	a.Unlock.Submit.Execute()
	q.Drain()
	if a.Screen.Get() != ScreenContainer {
		t.Fatalf("screen = %q", a.Screen.Get())
	}
	return a, q
}

func TestVerify(t *testing.T) {
	sess := &fakeSession{verifyN: 16}
	a, q := openContainer(t, sess)
	c := a.Container
	c.Verify.Execute()
	if !c.Busy.Get() || c.Verify.CanExecute() || c.Close.CanExecute() || c.EnrollKey.CanExecute() {
		t.Error("the screen is not locked while verifying")
	}
	if c.Activity.Get() != "verifying every sector..." || c.Progress.Get() != 0 {
		t.Errorf("activity %q, progress %v", c.Activity.Get(), c.Progress.Get())
	}
	q.Drain()
	if c.Activity.Get() != "ok: 16 sectors verified" || c.Progress.Get() != 1 || c.Busy.Get() {
		t.Errorf("activity %q, progress %v, busy %v", c.Activity.Get(), c.Progress.Get(), c.Busy.Get())
	}

	sess.verifyErr = errors.New("theca: sector failed authentication")
	c.Verify.Execute()
	q.Drain()
	if c.Activity.Get() != "" {
		t.Errorf("activity %q after a failure", c.Activity.Get())
	}
	if got := messages(a); got != "error: theca: sector failed authentication" {
		t.Errorf("messages = %q", got)
	}
}

func TestImportExport(t *testing.T) {
	sess := &fakeSession{}
	a, q := openContainer(t, sess)
	c := a.Container
	if c.Import.CanExecute() || c.Export.CanExecute() {
		t.Fatal("copying is offered with no path")
	}
	c.ImportPath.Set("   ")
	if c.Import.CanExecute() {
		t.Fatal("a blank path counts")
	}
	c.ImportPath.Set(" /tmp/in.bin ")
	c.ExportPath.Set("/tmp/out.bin")
	c.Import.Execute()
	q.Drain()
	if sess.imported != "/tmp/in.bin" || c.Activity.Get() != "ok: 100 bytes written at 0" || c.Progress.Get() != 1 {
		t.Errorf("imported %q, activity %q, progress %v", sess.imported, c.Activity.Get(), c.Progress.Get())
	}
	c.Export.Execute()
	q.Drain()
	if sess.exported != "/tmp/out.bin" || c.Activity.Get() != "ok: 4096 bytes exported" {
		t.Errorf("exported %q, activity %q", sess.exported, c.Activity.Get())
	}

	sess.importErr = errors.New("theca: offset or length out of range")
	c.Import.Execute()
	q.Drain()
	if got := messages(a); got != "error: theca: offset or length out of range" {
		t.Errorf("messages = %q", got)
	}
	sess.exportErr = errors.New("file exists")
	c.Export.Execute()
	q.Drain()
	if got := messages(a); got != "error: file exists" {
		t.Errorf("messages = %q", got)
	}
}

func TestProgressIsPostedToTheUIGoroutine(t *testing.T) {
	a, q := openContainer(t, &fakeSession{})
	c := a.Container
	c.progress(50, 100)
	if c.Progress.Get() != 0 || q.Len() != 1 {
		t.Fatalf("progress %v, queued %d: the observable was touched off the UI goroutine", c.Progress.Get(), q.Len())
	}
	q.Drain()
	if c.Progress.Get() != 0.5 {
		t.Errorf("progress = %v", c.Progress.Get())
	}
	c.progress(0, 0)
	q.Drain()
	if c.Progress.Get() != 1 {
		t.Errorf("an empty copy is %v done", c.Progress.Get())
	}
}

func TestEnrollKey(t *testing.T) {
	sess := &fakeSession{}
	svc := &fakeService{sess: sess}
	q := mvvm.NewQueue(nil)
	a := New(svc, q, nil)
	fillUnlock(a)
	a.Unlock.Submit.Execute()
	waitFor(t, q, func() bool { return a.Screen.Get() == ScreenContainer })

	c := a.Container
	if c.EnrollKey.CanExecute() {
		t.Fatal("enrolment is offered without the password or the recovery code")
	}
	c.EnrollRecovery.Set("  ")
	if c.EnrollKey.CanExecute() {
		t.Fatal("a blank recovery code counts")
	}
	c.EnrollPassword.Set("hunter2")
	c.EnrollRecovery.Set(" ABCD ")
	c.EnrollKey.Execute()
	if c.EnrollPassword.Get() != "" || c.EnrollRecovery.Get() != "" {
		t.Error("the entries still hold the secrets after enrolment started")
	}
	if flat(a) != EnrollNotice {
		t.Errorf("messages = %q", flat(a))
	}
	waitFor(t, q, func() bool { return a.PIN.Needed.Get() })
	a.PIN.Value.Set("1234")
	a.PIN.Submit.Execute()
	waitFor(t, q, func() bool { return !c.Busy.Get() })
	if sess.enrolled != 1 || c.Activity.Get() != "ok: the key is paired with 2 factor(s)" {
		t.Errorf("enrolled %d, activity %q", sess.enrolled, c.Activity.Get())
	}
	if sess.enrollPW != "hunter2" || sess.enrollRec != "ABCD" || !allZero(sess.enrollRaw) {
		t.Errorf("library got %q, %q; password bytes wiped: %v", sess.enrollPW, sess.enrollRec, allZero(sess.enrollRaw))
	}

	sess.enrollErr = errors.New("fido2: the key was not touched in time")
	c.EnrollPassword.Set("hunter2")
	c.EnrollKey.Execute()
	waitFor(t, q, func() bool { return !c.Busy.Get() })
	if got := messages(a); got != "error: fido2: the key was not touched in time" {
		t.Errorf("messages = %q", got)
	}
}

func TestClose(t *testing.T) {
	sess := &fakeSession{}
	a, _ := openContainer(t, sess)
	c := a.Container
	c.ImportPath.Set("/a")
	c.ExportPath.Set("/b")
	c.Close.Execute()
	if sess.closed != 1 || a.Screen.Get() != ScreenHome || c.sess != nil {
		t.Errorf("closed %d, screen %q", sess.closed, a.Screen.Get())
	}
	if c.ImportPath.Get() != "" || c.ExportPath.Get() != "" || messages(a) != "" {
		t.Error("the container's state outlived it")
	}

	sess = &fakeSession{closeErr: errors.New("theca: commit failed")}
	a, _ = openContainer(t, sess)
	a.Container.Close.Execute()
	if a.Screen.Get() != ScreenHome || messages(a) != "error: theca: commit failed" {
		t.Errorf("screen %q, messages %q", a.Screen.Get(), messages(a))
	}
}

// --- Backend ----------------------------------------------------------------

func TestBackend(t *testing.T) {
	b := Backend{V: &vault.Service{Hint: 1}}
	path := filepath.Join(t.TempDir(), "c.theca")
	code, err := b.Create(vault.CreateRequest{Path: path, Size: "64K", Password: []byte("pw")})
	if err != nil || code == "" {
		t.Fatalf("Create = %q, %v", code, err)
	}
	if s, err := b.Open(context.Background(), vault.UnlockRequest{Path: path, Password: []byte("wrong")}); err == nil || s != nil {
		t.Fatalf("a wrong password opened it: %v, %v", s, err)
	}
	s, err := b.Open(context.Background(), vault.UnlockRequest{Path: path, Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	if s.Info().Size != 64<<10 {
		t.Errorf("size = %d", s.Info().Size)
	}
	if err := s.Close(); err != nil {
		t.Error(err)
	}
	if bytes.Contains([]byte(code), []byte(" ")) {
		t.Errorf("recovery code %q has a space", code)
	}
}
