// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"errors"
	"strings"

	"github.com/go-widgets/mvvm"

	"github.com/go-theca/gui/internal/vault"
)

// Policy choices, as the values of CreateVM.Policy (a radio group's index).
const (
	PolicyAny        = 0
	PolicyTwoOfThree = 1
)

// PolicyLabels are the radio labels, in the order of the Policy values.
var PolicyLabels = []string{
	"any: the password or the recovery code opens it",
	"2of3: the password and the recovery code are both needed",
}

// DefaultSize is the size a new container is offered.
const DefaultSize = "16M"

// CreateVM is the Create screen and the Recovery screen that follows it.
type CreateVM struct {
	app *App

	// The form.
	Path     *mvvm.Observable[string]
	Size     *mvvm.Observable[string]
	Policy   *mvvm.Observable[int]
	Password *mvvm.Observable[string]
	Confirm  *mvvm.Observable[string]
	Busy     *mvvm.Observable[bool]

	// Recovery is the recovery code, held only while the Recovery screen shows
	// it: the one time it exists anywhere. Acked is the user's explicit "I have
	// written it down"; the screen cannot be left without it.
	Recovery *mvvm.Observable[string]
	Acked    *mvvm.Observable[bool]

	Submit *mvvm.Command // create the container
	Back   *mvvm.Command // abandon the form
	Done   *mvvm.Command // leave the Recovery screen, once acknowledged
}

func newCreateVM(a *App) *CreateVM {
	c := &CreateVM{
		app:      a,
		Path:     mvvm.NewObservable(""),
		Size:     mvvm.NewObservable(DefaultSize),
		Policy:   mvvm.NewObservable(PolicyAny),
		Password: mvvm.NewObservable(""),
		Confirm:  mvvm.NewObservable(""),
		Busy:     mvvm.NewObservable(false),
		Recovery: mvvm.NewObservable(""),
		Acked:    mvvm.NewObservable(false),
	}
	c.Submit = mvvm.NewCommand(c.submit, func() bool {
		return !c.Busy.Get() && strings.TrimSpace(c.Path.Get()) != "" &&
			strings.TrimSpace(c.Size.Get()) != "" && c.Password.Get() != ""
	})
	mvvm.BindCanExecute(c.Submit, c.Busy, c.Path, c.Size, c.Password)
	c.Back = mvvm.NewCommand(func() { a.show(ScreenHome) }, func() bool { return !c.Busy.Get() })
	mvvm.BindCanExecute(c.Back, c.Busy)
	c.Done = mvvm.NewCommand(c.done, func() bool { return c.Acked.Get() && c.Recovery.Get() != "" })
	mvvm.BindCanExecute(c.Done, c.Acked, c.Recovery)
	return c
}

func (c *CreateVM) submit() {
	if c.Password.Get() != c.Confirm.Get() {
		c.app.Fail(errors.New("the passwords differ"))
		return
	}
	req := vault.CreateRequest{
		Path:     strings.TrimSpace(c.Path.Get()),
		Size:     c.Size.Get(),
		Policy:   vault.PolicyAny,
		Password: []byte(c.Password.Get()),
	}
	if c.Policy.Get() == PolicyTwoOfThree {
		req.Policy = vault.PolicyTwoOfThree
	}
	// The entries are emptied now: only the byte copy lives on, and it is
	// cleared as soon as the library is done with it.
	c.Password.Set("")
	c.Confirm.Set("")
	var code string
	var err error
	c.app.async(c.Busy, func() {
		code, err = c.app.svc.Create(req)
		clear(req.Password)
	}, func() {
		if code != "" {
			// Shown even if closing the file failed: the container is formatted
			// and this is the only copy of the code there will ever be.
			c.Recovery.Set(code)
			c.Acked.Set(false)
			c.app.show(ScreenRecovery)
			msg := "note: " + vault.RollbackNotice
			if err != nil {
				msg = "error: " + err.Error() + "\n" + msg
			}
			c.app.Say(msg)
			return
		}
		c.app.Fail(err)
	})
}

// done leaves the Recovery screen, forgetting the code: it is shown once.
func (c *CreateVM) done() {
	c.Recovery.Set("")
	c.Acked.Set(false)
	c.app.Path.Set(strings.TrimSpace(c.Path.Get()))
	c.app.show(ScreenHome)
	c.app.Say("created " + c.app.Path.Get() + ". Unlock it with the password or the recovery code.")
}
