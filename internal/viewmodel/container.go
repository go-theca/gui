// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-widgets/mvvm"

	"github.com/go-theca/gui/internal/vault"
)

// EnrollNotice is shown while a security key is being enrolled.
const EnrollNotice = "Touch the security key twice when it blinks: to register it, then to confirm."

// ContainerVM is the Container screen: an unlocked container.
type ContainerVM struct {
	app  *App
	sess Session

	rollbackProtected bool

	// What Info says, one line each.
	Size, Epoch, Slots, Journal, Rollback *mvvm.Observable[string]

	// Activity is what the running (or last) operation says; Progress is how
	// far it is, in [0, 1].
	Activity *mvvm.Observable[string]
	Progress *mvvm.Observable[float64]
	Busy     *mvvm.Observable[bool]

	ImportPath *mvvm.Observable[string]
	ExportPath *mvvm.Observable[string]

	// EnrollPassword and EnrollRecovery are the factors a security key is
	// paired with, asked for again at the moment of enrolment.
	EnrollPassword *mvvm.Observable[string]
	EnrollRecovery *mvvm.Observable[string]

	// Share is the exFAT filesystem inside, served and mounted.
	Share *ShareVM

	Verify    *mvvm.Command
	Import    *mvvm.Command
	Export    *mvvm.Command
	EnrollKey *mvvm.Command
	Close     *mvvm.Command
}

func newContainerVM(a *App) *ContainerVM {
	c := &ContainerVM{
		app:        a,
		Size:       mvvm.NewObservable(""),
		Epoch:      mvvm.NewObservable(""),
		Slots:      mvvm.NewObservable(""),
		Journal:    mvvm.NewObservable(""),
		Rollback:   mvvm.NewObservable(""),
		Activity:   mvvm.NewObservable(""),
		Progress:   mvvm.NewObservable(0.0),
		Busy:       mvvm.NewObservable(false),
		ImportPath: mvvm.NewObservable(""),
		ExportPath: mvvm.NewObservable(""),

		EnrollPassword: mvvm.NewObservable(""),
		EnrollRecovery: mvvm.NewObservable(""),
	}
	c.Share = newShareVM(a, c)
	idle := func() bool { return !c.Busy.Get() }
	withPath := func(p *mvvm.Observable[string]) func() bool {
		return func() bool { return !c.Busy.Get() && strings.TrimSpace(p.Get()) != "" }
	}
	c.Verify = mvvm.NewCommand(c.verify, idle)
	c.Import = mvvm.NewCommand(c.importFile, withPath(c.ImportPath))
	c.Export = mvvm.NewCommand(c.exportFile, withPath(c.ExportPath))
	c.EnrollKey = mvvm.NewCommand(c.enrollKey, func() bool {
		return !c.Busy.Get() && (c.EnrollPassword.Get() != "" || strings.TrimSpace(c.EnrollRecovery.Get()) != "")
	})
	mvvm.BindCanExecute(c.EnrollKey, c.EnrollPassword, c.EnrollRecovery)
	c.Close = mvvm.NewCommand(c.close, idle)
	mvvm.BindCanExecute(c.Verify, c.Busy)
	mvvm.BindCanExecute(c.Import, c.Busy, c.ImportPath)
	mvvm.BindCanExecute(c.Export, c.Busy, c.ExportPath)
	mvvm.BindCanExecute(c.EnrollKey, c.Busy)
	mvvm.BindCanExecute(c.Close, c.Busy)
	return c
}

// attach shows an unlocked container.
func (c *ContainerVM) attach(s Session) {
	c.sess = s
	i := s.Info()
	c.Size.Set(fmt.Sprintf("size: %d bytes (%d sectors)", i.Size, i.Sectors))
	c.Epoch.Set(fmt.Sprintf("epoch: %d", i.Epoch))
	c.Slots.Set(fmt.Sprintf("slots used: %d of 16", i.SlotsUsed))
	c.Journal.Set(fmt.Sprintf("journal entries: %d", i.JournalEntries))
	c.Rollback.Set(fmt.Sprintf("rollback protected: %t", i.RollbackProtected))
	c.Activity.Set("")
	c.Progress.Set(0)
	c.rollbackProtected = i.RollbackProtected
	c.Share.idle()
	c.app.show(ScreenContainer)
	c.notice()
}

// notice shows what must stay in front of the user whenever nothing else is
// being said: the rollback-protection note, as the theca command prints it at
// every unlock (DESIGN 5.4).
func (c *ContainerVM) notice() {
	c.app.Messages.Clear()
	if !c.rollbackProtected {
		c.app.Say("note: " + vault.RollbackNotice)
	}
}

// progress is what a copy reports from its goroutine.
func (c *ContainerVM) progress(done, total int64) {
	f := 1.0
	if total > 0 {
		f = float64(done) / float64(total)
	}
	c.app.q.Post(func() { c.Progress.Set(f) })
}

// run is the shape of every operation: say what is starting, work on another
// goroutine, then report the outcome. outcome returns the line to show.
func (c *ContainerVM) run(start string, work func() (string, error)) {
	c.Activity.Set(start)
	c.Progress.Set(0)
	var line string
	var err error
	c.app.async(c.Busy, func() { line, err = work() }, func() {
		c.app.setKey(KeyIdle)
		if err != nil {
			c.Activity.Set("")
			c.app.Fail(err)
			return
		}
		c.Progress.Set(1)
		c.Activity.Set(line)
		c.notice()
	})
}

func (c *ContainerVM) verify() {
	// The library verifies in one call and reports no progress, so the bar
	// stays empty while it runs and fills when it is done.
	c.run("verifying every sector...", func() (string, error) {
		n, err := c.sess.Verify()
		return fmt.Sprintf("ok: %d sectors verified", n), err
	})
}

func (c *ContainerVM) importFile() {
	path := strings.TrimSpace(c.ImportPath.Get())
	c.run("importing...", func() (string, error) {
		n, err := c.sess.Import(path, c.progress)
		return fmt.Sprintf("ok: %d bytes written at 0", n), err
	})
}

func (c *ContainerVM) exportFile() {
	path := strings.TrimSpace(c.ExportPath.Get())
	c.run("exporting...", func() (string, error) {
		n, err := c.sess.Export(path, c.progress)
		return fmt.Sprintf("ok: %d bytes exported", n), err
	})
}

func (c *ContainerVM) enrollKey() {
	// The factors are presented again, now: unlock kept nothing derived from
	// them. The entries are emptied at once; the byte copy is cleared once the
	// library is done.
	pw := []byte(c.EnrollPassword.Get())
	rec := strings.TrimSpace(c.EnrollRecovery.Get())
	c.EnrollPassword.Set("")
	c.EnrollRecovery.Set("")
	c.app.Say(EnrollNotice)
	c.app.setKey(KeyTouch)
	ctx := context.Background()
	c.run("enrolling the security key...", func() (string, error) {
		n, err := c.sess.EnrollKey(ctx, pw, rec, c.app.PIN.Ask(ctx))
		clear(pw)
		return fmt.Sprintf("ok: the key is paired with %d factor(s)", n), err
	})
}

// close stops the share, if one runs, then commits and closes the container:
// the share reads and writes through the container, so it ends first.
func (c *ContainerVM) close() {
	if c.Share.sh != nil {
		c.Share.stop(false, c.finishClose)
		return
	}
	c.finishClose(nil)
}

// shutdown is the window closing: stop the share, then close the container,
// all on the calling goroutine. There is no screen left to report to.
func (c *ContainerVM) shutdown() {
	if c.sess == nil {
		return
	}
	c.Share.halt()
	c.sess.Close()
	c.sess = nil
}

// finishClose commits and closes the container, then goes back to the Home
// screen whatever the outcome: a container that failed to close must be
// reopened. stopErr is what stopping the share said.
func (c *ContainerVM) finishClose(stopErr error) {
	err := errors.Join(stopErr, c.sess.Close())
	c.sess = nil
	c.ImportPath.Set("")
	c.ExportPath.Set("")
	c.EnrollPassword.Set("")
	c.EnrollRecovery.Set("")
	c.Activity.Set("")
	c.app.show(ScreenHome)
	if err != nil {
		c.app.Fail(err)
	}
}
