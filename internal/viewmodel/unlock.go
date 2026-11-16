// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"context"
	"strings"

	"github.com/go-widgets/mvvm"

	"github.com/go-theca/gui/internal/vault"
)

// KeyTouchNotice is shown while a security key is asked to unlock.
const KeyTouchNotice = "Touch the security key when it blinks."

// UnlockVM is the Unlock screen: the factors the user presents.
type UnlockVM struct {
	app *App

	Password *mvvm.Observable[string]
	Recovery *mvvm.Observable[string]
	// UseKey is the "use security key" toggle.
	UseKey *mvvm.Observable[bool]
	Busy   *mvvm.Observable[bool]

	Submit *mvvm.Command // unlock
	Cancel *mvvm.Command // abandon an unlock in progress (a key may be waiting)
	Back   *mvvm.Command // back to the Home screen

	cancel context.CancelFunc
}

func newUnlockVM(a *App) *UnlockVM {
	u := &UnlockVM{
		app:      a,
		Password: mvvm.NewObservable(""),
		Recovery: mvvm.NewObservable(""),
		UseKey:   mvvm.NewObservable(false),
		Busy:     mvvm.NewObservable(false),
	}
	u.Submit = mvvm.NewCommand(u.submit, func() bool {
		return !u.Busy.Get() && (u.Password.Get() != "" || strings.TrimSpace(u.Recovery.Get()) != "")
	})
	mvvm.BindCanExecute(u.Submit, u.Busy, u.Password, u.Recovery)
	u.Cancel = mvvm.NewCommand(func() { u.cancel() }, func() bool { return u.Busy.Get() })
	mvvm.BindCanExecute(u.Cancel, u.Busy)
	u.Back = mvvm.NewCommand(func() { a.show(ScreenHome) }, func() bool { return !u.Busy.Get() })
	mvvm.BindCanExecute(u.Back, u.Busy)
	return u
}

func (u *UnlockVM) submit() {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	req := vault.UnlockRequest{
		Path:     strings.TrimSpace(u.app.Path.Get()),
		Password: []byte(u.Password.Get()),
		Recovery: strings.TrimSpace(u.Recovery.Get()),
		UseKey:   u.UseKey.Get(),
		PIN:      u.app.PIN.Ask(ctx),
	}
	// The entries are emptied now: only the byte copy lives on, and it is
	// cleared as soon as the library is done with it.
	u.Password.Set("")
	u.Recovery.Set("")
	if req.UseKey {
		u.app.Say(KeyTouchNotice)
	} else {
		u.app.Say("unlocking (stretching the password takes a moment)...")
	}
	var sess Session
	var err error
	u.app.async(u.Busy, func() {
		sess, err = u.app.svc.Open(ctx, req)
		clear(req.Password)
	}, func() {
		cancel()
		if err != nil {
			u.app.Fail(err)
			return
		}
		u.app.Container.attach(sess)
	})
}
