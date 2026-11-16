// SPDX-License-Identifier: BSD-3-Clause

// Package viewmodel holds ALL the state of the Theca GUI as go-widgets/mvvm
// primitives (Observables, an ObservableList and Commands) and never references
// a widget. The View (package view) binds widgets to these through
// go-widgets/mvvmtk, so state only ever changes here, and the whole application
// logic is tested without a window.
//
// Work that can take long (Argon2id at unlock, verifying every sector, copying
// a file, waiting for a security key's touch) runs on another goroutine, and
// its result comes back through an mvvm.Queue that the host drains on the UI
// goroutine: Observables are not safe for concurrent use.
//
// Secrets. The widgets hand the ViewModel a string (an Entry's text), which Go
// cannot wipe. The ViewModel therefore takes a []byte copy for the library,
// clears that copy as soon as the library is done with it, and empties the
// Observable the moment the operation starts, so that the string is dropped as
// early as it can be. No secret is ever put in a message, logged or placed on
// the clipboard.
package viewmodel

import (
	"context"
	"strings"

	"github.com/go-widgets/mvvm"

	"github.com/go-theca/gui/internal/vault"
)

// The screens, as the values of App.Screen.
const (
	ScreenHome      = "home"
	ScreenCreate    = "create"
	ScreenRecovery  = "recovery"
	ScreenUnlock    = "unlock"
	ScreenContainer = "container"
)

// wrapWidth is the width, in characters, messages are wrapped to.
const wrapWidth = 72

// Session is an unlocked container, as the ViewModels use it.
type Session interface {
	Info() vault.Info
	Verify() (uint64, error)
	Import(path string, progress func(done, total int64)) (int64, error)
	Export(path string, progress func(done, total int64)) (int64, error)
	EnrollKey(ctx context.Context, pin func() (string, error)) (int, error)
	Close() error
}

// Service creates and opens containers.
type Service interface {
	Create(req vault.CreateRequest) (string, error)
	Open(ctx context.Context, req vault.UnlockRequest) (Session, error)
}

// Backend adapts the vault package to Service.
type Backend struct{ V *vault.Service }

// Create creates a container.
func (b Backend) Create(req vault.CreateRequest) (string, error) { return b.V.Create(req) }

// Open unlocks a container.
func (b Backend) Open(ctx context.Context, req vault.UnlockRequest) (Session, error) {
	s, err := b.V.Open(ctx, req)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// App is the whole application state.
type App struct {
	svc   Service
	q     *mvvm.Queue
	spawn func(func())

	// Screen is the screen shown: one of the Screen constants.
	Screen *mvvm.Observable[string]
	// Messages is the status area: the last error, notice or result, wrapped
	// into lines.
	Messages *mvvm.ObservableList[string]
	// Path is the container to open (the Home screen's entry).
	Path *mvvm.Observable[string]

	// GoCreate and GoUnlock leave the Home screen.
	GoCreate *mvvm.Command
	GoUnlock *mvvm.Command

	PIN       *PINPrompt
	Create    *CreateVM
	Unlock    *UnlockVM
	Container *ContainerVM
}

// New builds the application state. q is where results of background work are
// posted; the host drains it on the UI goroutine. spawn runs background work;
// nil means a new goroutine, and a test passes a function that runs it at once.
func New(svc Service, q *mvvm.Queue, spawn func(func())) *App {
	if spawn == nil {
		spawn = func(f func()) { go f() }
	}
	a := &App{
		svc:      svc,
		q:        q,
		spawn:    spawn,
		Screen:   mvvm.NewObservable(ScreenHome),
		Messages: mvvm.NewObservableList[string](),
		Path:     mvvm.NewObservable(""),
	}
	a.PIN = newPINPrompt(q)
	a.Create = newCreateVM(a)
	a.Unlock = newUnlockVM(a)
	a.Container = newContainerVM(a)

	a.GoCreate = mvvm.NewCommand(func() {
		a.Create.Path.Set(a.Path.Get())
		a.show(ScreenCreate)
	}, nil)
	a.GoUnlock = mvvm.NewCommand(func() { a.show(ScreenUnlock) }, func() bool { return strings.TrimSpace(a.Path.Get()) != "" })
	mvvm.BindCanExecute(a.GoUnlock, a.Path)
	return a
}

// show shows a screen, clearing the status area of the previous one.
func (a *App) show(screen string) {
	a.Messages.Clear()
	a.Screen.Set(screen)
}

// Say puts text in the status area, wrapped.
func (a *App) Say(text string) {
	a.Messages.Clear()
	a.Messages.Append(Wrap(text, wrapWidth)...)
}

// Fail puts an error in the status area. Every error is shown, as the library
// words it.
func (a *App) Fail(err error) { a.Say("error: " + err.Error()) }

// async runs work on another goroutine with busy set, then, back on the UI
// goroutine, clears busy and runs done.
func (a *App) async(busy *mvvm.Observable[bool], work, done func()) {
	busy.Set(true)
	a.spawn(func() {
		work()
		a.q.Post(func() {
			busy.Set(false)
			done()
		})
	})
}

// Wrap breaks text into lines of at most width characters, at spaces where it
// can and inside a word where it must.
func Wrap(text string, width int) []string {
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			for len([]rune(word)) > width {
				r := []rune(word)
				if line != "" {
					lines = append(lines, line)
					line = ""
				}
				lines = append(lines, string(r[:width]))
				word = string(r[width:])
			}
			switch {
			case line == "":
				line = word
			case len([]rune(line))+1+len([]rune(word)) <= width:
				line += " " + word
			default:
				lines = append(lines, line)
				line = word
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
