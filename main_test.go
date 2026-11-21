// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"errors"
	"testing"

	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/toolkit"

	"github.com/go-theca/gui/internal/viewmodel"
)

func TestBuild(t *testing.T) {
	saved := defaultHost
	t.Cleanup(func() { defaultHost = saved })

	defaultHost = func() (viewmodel.Host, error) { return viewmodel.Host{}, nil }
	app, err := build(mvvm.NewQueue(nil))
	if err != nil || app == nil || app.Profiles == nil {
		t.Fatalf("%v %v", app, err)
	}

	want := errors.New("no configuration directory")
	defaultHost = func() (viewmodel.Host, error) { return viewmodel.Host{}, want }
	if app, err := build(mvvm.NewQueue(nil)); !errors.Is(err, want) || app != nil {
		t.Fatalf("%v %v", app, err)
	}
}

type fakeWin struct {
	closed, ran bool
	runErr      error
}

func (f *fakeWin) Close() error             { f.closed = true; return nil }
func (f *fakeWin) Run(toolkit.Widget) error { f.ran = true; return f.runErr }

func TestServe(t *testing.T) {
	saved := defaultHost
	t.Cleanup(func() { defaultHost = saved })

	defaultHost = func() (viewmodel.Host, error) { return viewmodel.Host{}, nil }
	runErr := errors.New("window ended")
	w := &fakeWin{runErr: runErr}
	if err := serve(w); !errors.Is(err, runErr) || !w.ran || !w.closed {
		t.Fatalf("%v %+v", err, w)
	}

	want := errors.New("no configuration directory")
	defaultHost = func() (viewmodel.Host, error) { return viewmodel.Host{}, want }
	w = &fakeWin{}
	if err := serve(w); !errors.Is(err, want) || w.ran || !w.closed {
		t.Fatalf("%v %+v", err, w)
	}
}
