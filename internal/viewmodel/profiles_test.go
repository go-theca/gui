// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-widgets/mvvm"

	"github.com/go-theca/theca/profile"
	"github.com/go-theca/theca/session"
)

// --- fakes: the store, the session client and the spawner --------------------

type fakeStore struct {
	ps  []profile.Profile
	err error
}

func (s *fakeStore) List() ([]profile.Profile, error) { return s.ps, s.err }

type fakeSessions struct {
	open      map[string]session.Info // what is open
	statusErr map[string]error
	closeErr  error
	closed    []string
	ctxLimit  bool // Close was given a deadline
}

func (s *fakeSessions) Status(_ context.Context, name string) (session.Info, error) {
	if err := s.statusErr[name]; err != nil {
		return session.Info{}, err
	}
	if i, ok := s.open[name]; ok {
		return i, nil
	}
	return session.Info{}, session.ErrNotOpen
}

func (s *fakeSessions) Close(ctx context.Context, name string) error {
	_, s.ctxLimit = ctx.Deadline()
	s.closed = append(s.closed, name)
	if s.closeErr == nil {
		delete(s.open, name)
	}
	return s.closeErr
}

type fakeSpawner struct {
	started []string
	notices []string // what the process writes while it unlocks
	during  func()   // runs after the notices, while Start is still waiting
	info    session.Info
	err     error
	waitCtx bool // block until the context ends
	hadDL   bool
}

func (f *fakeSpawner) Start(ctx context.Context, name string, notice func(string)) (session.Info, error) {
	f.started = append(f.started, name)
	_, f.hadDL = ctx.Deadline()
	for _, n := range f.notices {
		notice(n)
	}
	if f.during != nil {
		f.during()
	}
	if f.waitCtx {
		<-ctx.Done()
		return session.Info{}, ctx.Err()
	}
	return f.info, f.err
}

type profEnv struct {
	a  *App
	q  *mvvm.Queue
	st *fakeStore
	se *fakeSessions
	sp *fakeSpawner
	p  *ProfilesVM
}

func newProfEnv(t *testing.T, names ...string) *profEnv {
	t.Helper()
	e := &profEnv{
		st: &fakeStore{},
		se: &fakeSessions{open: map[string]session.Info{}, statusErr: map[string]error{}},
		sp: &fakeSpawner{info: session.Info{Mount: "/Volumes/THECA", Since: 1700000000}},
	}
	for _, n := range names {
		e.st.ps = append(e.st.ps, profile.New(n, "/vault/"+n+".theca"))
	}
	e.q = mvvm.NewQueue(nil)
	e.a = New(&fakeService{}, Host{Store: e.st, Sessions: e.se, Spawner: e.sp}, e.q, func(f func()) { f() })
	e.p = e.a.Profiles
	return e
}

// show opens the screen and lets the load finish.
func (e *profEnv) show() {
	e.a.GoProfiles.Execute()
	e.q.Drain()
}

func (e *profEnv) lines() []string { return e.p.Lines.Slice() }

func (e *profEnv) pick(i int) { e.p.Selected.Set(i) }

func (e *profEnv) run(c *mvvm.Command) {
	c.Execute()
	e.q.Drain()
}

// --- listing ----------------------------------------------------------------

func TestProfilesListState(t *testing.T) {
	e := newProfEnv(t, "bio", "work", "odd")
	e.se.open["work"] = session.Info{Name: "work", Mount: "/Volumes/WORK", Since: 1700000000}
	e.se.statusErr["odd"] = errors.New("session: peer is not the owner of the session")
	e.show()

	if e.a.Screen.Get() != ScreenProfiles {
		t.Fatalf("screen %q", e.a.Screen.Get())
	}
	since := time.Unix(1700000000, 0).Local().Format("2006-01-02 15:04")
	want := []string{
		"bio: closed",
		"work: open, mounted at /Volumes/WORK, since " + since,
		"odd: session: peer is not the owner of the session",
	}
	if got := e.lines(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("lines\n got %q\nwant %q", got, want)
	}
	rows := e.p.Rows()
	if rows[1].State != ProfileOpen || rows[1].Mount != "/Volumes/WORK" || rows[0].State != ProfileClosed || rows[2].State != ProfileError {
		t.Fatalf("%+v", rows)
	}
	if e.p.Empty.Get() != "" || e.p.Busy.Get() {
		t.Fatal("empty message or busy left on")
	}
}

func TestProfilesNone(t *testing.T) {
	e := newProfEnv(t)
	e.show()
	if e.p.Empty.Get() != NoProfiles || e.p.Lines.Len() != 0 {
		t.Fatalf("%q %v", e.p.Empty.Get(), e.lines())
	}
	if !strings.Contains(NoProfiles, "theca profile add") {
		t.Fatal("the message does not say what to do")
	}
}

func TestProfilesStoreError(t *testing.T) {
	e := newProfEnv(t, "bio")
	e.show()
	e.st.err = errors.New("containers.toml is readable by others")
	e.run(e.p.Refresh)
	if !strings.Contains(flat(e.a), "containers.toml is readable by others") {
		t.Fatal(messages(e.a))
	}
	if e.p.Lines.Len() != 1 { // what was listed stays
		t.Fatal(e.lines())
	}
}

func TestProfilesRefreshKeepsTheSelection(t *testing.T) {
	e := newProfEnv(t, "a", "b", "c")
	e.show()
	e.pick(1)
	e.st.ps = e.st.ps[1:] // "a" goes away: b is now first
	e.run(e.p.Refresh)
	if e.p.Selected.Get() != 0 || e.p.Rows()[0].Name != "b" {
		t.Fatalf("selected %d of %+v", e.p.Selected.Get(), e.p.Rows())
	}
	e.st.ps = e.st.ps[1:2] // "b" goes away too
	e.run(e.p.Refresh)
	if e.p.Selected.Get() != -1 {
		t.Fatalf("selected %d", e.p.Selected.Get())
	}
}

func TestProfilesBack(t *testing.T) {
	e := newProfEnv(t, "a")
	e.show()
	e.p.Back.Execute()
	if e.a.Screen.Get() != ScreenHome {
		t.Fatal(e.a.Screen.Get())
	}
	e.p.Busy.Set(true)
	if e.p.Back.CanExecute() || e.p.Refresh.CanExecute() {
		t.Fatal("left or refreshed while busy")
	}
}

// --- open -------------------------------------------------------------------

func TestProfilesOpen(t *testing.T) {
	e := newProfEnv(t, "bio", "work")
	e.show()
	e.pick(0)
	if !e.p.Open.CanExecute() || e.p.Close.CanExecute() || e.p.Cancel.CanExecute() {
		t.Fatal("a closed profile can be opened, not closed")
	}
	var sawBusy, sawOpening bool
	var stateDuring string
	e.sp.during = func() {
		sawBusy, sawOpening = e.p.Busy.Get(), e.p.Opening.Get()
		stateDuring = e.p.Rows()[0].State
	}
	e.run(e.p.Open)

	if len(e.sp.started) != 1 || e.sp.started[0] != "bio" || !e.sp.hadDL {
		t.Fatalf("started %v, deadline %v", e.sp.started, e.sp.hadDL)
	}
	if !sawBusy || !sawOpening || stateDuring != ProfileOpening {
		t.Fatalf("during: busy %v opening %v state %q", sawBusy, sawOpening, stateDuring)
	}
	r := e.p.Rows()[0]
	if r.State != ProfileOpen || r.Mount != "/Volumes/THECA" || !r.Since.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(e.lines()[0], "open, mounted at /Volumes/THECA, since ") {
		t.Fatal(e.lines())
	}
	if e.p.Busy.Get() || e.p.Opening.Get() || e.a.KeyState.Get() != KeyIdle {
		t.Fatal("still waiting")
	}
	if !strings.Contains(flat(e.a), "bio is open, mounted at /Volumes/THECA") {
		t.Fatal(messages(e.a))
	}
	// An open profile is closed, not opened again.
	if e.p.Open.CanExecute() || !e.p.Close.CanExecute() {
		t.Fatal("open profile offers Open")
	}
}

func TestProfilesTouchIsAState(t *testing.T) {
	e := newProfEnv(t, "bio")
	e.show()
	e.pick(0)
	var state, key, hint, line string
	e.sp.notices = []string{"warning: close-on-lock: polling every 2s", "Touch the security key when it blinks."}
	e.sp.during = func() {
		e.q.Drain() // what the window does at its next frame
		state, key, hint, line = e.p.Rows()[0].State, e.a.KeyState.Get(), e.a.KeyHint.Get(), e.lines()[0]
		if !e.p.Cancel.CanExecute() {
			t.Error("a wait for a touch cannot be cancelled")
		}
	}
	e.run(e.p.Open)
	if state != ProfileTouch || key != KeyTouch || hint != KeyTouchHint || !strings.Contains(line, "touch your security key") {
		t.Fatalf("state %q key %q hint %q line %q", state, key, hint, line)
	}
	if e.a.KeyState.Get() != KeyIdle || e.p.Rows()[0].State != ProfileOpen {
		t.Fatal("touch state outlived the open")
	}
}

func TestProfilesOtherNoticesAreNotATouch(t *testing.T) {
	e := newProfEnv(t, "bio")
	e.show()
	e.pick(0)
	var state string
	e.sp.notices = []string{"close-on-lock: notifications"}
	e.sp.during = func() { e.q.Drain(); state = e.p.Rows()[0].State }
	e.run(e.p.Open)
	if state != ProfileOpening {
		t.Fatalf("state %q", state)
	}
	// A notice for a profile no longer listed is dropped.
	e.p.notice("gone", "touch")
}

func TestProfilesOpenFails(t *testing.T) {
	e := newProfEnv(t, "bio")
	e.show()
	e.pick(0)
	e.sp.err = errors.New("the password manager refused")
	e.run(e.p.Open)
	if e.p.Rows()[0].State != ProfileClosed || e.p.Busy.Get() || e.p.Opening.Get() {
		t.Fatalf("%+v", e.p.Rows())
	}
	if !strings.Contains(flat(e.a), "error: the password manager refused") {
		t.Fatal(messages(e.a))
	}
	if !e.p.Open.CanExecute() {
		t.Fatal("cannot try again")
	}
}

func TestProfilesCancelAnOpen(t *testing.T) {
	e := newProfEnv(t, "bio")
	e.show()
	e.pick(0)
	e.sp.waitCtx = true
	e.sp.during = func() { e.p.Cancel.Execute() }
	e.run(e.p.Open)
	if e.p.Rows()[0].State != ProfileClosed || !strings.Contains(flat(e.a), "context canceled") {
		t.Fatalf("%+v %s", e.p.Rows(), messages(e.a))
	}
	if e.p.Cancel.CanExecute() {
		t.Fatal("nothing left to cancel")
	}
}

// --- close ------------------------------------------------------------------

func TestProfilesClose(t *testing.T) {
	e := newProfEnv(t, "bio")
	e.se.open["bio"] = session.Info{Name: "bio", Mount: "/Volumes/THECA", Since: 1}
	e.show()
	e.pick(0)
	if e.p.Open.CanExecute() || !e.p.Close.CanExecute() {
		t.Fatal("an open profile is closed")
	}
	e.run(e.p.Close)
	if len(e.se.closed) != 1 || e.se.closed[0] != "bio" || !e.se.ctxLimit {
		t.Fatalf("%v limit %v", e.se.closed, e.se.ctxLimit)
	}
	if r := e.p.Rows()[0]; r.State != ProfileClosed || r.Mount != "" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(flat(e.a), "bio is closed") || e.lines()[0] != "bio: closed" {
		t.Fatal(messages(e.a), e.lines())
	}
}

func TestProfilesCloseShowsClosing(t *testing.T) {
	e := newProfEnv(t, "bio")
	e.se.open["bio"] = session.Info{Mount: "/m"}
	e.show()
	e.pick(0)
	e.p.Close.Execute()
	e.q.Drain()
	if got := (ProfileRow{Name: "bio", State: ProfileClosing}).Line(); got != "bio: closing..." {
		t.Fatal(got)
	}
}

func TestProfilesCloseFails(t *testing.T) {
	e := newProfEnv(t, "bio")
	e.se.open["bio"] = session.Info{Mount: "/Volumes/THECA", Since: 1}
	e.show()
	e.pick(0)
	e.se.closeErr = errors.New("unmount: the volume is busy")
	e.run(e.p.Close)
	if r := e.p.Rows()[0]; r.State != ProfileOpen || r.Mount != "/Volumes/THECA" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(flat(e.a), "error: unmount: the volume is busy") {
		t.Fatal(messages(e.a))
	}
}

func TestProfilesCloseOfWhatIsAlreadyClosed(t *testing.T) {
	e := newProfEnv(t, "bio")
	e.se.open["bio"] = session.Info{Mount: "/m"}
	e.show()
	e.pick(0)
	e.se.closeErr = session.ErrNotOpen
	e.run(e.p.Close)
	if e.p.Rows()[0].State != ProfileClosed || !strings.Contains(flat(e.a), "bio is closed") {
		t.Fatalf("%+v %s", e.p.Rows(), messages(e.a))
	}
}

func TestProfilesCommandsNeedASelectionAndIdleness(t *testing.T) {
	e := newProfEnv(t, "a", "b")
	e.se.open["b"] = session.Info{Mount: "/m"}
	e.show()
	e.pick(-1)
	if e.p.Open.CanExecute() || e.p.Close.CanExecute() {
		t.Fatal("nothing selected")
	}
	e.pick(7)
	if e.p.Open.CanExecute() || e.p.Close.CanExecute() {
		t.Fatal("selection out of range")
	}
	e.pick(0)
	e.p.Busy.Set(true)
	if e.p.Open.CanExecute() || e.p.Close.CanExecute() {
		t.Fatal("busy")
	}
	e.p.Busy.Set(false)
	e.pick(1)
	if e.p.Open.CanExecute() || !e.p.Close.CanExecute() {
		t.Fatal("state not followed")
	}
	if len(e.sp.started)+len(e.se.closed) != 0 {
		t.Fatal("something ran")
	}
}

func TestProfileRowLine(t *testing.T) {
	cases := map[string]ProfileRow{
		"x: closed":     {Name: "x", State: ProfileClosed},
		"x: opening...": {Name: "x", State: ProfileOpening},
		"x: opening - touch your security key now": {Name: "x", State: ProfileTouch},
		"x: closing...": {Name: "x", State: ProfileClosing},
		"x: boom":       {Name: "x", State: ProfileError, Error: "boom"},
	}
	for want, r := range cases {
		if got := r.Line(); got != want {
			t.Errorf("%q, want %q", got, want)
		}
	}
}

func TestDefaultHost(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HOME", cfg)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("APPDATA", cfg)
	h, err := DefaultHost()
	if err != nil || h.Store == nil || h.Sessions == nil || h.Spawner == nil {
		t.Fatalf("%+v %v", h, err)
	}
	if st, ok := h.Store.(profile.Store); !ok || filepath.Base(st.Path) != "containers.toml" || !strings.HasPrefix(st.Path, cfg) {
		t.Fatalf("%#v", h.Store)
	}
	// No home, no configuration directory: the host cannot be built.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")
	if _, err := DefaultHost(); err == nil {
		t.Fatal("a host without a configuration directory")
	}
}
