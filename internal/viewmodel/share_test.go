// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/go-widgets/mvvm"
)

// fakeShare is a running share; its Stop closes Done, as the real one does.
type fakeShare struct {
	url, mount, addr string
	done             chan struct{}
	once             sync.Once
	err              error // Err, read after Done closes
	stopErr          error
	stopped          int
	events           *[]string
}

func newFakeShare() *fakeShare {
	return &fakeShare{url: "smb://127.0.0.1:5555/theca", mount: "/Volumes/theca", done: make(chan struct{})}
}

func (f *fakeShare) Addr() string {
	if f.addr != "" {
		return f.addr
	}
	return "127.0.0.1:5555"
}
func (f *fakeShare) Password() string      { return "s3cret-password" }
func (f *fakeShare) URL() string           { return f.url }
func (f *fakeShare) MountPath() string     { return f.mount }
func (f *fakeShare) Done() <-chan struct{} { return f.done }
func (f *fakeShare) Err() error            { return f.err }
func (f *fakeShare) Stop() error {
	f.stopped++
	if f.events != nil {
		*f.events = append(*f.events, "stop")
	}
	f.once.Do(func() { close(f.done) })
	if f.stopErr != nil {
		return f.stopErr
	}
	return f.err // as the real one: the error that ended the share
}

// end makes the share end by itself.
func (f *fakeShare) end(err error) {
	f.err = err
	f.once.Do(func() { close(f.done) })
}

// sharedContainer opens a container whose session shares through sh.
func sharedContainer(t *testing.T, sh *fakeShare) (*App, *mvvm.Queue, *fakeSession) {
	t.Helper()
	sess := &fakeSession{sh: sh}
	a, q := openContainer(t, sess)
	return a, q, sess
}

// startShare shares and drains, leaving the share running.
func startShare(t *testing.T, a *App, q *mvvm.Queue) {
	t.Helper()
	a.Container.Share.Toggle.Execute()
	q.Drain()
	if a.Container.Share.State.Get() != ShareShared {
		t.Fatalf("state %q, messages %q", a.Container.Share.State.Get(), flat(a))
	}
}

func TestShareToggle(t *testing.T) {
	sh := newFakeShare()
	a, q, sess := sharedContainer(t, sh)
	s := a.Container.Share
	if s.State.Get() != ShareIdle || s.Label.Get() != LabelShare || s.URL.Get() != "" || s.MountPath.Get() != "" || s.Open.CanExecute() {
		t.Fatalf("not idle at the start: %q %q", s.State.Get(), s.Label.Get())
	}
	s.Toggle.Execute()
	if s.State.Get() != ShareStarting || s.Toggle.CanExecute() || a.Container.Close.CanExecute() {
		t.Error("the screen is not locked while starting")
	}
	q.Drain()
	if s.State.Get() != ShareShared || s.Label.Get() != LabelStop || s.URL.Get() != sh.url || s.MountPath.Get() != "/Volumes/theca" {
		t.Fatalf("shared: %q %q %q %q", s.State.Get(), s.Label.Get(), s.URL.Get(), s.MountPath.Get())
	}
	if !s.Toggle.CanExecute() || !s.Open.CanExecute() || sess.shareCalls != 1 {
		t.Errorf("toggle %v open %v calls %d", s.Toggle.CanExecute(), s.Open.CanExecute(), sess.shareCalls)
	}
	// The password is not in anything the ViewModel shows.
	for _, shown := range []string{s.URL.Get(), s.MountPath.Get(), s.Error.Get(), flat(a)} {
		if strings.Contains(shown, "secret") {
			t.Errorf("%q shows the password", shown)
		}
	}

	s.Toggle.Execute() // stop
	if s.Toggle.CanExecute() {
		t.Error("the screen is not locked while stopping")
	}
	q.Drain()
	if sh.stopped != 1 || s.State.Get() != ShareIdle || s.Label.Get() != LabelShare || s.URL.Get() != "" || s.MountPath.Get() != "" || s.Open.CanExecute() {
		t.Fatalf("stopped %d, state %q, label %q", sh.stopped, s.State.Get(), s.Label.Get())
	}
	// Stopping by hand is not a share that ended by itself.
	q.Drain()
	if s.State.Get() != ShareIdle || s.Error.Get() != "" {
		t.Errorf("state %q, error %q", s.State.Get(), s.Error.Get())
	}
	if a.Container.sess == nil || sess.closed != 0 {
		t.Error("stopping the share closed the container")
	}
}

func TestShareOpenInFileManager(t *testing.T) {
	sh := newFakeShare()
	a, q, _ := sharedContainer(t, sh)
	s := a.Container.Share
	var opened []string
	s.reveal = func(p string) error { opened = append(opened, p); return nil }
	startShare(t, a, q)
	s.Open.Execute()
	if len(opened) != 1 || opened[0] != "/Volumes/theca" {
		t.Fatalf("opened %q", opened)
	}
	s.reveal = func(string) error { return errors.New("no file manager") }
	s.Open.Execute()
	if flat(a) != "error: no file manager" {
		t.Errorf("messages %q", flat(a))
	}
	if s.State.Get() != ShareShared {
		t.Error("a file manager that did not open ended the share")
	}
}

func TestShareRefused(t *testing.T) {
	a, q := openContainer(t, &fakeSession{shareErr: errors.New("nothing to share, the container holds no exFAT filesystem")})
	s := a.Container.Share
	s.Toggle.Execute()
	q.Drain()
	if s.State.Get() != ShareError || s.Label.Get() != LabelShare || !strings.Contains(s.Error.Get(), "no exFAT filesystem") {
		t.Fatalf("state %q label %q error %q", s.State.Get(), s.Label.Get(), s.Error.Get())
	}
	if !strings.Contains(flat(a), "error: nothing to share") || a.Screen.Get() != ScreenContainer || a.Container.Busy.Get() {
		t.Errorf("messages %q, screen %q", flat(a), a.Screen.Get())
	}
	// It can be tried again, and a new try clears the error.
	a.Container.sess.(*fakeSession).shareErr = nil
	a.Container.sess.(*fakeSession).sh = newFakeShare()
	startShare(t, a, q)
	if s.Error.Get() != "" {
		t.Errorf("error %q after a good start", s.Error.Get())
	}
}

func TestShareEndsByItself(t *testing.T) {
	boom := errors.New("commit failed")
	sh := newFakeShare()
	a, q, _ := sharedContainer(t, sh)
	s := a.Container.Share
	startShare(t, a, q)
	sh.end(boom)
	waitFor(t, q, func() bool { return s.State.Get() == ShareError })
	if sh.stopped != 1 || !strings.Contains(s.Error.Get(), "commit failed") || s.MountPath.Get() != "" || s.Label.Get() != LabelShare {
		t.Fatalf("stopped %d, error %q, mount %q", sh.stopped, s.Error.Get(), s.MountPath.Get())
	}
	if !strings.Contains(flat(a), "error: commit failed") {
		t.Errorf("messages %q", flat(a))
	}

	// Ending with no reason is still shown.
	sh = newFakeShare()
	a, q, _ = sharedContainer(t, sh)
	s = a.Container.Share
	startShare(t, a, q)
	sh.end(nil)
	waitFor(t, q, func() bool { return s.State.Get() == ShareError })
	if s.Error.Get() != errShareEnded.Error() {
		t.Errorf("error %q", s.Error.Get())
	}
}

func TestShareStopFailureIsShown(t *testing.T) {
	sh := newFakeShare()
	sh.stopErr = errors.New("unmounting /Volumes/theca: busy")
	a, q, _ := sharedContainer(t, sh)
	s := a.Container.Share
	startShare(t, a, q)
	s.Toggle.Execute()
	q.Drain()
	if s.State.Get() != ShareError || !strings.Contains(s.Error.Get(), "busy") || s.sh != nil {
		t.Fatalf("state %q error %q", s.State.Get(), s.Error.Get())
	}
}

func TestLockStopsTheShareBeforeClosingTheContainer(t *testing.T) {
	var events []string
	sh := newFakeShare()
	sh.events = &events
	a, q, sess := sharedContainer(t, sh)
	sess.events = &events
	startShare(t, a, q)
	a.Container.Close.Execute()
	if a.Screen.Get() != ScreenContainer || sess.closed != 0 {
		t.Fatal("the container closed before the share had stopped")
	}
	q.Drain()
	if strings.Join(events, ",") != "stop,close" {
		t.Fatalf("order %v", events)
	}
	if a.Screen.Get() != ScreenHome || a.Container.sess != nil || a.Container.Share.State.Get() != ShareIdle || messages(a) != "" {
		t.Errorf("screen %q, state %q, messages %q", a.Screen.Get(), a.Container.Share.State.Get(), messages(a))
	}

	// A share that fails to stop does not keep the container open: both errors
	// are shown.
	events = nil
	sh = newFakeShare()
	sh.events = &events
	sh.stopErr = errors.New("unmount timed out")
	a, q, sess = sharedContainer(t, sh)
	sess.events = &events
	sess.closeErr = errors.New("commit failed")
	startShare(t, a, q)
	a.Container.Close.Execute()
	q.Drain()
	if sess.closed != 1 || a.Screen.Get() != ScreenHome || !strings.Contains(flat(a), "unmount timed out") || !strings.Contains(flat(a), "commit failed") {
		t.Fatalf("closed %d, screen %q, messages %q", sess.closed, a.Screen.Get(), flat(a))
	}
}

func TestShutdownStopsTheShareThenClosesTheContainer(t *testing.T) {
	var events []string
	sh := newFakeShare()
	sh.events = &events
	a, q, sess := sharedContainer(t, sh)
	sess.events = &events
	startShare(t, a, q)
	a.Shutdown()
	if strings.Join(events, ",") != "stop,close" || a.Container.sess != nil {
		t.Fatalf("order %v", events)
	}
	a.Shutdown() // nothing open: nothing to do
	if len(events) != 2 {
		t.Errorf("a second shutdown did something: %v", events)
	}

	// No share running: only the container closes.
	sess = &fakeSession{}
	a, _ = openContainer(t, sess)
	a.Shutdown()
	if sess.closed != 1 {
		t.Errorf("closed %d", sess.closed)
	}

	// No container open at all.
	a, _ = newApp(&fakeService{})
	a.Shutdown()
}

func TestAnotherContainerStartsIdle(t *testing.T) {
	sh := newFakeShare()
	a, q, sess := sharedContainer(t, sh)
	sess.shareErr = errors.New("refused")
	sess.sh = nil
	a.Container.Share.Toggle.Execute()
	q.Drain()
	if a.Container.Share.State.Get() != ShareError {
		t.Fatal("no error to carry over")
	}
	a.Container.Close.Execute()
	fillUnlock(a)
	a.Unlock.Submit.Execute()
	q.Drain()
	if a.Container.Share.State.Get() != ShareIdle || a.Container.Share.Error.Get() != "" {
		t.Errorf("state %q error %q on a new container", a.Container.Share.State.Get(), a.Container.Share.Error.Get())
	}
}

func TestNetworkShare(t *testing.T) {
	sh := newFakeShare()
	sh.addr = "[::]:4455"
	a, q, sess := sharedContainer(t, sh)
	s := a.Container.Share
	if s.Network.Get() || s.Warning.Get() != "" {
		t.Fatal("the network is chosen by default")
	}
	// Choosing it shows the warning, before anything is started.
	s.Network.Set(true)
	if s.Warning.Get() != NetworkWarning || !strings.Contains(NetworkWarning, "sign AND encrypt") || !strings.Contains(NetworkWarning, "has the password") {
		t.Fatalf("warning %q", s.Warning.Get())
	}
	if sess.shareCalls != 0 {
		t.Fatal("choosing the network started a share")
	}
	s.Network.Set(false)
	if s.Warning.Get() != "" {
		t.Fatalf("the warning outlived the choice: %q", s.Warning.Get())
	}
	s.Network.Set(true)
	startShare(t, a, q)
	if !sess.shareNetwork {
		t.Error("the library was not asked for a network share")
	}
	if s.Scope.Get() != ScopeNetwork || !strings.Contains(s.Addr.Get(), "[::]:4455") || !strings.Contains(s.Addr.Get(), ":4455/theca") {
		t.Errorf("scope %q, addr %q", s.Scope.Get(), s.Addr.Get())
	}
	// The password is a secret: nowhere until asked for.
	for name, shown := range map[string]string{"URL": s.URL.Get(), "Addr": s.Addr.Get(), "Scope": s.Scope.Get(),
		"Error": s.Error.Get(), "messages": flat(a), "warning": s.Warning.Get(), "mount": s.MountPath.Get()} {
		if strings.Contains(shown, "s3cret") {
			t.Errorf("%s shows the password", name)
		}
	}
	if s.Password.Get() != "" || !s.ShowPassword.CanExecute() || s.HidePassword.CanExecute() {
		t.Fatal("the password is shown before it is asked for")
	}
	s.ShowPassword.Execute()
	if s.Password.Get() != "s3cret-password" || s.ShowPassword.CanExecute() || !s.HidePassword.CanExecute() {
		t.Fatalf("after Show: %q", s.Password.Get())
	}
	if strings.Contains(flat(a), "s3cret") {
		t.Error("Show password put the password in a message")
	}
	s.HidePassword.Execute()
	if s.Password.Get() != "" {
		t.Error("Hide did not hide")
	}
	// Stopping clears everything, the password included.
	s.ShowPassword.Execute()
	s.Toggle.Execute()
	q.Drain()
	if s.Password.Get() != "" || s.Scope.Get() != "" || s.Addr.Get() != "" || s.ShowPassword.CanExecute() {
		t.Errorf("after stop: %q %q %q", s.Password.Get(), s.Scope.Get(), s.Addr.Get())
	}
}

func TestLocalShareScopeAndNoPasswordUntilAsked(t *testing.T) {
	sh := newFakeShare()
	a, q, sess := sharedContainer(t, sh)
	s := a.Container.Share
	startShare(t, a, q)
	if sess.shareNetwork || s.Scope.Get() != ScopeLocal || s.Addr.Get() != "listening on 127.0.0.1:5555" {
		t.Errorf("network %v, scope %q, addr %q", sess.shareNetwork, s.Scope.Get(), s.Addr.Get())
	}
	// Locking with the password shown wipes it from the ViewModel.
	s.ShowPassword.Execute()
	a.Container.Close.Execute()
	q.Drain()
	if s.Password.Get() != "" {
		t.Error("the password outlived the lock")
	}
	// An address with no port is shown as it is.
	if portOf("nonsense") != "nonsense" {
		t.Error("portOf")
	}
	// Nothing to show before a share runs.
	if s.ShowPassword.CanExecute() {
		t.Error("Show password is offered with no share")
	}
}
