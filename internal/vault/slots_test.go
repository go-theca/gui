// SPDX-License-Identifier: BSD-3-Clause

package vault

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-theca/theca"
)

func openSlots(t *testing.T) (*Session, string) {
	t.Helper()
	path, code := newContainer(t, PolicyAny)
	sess, err := (&Service{Hint: 1}).Open(context.Background(), UnlockRequest{Path: path, Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess, code
}

func TestListSlotsAndRemoveByFactors(t *testing.T) {
	sess, code := openSlots(t)
	l, err := sess.ListSlots([]byte("pw"), code)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Recorded) != 2 || len(l.Opened[SetPassword]) != 1 || len(l.Opened[SetRecovery]) != 1 || len(l.Opened[SetPasswordRecovery]) != 0 {
		t.Fatalf("listing %+v", l)
	}
	// Only the password typed: only it is listed.
	l, err = sess.ListSlots([]byte("pw"), "")
	if err != nil || len(l.Opened) != 1 {
		t.Fatalf("password only: %+v %v", l, err)
	}

	// The last way in is refused, and nothing changes.
	if _, err := sess.RemoveFactors(SetPassword, SetPassword, []byte("pw"), code); !errors.Is(err, ErrLastWay) {
		t.Fatalf("same set: %v", err)
	}
	if _, err := sess.RemoveFactors(SetRecovery, SetPasswordRecovery, []byte("pw"), code); !errors.Is(err, ErrLastWay) {
		t.Fatalf("a keep set that opens nothing: %v", err)
	}
	if l, _ := sess.ListSlots([]byte("pw"), code); len(l.Opened[SetRecovery]) != 1 {
		t.Fatal("a refusal changed the slots")
	}

	// Remove the recovery code, keeping the password.
	n, err := sess.RemoveFactors(SetRecovery, SetPassword, []byte("pw"), code)
	if err != nil || n != 1 {
		t.Fatalf("RemoveFactors = %d, %v", n, err)
	}
	l, _ = sess.ListSlots([]byte("pw"), code)
	if len(l.Opened[SetRecovery]) != 0 || len(l.Opened[SetPassword]) != 1 || len(l.Recorded) != 1 {
		t.Fatalf("after: %+v", l)
	}
}

func TestSlotRefusals(t *testing.T) {
	sess, code := openSlots(t)
	for name, f := range map[string]func() error{
		"remove: no recovery typed": func() error {
			_, err := sess.RemoveFactors(SetRecovery, SetPassword, []byte("pw"), "")
			return err
		},
		"keep: no password typed": func() error {
			_, err := sess.RemoveFactors(SetRecovery, SetPassword, nil, code)
			return err
		},
		"unknown set": func() error {
			_, err := sess.RemoveFactors("key", SetPassword, []byte("pw"), code)
			return err
		},
		"bad recovery code": func() error {
			_, err := sess.ListSlots([]byte("pw"), "not a code")
			return err
		},
		"bad recovery code on remove": func() error {
			_, err := sess.RemoveFactors(SetRecovery, SetPassword, []byte("pw"), "not a code")
			return err
		},
	} {
		if err := f(); err == nil || errors.Is(err, ErrLastWay) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := sess.RemoveFactors(SetRecovery, SetPassword, []byte("pw"), ""); !strings.Contains(err.Error(), "recovery code is needed") {
		t.Errorf("the message does not say what is missing: %v", err)
	}
	// Nothing was removed by any of them.
	if l, _ := sess.ListSlots([]byte("pw"), code); len(l.Recorded) != 2 {
		t.Fatalf("recorded %v", l.Recorded)
	}
}

func TestSlotsNeedAContainerThatHasThem(t *testing.T) {
	s := &Session{c: &brokenVolume{}}
	if _, err := s.ListSlots([]byte("pw"), ""); err == nil {
		t.Error("ListSlots on a volume with no slots")
	}
	if _, err := s.RemoveFactors(SetPassword, SetRecovery, []byte("pw"), ""); err == nil {
		t.Error("RemoveFactors on a volume with no slots")
	}
}

func TestPasswordFactorFailureIsReported(t *testing.T) {
	sess, _ := openSlots(t)
	saved := passwordFactor
	t.Cleanup(func() { passwordFactor = saved })
	boom := errors.New("kdf boom")
	passwordFactor = func(int, []byte, []byte) (theca.Factor, error) { return theca.Factor{}, boom }
	if _, err := sess.ListSlots([]byte("pw"), ""); !errors.Is(err, boom) {
		t.Fatalf("ListSlots: %v", err)
	}
	if _, err := sess.RemoveFactors(SetRecovery, SetPassword, []byte("pw"), ""); !errors.Is(err, boom) {
		t.Fatalf("RemoveFactors: %v", err)
	}
}

// A listing that fails while trial-opening the slots says so.
func TestListSlotsFailure(t *testing.T) {
	boom := errors.New("read boom")
	s := &Session{c: &slotFailer{err: boom}, salt: make([]byte, saltSize)}
	if _, err := s.ListSlots([]byte("pw"), ""); !errors.Is(err, boom) {
		t.Fatalf("ListSlots: %v", err)
	}
}

// slotFailer is a volume whose slot calls fail.
type slotFailer struct {
	brokenVolume
	err error
}

func (f *slotFailer) Slots() []int { return nil }
func (f *slotFailer) SlotsOf(...theca.Factor) ([]int, error) {
	return nil, f.err
}
func (f *slotFailer) RemoveFactors(_, _ []theca.Factor) (int, error) { return 0, f.err }
