// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-widgets/mvvm"

	"github.com/go-theca/gui/internal/vault"
)

// Factor set choices, as the values of SlotsVM.Remove and SlotsVM.Keep (a radio
// group's index), in the order of vault.FactorSets.
var SetLabels = []string{
	"password",
	"recovery code",
	"password + recovery code (a 2-of-3 slot)",
}

// LastWayMessage is what a refused removal says: the container is unchanged.
const LastWayMessage = "Refused, nothing was changed: after this removal the way in you chose to keep would not open the container. Keep a different way in (for example the password) before removing this one."

// CopyNotice is shown after a removal: it only stops access to this copy.
const CopyNotice = "A copy of the container made before now still opens with the removed factor."

// SlotsVM is the Keyslots screen: which slots the container records and which
// the factors you type now open, and removal by factor set.
type SlotsVM struct {
	app *App
	c   *ContainerVM

	// Lines is the listing, one line per fact.
	Lines *mvvm.ObservableList[string]
	// Password and Recovery are what the slots are asked about, typed again now
	// (nothing derived from them was kept at unlock); emptied once used.
	Password *mvvm.Observable[string]
	Recovery *mvvm.Observable[string]
	// Remove is the factor set whose slot is removed, Keep the DIFFERENT set that
	// must still open the container; indexes into vault.FactorSets.
	Remove *mvvm.Observable[int]
	Keep   *mvvm.Observable[int]

	Open       *mvvm.Command // show the screen
	List       *mvvm.Command
	RemoveSlot *mvvm.Command
	Back       *mvvm.Command
}

func newSlotsVM(a *App, c *ContainerVM) *SlotsVM {
	s := &SlotsVM{
		app: a, c: c,
		Lines:    mvvm.NewObservableList[string](),
		Password: mvvm.NewObservable(""),
		Recovery: mvvm.NewObservable(""),
		Remove:   mvvm.NewObservable(0), // the password...
		Keep:     mvvm.NewObservable(1), // ...keeping the recovery code
	}
	presented := func() bool {
		return s.Password.Get() != "" || strings.TrimSpace(s.Recovery.Get()) != ""
	}
	s.Open = mvvm.NewCommand(s.open, func() bool { return !c.Busy.Get() })
	mvvm.BindCanExecute(s.Open, c.Busy)
	s.List = mvvm.NewCommand(s.list, func() bool { return !c.Busy.Get() && presented() })
	mvvm.BindCanExecute(s.List, c.Busy, s.Password, s.Recovery)
	s.RemoveSlot = mvvm.NewCommand(s.remove, func() bool { return !c.Busy.Get() && presented() })
	mvvm.BindCanExecute(s.RemoveSlot, c.Busy, s.Password, s.Recovery)
	s.Back = mvvm.NewCommand(s.back, func() bool { return !c.Busy.Get() })
	mvvm.BindCanExecute(s.Back, c.Busy)
	return s
}

func (s *SlotsVM) open() {
	s.reset()
	s.app.show(ScreenSlots)
	s.app.Say("Type the password and/or the recovery code to see which slots they open.")
}

func (s *SlotsVM) back() {
	s.reset()
	s.app.show(ScreenContainer)
	s.c.notice()
}

func (s *SlotsVM) reset() {
	s.Lines.Clear()
	s.Password.Set("")
	s.Recovery.Set("")
}

// take returns the typed secrets and empties the entries; the byte copy is the
// caller's to clear.
func (s *SlotsVM) take() (pw []byte, rec string) {
	pw = []byte(s.Password.Get())
	rec = strings.TrimSpace(s.Recovery.Get())
	s.Password.Set("")
	s.Recovery.Set("")
	return pw, rec
}

func ints(v []int) string {
	if len(v) == 0 {
		return "none"
	}
	out := make([]string, len(v))
	for i, n := range v {
		out[i] = fmt.Sprint(n)
	}
	return strings.Join(out, ", ")
}

// show puts a listing in the lines.
func (s *SlotsVM) show(l vault.SlotListing) {
	s.Lines.Clear()
	s.Lines.Append(fmt.Sprintf("recorded slots: %s (%d of 16)", ints(l.Recorded), len(l.Recorded)))
	for i, set := range vault.FactorSets {
		if idx, ok := l.Opened[set]; ok {
			s.Lines.Append(fmt.Sprintf("opened by the %s: %s", SetLabels[i], ints(idx)))
		}
	}
}

func (s *SlotsVM) list() {
	pw, rec := s.take()
	var l vault.SlotListing
	var err error
	s.app.async(s.c.Busy, func() {
		l, err = s.c.sess.ListSlots(pw, rec)
		clear(pw)
	}, func() {
		if err != nil {
			s.app.Fail(err)
			return
		}
		s.show(l)
		s.app.Messages.Clear()
	})
}

func (s *SlotsVM) remove() {
	rm, kp := vault.FactorSets[s.Remove.Get()], vault.FactorSets[s.Keep.Get()]
	pw, rec := s.take()
	s.app.Say("removing the slot...")
	var n int
	var err error
	s.app.async(s.c.Busy, func() {
		n, err = s.c.sess.RemoveFactors(rm, kp, pw, rec)
		clear(pw)
	}, func() {
		switch {
		case errors.Is(err, vault.ErrLastWay):
			s.app.Say(LastWayMessage)
		case err != nil:
			s.app.Fail(err)
		default:
			s.app.Say(fmt.Sprintf("ok: removed %d slot(s) opened by the %s. %s", n, SetLabels[s.Remove.Get()], CopyNotice))
		}
	})
}
