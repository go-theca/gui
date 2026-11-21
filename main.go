// SPDX-License-Identifier: BSD-3-Clause

// Command theca-gui is a desktop GUI for Theca encrypted containers
// (github.com/go-theca/theca): create, unlock, inspect, verify, import,
// export, enroll a FIDO2 security key, and share the exFAT filesystem inside.
//
// It is a go-widgets application: the state lives in internal/viewmodel
// (go-widgets/mvvm), the widgets in internal/view (go-widgets/toolkit, bound
// with go-widgets/mvvmtk), the library calls in internal/vault, and the window
// is go-widgets/window's, which picks Cocoa, X11 or Wayland, or Win32. This
// file only wires them together and is the one file the 100% coverage gate
// does not measure.
package main

import (
	"fmt"
	"os"

	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/window"

	"github.com/go-theca/gui/internal/vault"
	"github.com/go-theca/gui/internal/view"
	"github.com/go-theca/gui/internal/viewmodel"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "theca-gui:", err)
		os.Exit(1)
	}
}

func run() error {
	w, err := window.Open(window.Config{
		Title:     "Theca",
		Class:     "theca-gui",
		Width:     view.Width,
		Height:    view.Height,
		FixedSize: true,
		Theme:     toolkit.DefaultLight(),
	})
	if err != nil {
		return err
	}
	defer w.Close()

	// Background work posts its results here; the window wakes to draw a
	// frame, and the View drains the queue at the start of it.
	q := mvvm.NewQueue(func() {
		if r, ok := w.(window.Repainter); ok {
			r.Repaint()
		}
	})
	// The host clipboard lets a path be pasted. A secret never reaches it: the
	// masked entries refuse copy and cut, and the recovery code is a label.
	if c, ok := w.(window.Clipboard); ok {
		toolkit.SetClipboard(c)
	}

	host, err := viewmodel.DefaultHost()
	if err != nil {
		return err
	}
	app := viewmodel.New(viewmodel.Backend{V: vault.New()}, host, q, nil)
	v := view.New(app, q)
	defer v.Close()
	// Last deferred, first run: the share stops and the container closes
	// before the view and the window go.
	defer app.Shutdown()
	return w.Run(v)
}
