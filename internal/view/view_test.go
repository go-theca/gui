// SPDX-License-Identifier: BSD-3-Clause

package view

import (
	"context"
	"strings"
	"testing"

	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"

	"github.com/go-theca/gui/internal/vault"
	"github.com/go-theca/gui/internal/viewmodel"
)

// --- a service that needs no file -------------------------------------------

type session struct{ closed bool }

func (s *session) Info() vault.Info {
	return vault.Info{Size: 65536, Sectors: 16, Epoch: 2, SlotsUsed: 3, JournalEntries: 64}
}
func (s *session) Verify() (uint64, error) { return 16, nil }
func (s *session) Import(string, func(int64, int64)) (int64, error) {
	return 0, nil
}
func (s *session) Export(string, func(int64, int64)) (int64, error) {
	return 0, nil
}
func (s *session) EnrollKey(context.Context, []byte, string, func() (string, error)) (int, error) {
	return 1, nil
}
func (s *session) Close() error { s.closed = true; return nil }

type service struct{ password string }

func (s *service) Create(req vault.CreateRequest) (string, error) {
	s.password = string(req.Password)
	return "CODE-1234-ABCD", nil
}

func (s *service) Open(_ context.Context, req vault.UnlockRequest) (viewmodel.Session, error) {
	s.password = string(req.Password)
	return &session{}, nil
}

// --- helpers ----------------------------------------------------------------

func newView() (*View, *viewmodel.App, *service) {
	svc := &service{}
	q := mvvm.NewQueue(nil)
	app := viewmodel.New(svc, q, func(f func()) { f() })
	return New(app, q), app, svc
}

// frame is what a window does before drawing: drain, lay out, draw.
func frame(v *View) []byte {
	v.SetBounds(toolkit.Rect{W: Width, H: Height})
	buf := make([]byte, 4*Width*Height)
	v.Draw(painter.NewPixelPainter(buf, Width, Height), toolkit.DefaultLight())
	return buf
}

func walk(w toolkit.Widget, fn func(toolkit.Widget)) {
	fn(w)
	if c, ok := w.(interface{ Children() []toolkit.Widget }); ok {
		for _, ch := range c.Children() {
			walk(ch, fn)
		}
	}
}

// entries are the entries of the visible screen, in order.
func entries(v *View) (out []*toolkit.Entry) {
	walk(v, func(w toolkit.Widget) {
		if e, ok := w.(*toolkit.Entry); ok {
			out = append(out, e)
		}
	})
	return out
}

func button(t *testing.T, v *View, label string) *toolkit.Button {
	t.Helper()
	var found *toolkit.Button
	walk(v, func(w toolkit.Widget) {
		if b, ok := w.(*toolkit.Button); ok && b.Label().Get() == label {
			found = b
		}
	})
	if found == nil {
		t.Fatalf("no %q button on screen %q", label, v.app.Screen.Get())
	}
	return found
}

func labels(v *View) (out []string) {
	walk(v, func(w toolkit.Widget) {
		if l, ok := w.(*toolkit.Label); ok {
			out = append(out, l.Text().Get())
		}
	})
	return out
}

func hasLabel(v *View, s string) bool {
	for _, l := range labels(v) {
		if l == s {
			return true
		}
	}
	return false
}

// click presses a widget the way a window would: an event in the root's own
// coordinates, at the widget's centre, routed by hit-testing.
func click(v *View, w toolkit.Widget) {
	b := w.Bounds()
	v.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: b.X + b.W/2, Y: b.Y + b.H/2})
}

// typeInto focuses an entry with a click and types text into it.
func typeInto(v *View, e *toolkit.Entry, text string) {
	click(v, e)
	for _, r := range text {
		v.OnEvent(toolkit.Event{Kind: toolkit.EventChar, Code: string(r)})
	}
}

// --- tests ------------------------------------------------------------------

func TestEveryScreenDraws(t *testing.T) {
	v, app, _ := newView()
	defer v.Close()
	for _, screen := range []string{
		viewmodel.ScreenHome, viewmodel.ScreenCreate, viewmodel.ScreenRecovery,
		viewmodel.ScreenUnlock, viewmodel.ScreenContainer,
	} {
		app.Screen.Set(screen)
		buf := frame(v)
		bg := toolkit.DefaultLight().Background
		painted := 0
		for i := 0; i+3 < len(buf); i += 4 {
			if buf[i] != bg.R || buf[i+1] != bg.G || buf[i+2] != bg.B {
				painted++
			}
		}
		if painted == 0 {
			t.Errorf("screen %q drew nothing", screen)
		}
	}
}

func TestSecretsAreMasked(t *testing.T) {
	v, app, _ := newView()
	defer v.Close()
	// Per screen, in order: which entries hold a secret.
	for screen, secret := range map[string][]bool{
		viewmodel.ScreenHome:      {false},
		viewmodel.ScreenCreate:    {false, false, true, true},
		viewmodel.ScreenUnlock:    {true, true, true},
		viewmodel.ScreenContainer: {false, false, true, true, true},
	} {
		app.Screen.Set(screen)
		frame(v)
		es := entries(v)
		if len(es) != len(secret) {
			t.Fatalf("%s: %d entries, want %d", screen, len(es), len(secret))
		}
		for i, e := range es {
			if (e.Mask != 0) != secret[i] {
				t.Errorf("%s entry %d: masked = %v, want %v", screen, i, e.Mask != 0, secret[i])
			}
		}
	}
}

// memClipboard records what reaches the clipboard.
type memClipboard struct{ text string }

func (c *memClipboard) ClipboardText() string     { return c.text }
func (c *memClipboard) SetClipboardText(s string) { c.text = s }

func TestASecretNeverReachesTheClipboard(t *testing.T) {
	clip := &memClipboard{}
	toolkit.SetClipboard(clip)
	defer toolkit.SetClipboard(nil)

	v, app, _ := newView()
	defer v.Close()
	app.Screen.Set(viewmodel.ScreenUnlock)
	frame(v)
	pw := entries(v)[0]
	typeInto(v, pw, "s3cret")
	if pw.Value() != "s3cret" {
		t.Fatalf("typing did not reach the entry: %q", pw.Value())
	}
	for _, mod := range []toolkit.Event{{Ctrl: true}, {Meta: true}} {
		for _, key := range []string{"a", "c", "x"} {
			e := mod
			e.Kind, e.Code = toolkit.EventKeyDown, key
			v.OnEvent(e)
		}
	}
	if clip.text != "" {
		t.Errorf("a secret reached the clipboard: %q", clip.text)
	}
}

func TestBindingsFollowTheViewModel(t *testing.T) {
	v, app, _ := newView()
	frame(v)
	path := entries(v)[0]
	app.Path.Set("/a/b.theca")
	if path.Value() != "/a/b.theca" {
		t.Errorf("entry = %q", path.Value())
	}
	if !v.Dirty() {
		t.Error("a ViewModel change did not mark the view dirty")
	}
	if v.Dirty() {
		t.Error("Dirty did not clear itself")
	}
	// And the other way: typing reaches the ViewModel.
	typeInto(v, path, "!")
	if app.Path.Get() != "/a/b.theca!" {
		t.Errorf("path = %q", app.Path.Get())
	}

	v.Close()
	app.Path.Set("/elsewhere")
	if path.Value() == "/elsewhere" {
		t.Error("a closed view is still bound")
	}
}

func TestWholeFlowThroughTheWidgets(t *testing.T) {
	v, app, svc := newView()
	defer v.Close()
	frame(v)

	// Home: the path, then Create.
	typeInto(v, entries(v)[0], "/tmp/x.theca")
	click(v, button(t, v, "Create new container"))
	frame(v)
	if app.Screen.Get() != viewmodel.ScreenCreate {
		t.Fatalf("screen = %q", app.Screen.Get())
	}
	es := entries(v)
	if es[0].Value() != "/tmp/x.theca" {
		t.Errorf("create path = %q", es[0].Value())
	}

	// Create: the second radio (2of3), the password twice.
	var radios []*toolkit.RadioButton
	walk(v, func(w toolkit.Widget) {
		if r, ok := w.(*toolkit.RadioButton); ok {
			radios = append(radios, r)
		}
	})
	if len(radios) != len(viewmodel.PolicyLabels) {
		t.Fatalf("%d radios", len(radios))
	}
	if !radios[0].Checked().Get() || radios[1].Checked().Get() {
		t.Errorf("the default policy is not shown: %v %v", radios[0].Checked().Get(), radios[1].Checked().Get())
	}
	click(v, radios[1])
	if radios[0].Checked().Get() || !radios[1].Checked().Get() {
		t.Errorf("the chosen policy is not shown")
	}
	if app.Create.Policy.Get() != viewmodel.PolicyTwoOfThree {
		t.Errorf("policy = %d", app.Create.Policy.Get())
	}
	typeInto(v, es[2], "hunter2")
	typeInto(v, es[3], "hunter2")
	click(v, button(t, v, "Create"))
	frame(v)

	// Recovery: the code is shown; the screen is left only once acknowledged.
	if app.Screen.Get() != viewmodel.ScreenRecovery || !hasLabel(v, "CODE-1234-ABCD") {
		t.Fatalf("screen %q, labels %q", app.Screen.Get(), labels(v))
	}
	if svc.password != "hunter2" {
		t.Errorf("the library got %q", svc.password)
	}
	click(v, button(t, v, "Done"))
	frame(v)
	if app.Screen.Get() != viewmodel.ScreenRecovery {
		t.Fatal("left the Recovery screen without the acknowledgement")
	}
	var ack *toolkit.CheckButton
	walk(v, func(w toolkit.Widget) {
		if c, ok := w.(*toolkit.CheckButton); ok {
			ack = c
		}
	})
	click(v, ack)
	click(v, button(t, v, "Done"))
	frame(v)
	if app.Screen.Get() != viewmodel.ScreenHome || hasLabel(v, "CODE-1234-ABCD") {
		t.Fatalf("screen %q: the code outlived its screen", app.Screen.Get())
	}

	// Home again, with the new container's path: unlock it.
	click(v, button(t, v, "Unlock container"))
	frame(v)
	if app.Screen.Get() != viewmodel.ScreenUnlock || !hasLabel(v, "/tmp/x.theca") {
		t.Fatalf("screen %q, labels %q", app.Screen.Get(), labels(v))
	}
	typeInto(v, entries(v)[0], "hunter2")
	click(v, button(t, v, "Unlock"))
	frame(v)
	if app.Screen.Get() != viewmodel.ScreenContainer {
		t.Fatalf("screen = %q, messages %q", app.Screen.Get(), app.Messages.Slice())
	}
	for _, want := range []string{
		"size: 65536 bytes (16 sectors)", "epoch: 2", "slots used: 3 of 16",
		"journal entries: 64", "rollback protected: false",
	} {
		if !hasLabel(v, want) {
			t.Errorf("no %q on the Container screen", want)
		}
	}
	// The rollback notice is in the status area.
	if got := strings.Join(app.Messages.Slice(), " "); !strings.Contains(got, vault.RollbackNotice) {
		t.Errorf("messages = %q", got)
	}

	click(v, button(t, v, "Verify"))
	frame(v)
	if !hasLabel(v, "ok: 16 sectors verified") {
		t.Errorf("labels = %q", labels(v))
	}
	click(v, button(t, v, "Close container"))
	frame(v)
	if app.Screen.Get() != viewmodel.ScreenHome {
		t.Errorf("screen = %q", app.Screen.Get())
	}
}

func TestADisabledButtonDoesNothing(t *testing.T) {
	v, app, _ := newView()
	defer v.Close()
	frame(v)
	click(v, button(t, v, "Unlock container")) // no path yet
	if app.Screen.Get() != viewmodel.ScreenHome {
		t.Errorf("a disabled button moved to %q", app.Screen.Get())
	}
}

func TestBackgroundResultsAreDrainedByTheFrame(t *testing.T) {
	v, app, _ := newView()
	defer v.Close()
	frame(v)
	drained := false
	v.q.Post(func() { drained = true })
	if drained {
		t.Fatal("posted work ran on the poster's goroutine")
	}
	app.Screen.Set(viewmodel.ScreenHome)
	frame(v)
	if !drained {
		t.Error("the frame did not drain the queue")
	}
}
