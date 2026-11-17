// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"context"
	"errors"
	"net"

	"github.com/go-widgets/mvvm"

	"github.com/go-theca/gui/internal/vault"
)

// The values of ShareVM.State.
const (
	ShareIdle     = "idle"
	ShareStarting = "starting"
	ShareShared   = "shared"
	ShareError    = "error"
)

// The labels of the Share button, which is a toggle.
const (
	LabelShare = "Share"
	LabelStop  = "Stop sharing"
)

// NetworkWarning is shown whenever the share may leave this computer.
const NetworkWarning = "Network share: every client must sign AND encrypt (an older or plainer client is refused), but anyone on the network who can reach this computer and has the password can read and write the files."

// The scope of a running share, as the values of ShareVM.Scope.
const (
	ScopeLocal   = "This computer only (127.0.0.1)"
	ScopeNetwork = "The network (signing and encryption required)"
)

// errShareEnded is shown when a share ends by itself without saying why.
var errShareEnded = errors.New("the share ended")

// ShareVM is the sharing of an unlocked container: its exFAT filesystem is
// served over SMB on loopback and mounted in the file manager. The account's
// password is never here: the vault layer does not hand it out.
type ShareVM struct {
	app *App
	c   *ContainerVM
	sh  vault.Share // nil unless the share is running
	// reveal opens a path in the file manager.
	reveal func(path string) error

	// State is one of ShareIdle, ShareStarting, ShareShared, ShareError.
	State *mvvm.Observable[string]
	// Label is the toggle's text: LabelShare or LabelStop.
	Label *mvvm.Observable[string]
	// URL is the share's address while it runs, "" otherwise.
	URL *mvvm.Observable[string]
	// MountPath is where the share is mounted, "" when it is not.
	MountPath *mvvm.Observable[string]
	// Error is why the last start or the running share failed.
	Error *mvvm.Observable[string]
	// Network is the "Share on the network" choice, taken when sharing starts.
	Network *mvvm.Observable[bool]
	// Warning is NetworkWarning while Network is chosen, "" otherwise.
	Warning *mvvm.Observable[string]
	// Scope and Addr say how the running share is reachable: where it listens,
	// and (on the network) the port another machine connects to.
	Scope *mvvm.Observable[string]
	Addr  *mvvm.Observable[string]
	// Password is the account's SECRET: empty until ShowPassword, emptied again
	// by HidePassword, at stop and at lock. It is never in a message or a log.
	Password *mvvm.Observable[string]

	// ShowPassword reveals the password; HidePassword takes it off the screen.
	ShowPassword *mvvm.Command
	HidePassword *mvvm.Command

	// Toggle starts the share, or stops it.
	Toggle *mvvm.Command
	// Open shows the mounted share in the file manager.
	Open *mvvm.Command
}

func newShareVM(a *App, c *ContainerVM) *ShareVM {
	s := &ShareVM{
		app:       a,
		c:         c,
		reveal:    vault.Reveal,
		State:     mvvm.NewObservable(ShareIdle),
		Label:     mvvm.NewObservable(LabelShare),
		URL:       mvvm.NewObservable(""),
		MountPath: mvvm.NewObservable(""),
		Error:     mvvm.NewObservable(""),
		Network:   mvvm.NewObservable(false),
		Warning:   mvvm.NewObservable(""),
		Scope:     mvvm.NewObservable(""),
		Addr:      mvvm.NewObservable(""),
		Password:  mvvm.NewObservable(""),
	}
	s.Network.Subscribe(func(on bool) {
		if on {
			s.Warning.Set(NetworkWarning)
		} else {
			s.Warning.Set("")
		}
	})
	s.ShowPassword = mvvm.NewCommand(func() { s.Password.Set(s.sh.Password()) },
		func() bool { return s.sh != nil && s.Password.Get() == "" })
	mvvm.BindCanExecute(s.ShowPassword, s.State, s.Password)
	s.HidePassword = mvvm.NewCommand(func() { s.Password.Set("") }, func() bool { return s.Password.Get() != "" })
	mvvm.BindCanExecute(s.HidePassword, s.Password)
	s.Toggle = mvvm.NewCommand(s.toggle, func() bool { return !c.Busy.Get() })
	mvvm.BindCanExecute(s.Toggle, c.Busy)
	s.Open = mvvm.NewCommand(s.open, func() bool { return !c.Busy.Get() && s.MountPath.Get() != "" })
	mvvm.BindCanExecute(s.Open, c.Busy, s.MountPath)
	return s
}

// idle puts the state back to "not shared".
func (s *ShareVM) idle() {
	s.State.Set(ShareIdle)
	s.Label.Set(LabelShare)
	s.URL.Set("")
	s.MountPath.Set("")
	s.Error.Set("")
	s.Scope.Set("")
	s.Addr.Set("")
	s.Password.Set("")
}

// fail shows why the share is not running.
func (s *ShareVM) fail(err error) {
	s.idle()
	s.State.Set(ShareError)
	s.Error.Set(err.Error())
	s.app.Fail(err)
}

func (s *ShareVM) toggle() {
	if s.sh != nil {
		s.stop(false, nil)
		return
	}
	s.start()
}

func (s *ShareVM) start() {
	s.idle()
	s.State.Set(ShareStarting)
	s.app.Say("starting the share and mounting it...")
	var sh vault.Share
	var err error
	network := s.Network.Get()
	s.app.async(s.c.Busy, func() { sh, err = s.c.sess.Share(context.Background(), network) }, func() {
		if err != nil {
			s.fail(err)
			return
		}
		s.sh = sh
		s.State.Set(ShareShared)
		s.Label.Set(LabelStop)
		s.URL.Set(sh.URL())
		s.MountPath.Set(sh.MountPath())
		if network {
			s.Scope.Set(ScopeNetwork)
			s.Addr.Set("listening on " + sh.Addr() + "; from another machine: smb://<this computer's address>:" + portOf(sh.Addr()) + "/theca")
		} else {
			s.Scope.Set(ScopeLocal)
			s.Addr.Set("listening on " + sh.Addr())
		}
		s.c.notice()
		// A failed commit or server ends the share by itself. Not spawn: this
		// waits, and the tests run spawn at once.
		go func() {
			<-sh.Done()
			s.app.q.Post(func() {
				if s.sh == sh { // not already stopped by the user
					s.stop(true, nil)
				}
			})
		}()
	})
}

// stop unmounts and stops the share, on another goroutine. ended says the
// share ended by itself, so that is what is shown whatever Stop returns. When
// after is given it is told Stop's error and nothing is shown here; else the
// error, if any, is shown.
func (s *ShareVM) stop(ended bool, after func(error)) {
	sh := s.sh
	s.sh = nil // the watcher must not see it as a share that ended by itself
	var err error
	s.app.async(s.c.Busy, func() { err = sh.Stop() }, func() {
		if ended && err == nil {
			err = errShareEnded
		}
		s.idle()
		switch {
		case after != nil:
			after(err)
		case err != nil:
			s.fail(err)
		default:
			s.c.notice()
		}
	})
}

// halt stops the share at once, on the calling goroutine: the window is
// closing, and the container is closed right after.
func (s *ShareVM) halt() error {
	sh := s.sh
	if sh == nil {
		return nil
	}
	s.sh = nil
	return sh.Stop()
}

// portOf is the port of a host:port address.
func portOf(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	return addr
}

func (s *ShareVM) open() {
	if err := s.reveal(s.MountPath.Get()); err != nil {
		s.app.Fail(err)
	}
}
