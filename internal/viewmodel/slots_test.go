// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-theca/gui/internal/vault"
)

func TestSlotsScreenListsWhatTheFactorsOpen(t *testing.T) {
	sess := &fakeSession{listing: vault.SlotListing{
		Recorded: []int{2, 11},
		Opened:   map[vault.FactorSet][]int{vault.SetPassword: {2}, vault.SetRecovery: {11}, vault.SetPasswordRecovery: nil},
	}}
	a, q := openContainer(t, sess)
	s := a.Container.Keyslots
	s.Open.Execute()
	if a.Screen.Get() != ScreenSlots || s.List.CanExecute() {
		t.Fatalf("screen %q, List offered with no factor: %v", a.Screen.Get(), s.List.CanExecute())
	}
	s.Password.Set("hunter2")
	s.Recovery.Set(" ABCD ")
	s.List.Execute()
	q.Drain()
	if s.Password.Get() != "" || s.Recovery.Get() != "" {
		t.Error("the entries still hold the secrets")
	}
	if sess.listPW != "hunter2" || sess.listRec != "ABCD" {
		t.Errorf("the library got %q %q", sess.listPW, sess.listRec)
	}
	got := strings.Join(s.Lines.Slice(), "|")
	want := "recorded slots: 2, 11 (2 of 16)|opened by the password: 2|opened by the recovery code: 11|opened by the password + recovery code (a 2-of-3 slot): none"
	if got != want {
		t.Fatalf("lines\n%s\nwant\n%s", got, want)
	}
	// A failure is shown, not a crash.
	sess.listErr = errors.New("boom")
	s.Password.Set("x")
	s.List.Execute()
	q.Drain()
	if !strings.Contains(flat(a), "error: boom") {
		t.Errorf("messages %q", flat(a))
	}
	// Back to the container, clean.
	s.Back.Execute()
	if a.Screen.Get() != ScreenContainer || s.Lines.Len() != 0 {
		t.Errorf("screen %q, %d lines", a.Screen.Get(), s.Lines.Len())
	}
}

func TestRemoveSlotByFactors(t *testing.T) {
	sess := &fakeSession{}
	a, q := openContainer(t, sess)
	s := a.Container.Keyslots
	s.Open.Execute()
	if s.RemoveSlot.CanExecute() {
		t.Fatal("removal is offered with no factor typed")
	}
	// Defaults: remove the password, keep the recovery code. Pick the recovery
	// code to remove, the password to keep.
	if s.Remove.Get() != 0 || s.Keep.Get() != 1 {
		t.Fatalf("defaults %d %d", s.Remove.Get(), s.Keep.Get())
	}
	s.Remove.Set(1)
	s.Keep.Set(0)
	s.Password.Set("hunter2")
	s.Recovery.Set("ABCD")
	s.RemoveSlot.Execute()
	q.Drain()
	if sess.removed != [2]vault.FactorSet{vault.SetRecovery, vault.SetPassword} || sess.removePW != "hunter2" || sess.removeRec != "ABCD" {
		t.Fatalf("library got %v %q %q", sess.removed, sess.removePW, sess.removeRec)
	}
	if !allZero(sess.removeRaw) {
		t.Error("the password bytes were not wiped")
	}
	if s.Password.Get() != "" || s.Recovery.Get() != "" {
		t.Error("the entries still hold the secrets")
	}
	if got := flat(a); !strings.HasPrefix(got, "ok: removed 1 slot(s) opened by the recovery code.") || !strings.Contains(got, CopyNotice) {
		t.Errorf("messages %q", got)
	}
}

func TestRemoveSlotRefusals(t *testing.T) {
	sess := &fakeSession{removeErr: vault.ErrLastWay}
	a, q := openContainer(t, sess)
	s := a.Container.Keyslots
	s.Open.Execute()
	s.Password.Set("hunter2")
	s.RemoveSlot.Execute()
	q.Drain()
	// The refusal has its own clear message, and the screen stays.
	if flat(a) != LastWayMessage || a.Screen.Get() != ScreenSlots {
		t.Fatalf("messages %q, screen %q", flat(a), a.Screen.Get())
	}
	// Any other error is shown as the library words it.
	sess.removeErr = errors.New("the recovery code is needed for \"recovery\": type it again")
	s.Password.Set("hunter2")
	s.RemoveSlot.Execute()
	q.Drain()
	if !strings.HasPrefix(flat(a), "error: the recovery code is needed") {
		t.Errorf("messages %q", flat(a))
	}
}

func TestSlotsScreenIsLockedWhileBusy(t *testing.T) {
	a, _ := openContainer(t, &fakeSession{})
	c := a.Container
	c.Busy.Set(true)
	s := c.Keyslots
	s.Password.Set("x")
	if s.Open.CanExecute() || s.List.CanExecute() || s.RemoveSlot.CanExecute() || s.Back.CanExecute() {
		t.Error("the slots screen is not locked while busy")
	}
}
