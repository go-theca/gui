// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"context"
	"strings"
	"testing"

	"github.com/go-widgets/mvvm"

	"github.com/go-theca/gui/internal/vault"
)

// The key's state follows what it waits for: touch at unlock, PIN when it asks,
// touch again after the PIN, idle when the unlock is over. The PIN is never in
// a hint or a message.
func TestKeyStateAtUnlock(t *testing.T) {
	sess := &fakeSession{}
	svc := &fakeService{sess: sess}
	asked := make(chan struct{})
	svc.onOpen = func(_ context.Context, req vault.UnlockRequest) {
		close(asked)
		req.PIN()
	}
	q := mvvm.NewQueue(nil)
	a := New(svc, Host{}, q, nil)
	if a.KeyState.Get() != KeyIdle || a.KeyHint.Get() != "" {
		t.Fatalf("not idle at the start: %q %q", a.KeyState.Get(), a.KeyHint.Get())
	}
	fillUnlock(a)
	a.Unlock.UseKey.Set(true)
	a.Unlock.Submit.Execute()
	if a.KeyState.Get() != KeyTouch || a.KeyHint.Get() != KeyTouchHint {
		t.Fatalf("touch not asked for: %q %q", a.KeyState.Get(), a.KeyHint.Get())
	}
	<-asked
	waitFor(t, q, func() bool { return a.KeyState.Get() == KeyPIN })
	if a.KeyHint.Get() != KeyPINHint {
		t.Errorf("hint %q", a.KeyHint.Get())
	}
	a.PIN.Value.Set("987654")
	a.PIN.Submit.Execute()
	if a.KeyState.Get() != KeyTouch {
		t.Errorf("after the PIN the key wants a touch, state %q", a.KeyState.Get())
	}
	if strings.Contains(a.KeyHint.Get()+flat(a), "987654") {
		t.Error("the PIN is in a message")
	}
	waitFor(t, q, func() bool { return a.Screen.Get() == ScreenContainer })
	if a.KeyState.Get() != KeyIdle || a.KeyHint.Get() != "" {
		t.Errorf("not idle after unlock: %q %q", a.KeyState.Get(), a.KeyHint.Get())
	}
}

// A password-less key unlock is not offered: the key never opens alone.
func TestKeyNeverUnlocksAlone(t *testing.T) {
	a, _ := newApp(&fakeService{sess: &fakeSession{}})
	a.Unlock.UseKey.Set(true)
	if a.Unlock.Submit.CanExecute() {
		t.Fatal("unlock is offered with a key and no password or recovery code")
	}
	a.Unlock.Recovery.Set("ABCD")
	if !a.Unlock.Submit.CanExecute() {
		t.Fatal("a recovery code with the key is not offered")
	}
}

func TestKeyStateAtEnrol(t *testing.T) {
	q := mvvm.NewQueue(nil)
	a := New(&fakeService{sess: &fakeSession{}}, Host{}, q, nil)
	fillUnlock(a)
	a.Unlock.Submit.Execute()
	waitFor(t, q, func() bool { return a.Screen.Get() == ScreenContainer })
	c := a.Container
	c.EnrollPassword.Set("pw")
	c.EnrollKey.Execute()
	if a.KeyState.Get() != KeyTouch {
		t.Fatalf("enrol: state %q", a.KeyState.Get())
	}
	// The fake key asks for the PIN: PIN, then touch again, then idle.
	waitFor(t, q, func() bool { return a.KeyState.Get() == KeyPIN })
	a.PIN.Value.Set("1234")
	a.PIN.Submit.Execute()
	waitFor(t, q, func() bool { return a.KeyState.Get() == KeyIdle })
}

func TestKeyNoteOnWindows(t *testing.T) {
	saved := goos
	t.Cleanup(func() { goos = saved })
	for os, want := range map[string]string{"windows": WindowsKeyNote, "darwin": "", "linux": ""} {
		goos = os
		a, _ := newApp(&fakeService{})
		if a.KeyNote.Get() != want {
			t.Errorf("%s: note %q", os, a.KeyNote.Get())
		}
	}
}
