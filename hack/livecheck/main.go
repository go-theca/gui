// SPDX-License-Identifier: BSD-3-Clause

// Command livecheck is a developer tool, not part of the application: it opens
// the REAL window (Cocoa on a Mac), then drives the real View through the
// widget tree's own event path (clicks at each widget's position, typed
// characters) to create a throwaway container, acknowledge its recovery code,
// unlock it, read its info and verify it, taking a screenshot of the window at
// each step.
//
// It never touches the security key: enrolment needs the user's touches.
//
//	go run ./hack/livecheck -dir <directory for the throwaway container>
//
// Screenshots go to a directory under os.UserConfigDir(), and are refused
// anywhere under a .git directory: a capture is never to be committed.
//
//go:build darwin

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/window"

	"github.com/go-theca/gui/internal/vault"
	"github.com/go-theca/gui/internal/view"
	"github.com/go-theca/gui/internal/viewmodel"
)

const password = "live-check-password"

func main() {
	dir := flag.String("dir", "", "directory for the throwaway container (created if absent)")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "livecheck: -dir is required")
		os.Exit(2)
	}
	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "livecheck: FAIL:", err)
		os.Exit(1)
	}
}

// captureDir is where screenshots go: under the user's config directory, never
// inside a repository.
func captureDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "theca-gui-livecheck")
	for _, part := range strings.Split(d, string(filepath.Separator)) {
		if part == ".git" {
			return "", errors.New("refusing to capture screenshots under a .git directory")
		}
	}
	return d, os.MkdirAll(d, 0o700)
}

func run(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, "gui-live.theca")
	if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
		return err
	}
	shots, err := captureDir()
	if err != nil {
		return err
	}

	w, err := window.Open(window.Config{
		Title: "Theca (livecheck)", Class: "theca-gui", Width: view.Width, Height: view.Height,
		FixedSize: true, Theme: toolkit.DefaultLight(),
	})
	if err != nil {
		return err
	}
	q := mvvm.NewQueue(func() {
		if r, ok := w.(window.Repainter); ok {
			r.Repaint()
		}
	})
	app := viewmodel.New(viewmodel.Backend{V: vault.New()}, q, nil)
	v := view.New(app, q)

	result := make(chan error, 1)
	go func() {
		err := drive(w, q, app, v, file, shots)
		result <- err
		w.Close()
		time.Sleep(2 * time.Second)
		os.Exit(0) // the run loop below may not return on a Close from here
	}()
	runErr := w.Run(v)
	select {
	case err := <-result:
		return err
	default:
		return runErr
	}
}

// do runs f on the UI goroutine and waits for it.
func do(q *mvvm.Queue, f func()) {
	done := make(chan struct{})
	q.Post(func() { f(); close(done) })
	<-done
}

// until polls cond, on the UI goroutine, until it holds.
func until(q *mvvm.Queue, what string, limit time.Duration, cond func() bool) error {
	end := time.Now().Add(limit)
	for time.Now().Before(end) {
		ok := false
		do(q, func() { ok = cond() })
		if ok {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", what)
}

func walk(w toolkit.Widget, fn func(toolkit.Widget)) {
	fn(w)
	if c, ok := w.(interface{ Children() []toolkit.Widget }); ok {
		for _, ch := range c.Children() {
			walk(ch, fn)
		}
	}
}

func entries(v *view.View) (out []*toolkit.Entry) {
	walk(v, func(w toolkit.Widget) {
		if e, ok := w.(*toolkit.Entry); ok {
			out = append(out, e)
		}
	})
	return out
}

func labelTexts(v *view.View) (out []string) {
	walk(v, func(w toolkit.Widget) {
		if l, ok := w.(*toolkit.Label); ok {
			out = append(out, l.Text().Get())
		}
	})
	return out
}

func click(v *view.View, w toolkit.Widget) {
	b := w.Bounds()
	v.OnEvent(toolkit.Event{Kind: toolkit.EventClick, X: b.X + b.W/2, Y: b.Y + b.H/2})
}

func typeInto(v *view.View, e *toolkit.Entry, text string) {
	click(v, e)
	for _, r := range text {
		v.OnEvent(toolkit.Event{Kind: toolkit.EventChar, Code: string(r)})
	}
}

func clickButton(v *view.View, label string) error {
	var found *toolkit.Button
	walk(v, func(w toolkit.Widget) {
		if b, ok := w.(*toolkit.Button); ok && b.Label().Get() == label {
			found = b
		}
	})
	if found == nil {
		return fmt.Errorf("no %q button on screen", label)
	}
	click(v, found)
	return nil
}

func shoot(w window.Backend, dir, name string) {
	n, ok := w.(window.Numbered)
	if !ok || n.Number() == 0 {
		fmt.Println("shot", name, ": window has no number, skipped")
		return
	}
	path := filepath.Join(dir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-l", fmt.Sprint(n.Number()), path).CombinedOutput()
	if err != nil {
		fmt.Println("shot", name, "failed:", err, strings.TrimSpace(string(out)))
		return
	}
	fmt.Println("shot", name, "->", path)
}

// step runs one action on the UI goroutine, lets a frame be drawn, and shoots.
func step(w window.Backend, q *mvvm.Queue, shots, name string, f func() error) error {
	var err error
	do(q, func() { err = f() })
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	time.Sleep(700 * time.Millisecond)
	shoot(w, shots, name)
	return nil
}

func drive(w window.Backend, q *mvvm.Queue, app *viewmodel.App, v *view.View, file, shots string) error {
	time.Sleep(1500 * time.Millisecond) // the first frame

	screen := func(want string) func() bool { return func() bool { return app.Screen.Get() == want } }

	if err := step(w, q, shots, "1-home", func() error {
		typeInto(v, entries(v)[0], file)
		return nil
	}); err != nil {
		return err
	}
	if err := step(w, q, shots, "2-create-form", func() error { return clickButton(v, "Create new container") }); err != nil {
		return err
	}
	// The form is laid out by the next frame; fill it in.
	time.Sleep(500 * time.Millisecond)
	if err := step(w, q, shots, "3-create-filled", func() error {
		app.Create.Size.Set("1M")
		es := entries(v)
		if len(es) != 4 {
			return fmt.Errorf("create form has %d entries, want 4", len(es))
		}
		typeInto(v, es[2], password)
		typeInto(v, es[3], password)
		return nil
	}); err != nil {
		return err
	}
	if err := step(w, q, shots, "4-creating", func() error { return clickButton(v, "Create") }); err != nil {
		return err
	}
	if err := until(q, "the Recovery screen", 60*time.Second, screen(viewmodel.ScreenRecovery)); err != nil {
		return err
	}
	var codeLen int
	do(q, func() { codeLen = len(app.Create.Recovery.Get()) })
	fmt.Println("recovery code shown, length", codeLen, "(not printed)")
	if err := step(w, q, shots, "5-recovery", func() error {
		// Done must refuse before the acknowledgement.
		if err := clickButton(v, "Done"); err != nil {
			return err
		}
		if app.Screen.Get() != viewmodel.ScreenRecovery {
			return errors.New("left the Recovery screen without the acknowledgement")
		}
		var ack *toolkit.CheckButton
		walk(v, func(w toolkit.Widget) {
			if c, ok := w.(*toolkit.CheckButton); ok {
				ack = c
			}
		})
		click(v, ack)
		return nil
	}); err != nil {
		return err
	}
	if err := step(w, q, shots, "6-home-again", func() error { return clickButton(v, "Done") }); err != nil {
		return err
	}
	if err := step(w, q, shots, "7-unlock-form", func() error { return clickButton(v, "Unlock container") }); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	if err := step(w, q, shots, "8-unlock-filled", func() error {
		typeInto(v, entries(v)[0], password)
		return nil
	}); err != nil {
		return err
	}
	if err := step(w, q, shots, "9-unlocking", func() error { return clickButton(v, "Unlock") }); err != nil {
		return err
	}
	if err := until(q, "the Container screen", 60*time.Second, screen(viewmodel.ScreenContainer)); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	if err := step(w, q, shots, "10-info", func() error {
		fmt.Println("info:", strings.Join(labelTexts(v), " | "))
		fmt.Println("status area:", strings.Join(app.Messages.Slice(), " "))
		return nil
	}); err != nil {
		return err
	}
	if err := step(w, q, shots, "11-verifying", func() error { return clickButton(v, "Verify") }); err != nil {
		return err
	}
	if err := until(q, "the verify result", 60*time.Second, func() bool {
		return strings.HasPrefix(app.Container.Activity.Get(), "ok: ")
	}); err != nil {
		return err
	}
	if err := step(w, q, shots, "12-verified", func() error {
		fmt.Println("verify:", app.Container.Activity.Get())
		return nil
	}); err != nil {
		return err
	}
	if err := step(w, q, shots, "13-closed", func() error { return clickButton(v, "Close container") }); err != nil {
		return err
	}
	if err := until(q, "the Home screen", 10*time.Second, screen(viewmodel.ScreenHome)); err != nil {
		return err
	}
	fmt.Println("livecheck: OK")
	return nil
}
