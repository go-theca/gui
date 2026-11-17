// SPDX-License-Identifier: BSD-3-Clause

package vault

import (
	"errors"
	"fmt"

	"github.com/go-theca/theca"
)

// ErrLastWay is the refusal of a removal after which the factors to keep would
// not open the container. Nothing was written.
var ErrLastWay = theca.ErrLastWay

// FactorSet names the factors that open one slot: the password, the recovery
// code, or the two together (a 2-of-3 container's slot).
type FactorSet string

const (
	SetPassword         FactorSet = "password"
	SetRecovery         FactorSet = "recovery"
	SetPasswordRecovery FactorSet = "password+recovery"
)

// FactorSets is every set, in the order they are listed and offered.
var FactorSets = []FactorSet{SetPassword, SetRecovery, SetPasswordRecovery}

// SlotListing is what the container can say about its slots: the ones the
// header records, and, for each factor set the caller presented, the slots it
// opens. A slot holds nothing that says what opens it, so nothing else is known.
type SlotListing struct {
	Recorded []int
	Opened   map[FactorSet][]int
}

// slotter is what a container offers for slot management; *theca.Container is
// one.
type slotter interface {
	Slots() []int
	SlotsOf(fs ...theca.Factor) ([]int, error)
	RemoveFactors(remove, keep []theca.Factor) (int, error)
}

// presented derives the password and recovery factors from what the user
// typed now (nothing derived from them was kept at unlock). The caller wipes
// them with wipe.
type presentedFactors map[string]*theca.Factor

func (p presentedFactors) wipe() {
	for _, f := range p {
		f.Zeroize()
	}
}

// wipeAll wipes copies of factors that resolving a set handed out.
func wipeAll(fs []theca.Factor) {
	for i := range fs {
		fs[i].Zeroize()
	}
}

func (s *Session) present(password []byte, recovery string) (presentedFactors, error) {
	p := presentedFactors{}
	if len(password) > 0 {
		pw, err := passwordFactor(s.hint, password, s.salt)
		if err != nil {
			return nil, err
		}
		p["pw"] = &pw
	}
	if recovery != "" {
		rc, err := recoveryFactor(recovery)
		if err != nil {
			p.wipe()
			return nil, err
		}
		p["rc"] = &rc
	}
	return p, nil
}

// factors resolves a set to the factors presented, or says which is missing.
func (p presentedFactors) factors(set FactorSet) ([]theca.Factor, error) {
	var kinds []string
	switch set {
	case SetPassword:
		kinds = []string{"pw"}
	case SetRecovery:
		kinds = []string{"rc"}
	case SetPasswordRecovery:
		kinds = []string{"pw", "rc"}
	default:
		return nil, fmt.Errorf("unknown factor set %q", set)
	}
	var out []theca.Factor
	for _, k := range kinds {
		f, ok := p[k]
		if !ok {
			name := "password"
			if k == "rc" {
				name = "recovery code"
			}
			return nil, fmt.Errorf("the %s is needed for %q: type it again", name, string(set))
		}
		out = append(out, *f)
	}
	return out, nil
}

func (s *Session) slotter() (slotter, error) {
	sl, ok := s.c.(slotter)
	if !ok {
		return nil, errors.New("this container cannot manage its slots")
	}
	return sl, nil
}

// ListSlots says which slots the header records and which ones the password
// and/or recovery code typed NOW open. password is wiped by the caller.
func (s *Session) ListSlots(password []byte, recovery string) (SlotListing, error) {
	sl, err := s.slotter()
	if err != nil {
		return SlotListing{}, err
	}
	p, err := s.present(password, recovery)
	if err != nil {
		return SlotListing{}, err
	}
	defer p.wipe()
	out := SlotListing{Recorded: sl.Slots(), Opened: map[FactorSet][]int{}}
	for _, set := range FactorSets {
		fs, err := p.factors(set)
		if err != nil {
			continue // not presented
		}
		idx, err := sl.SlotsOf(fs...)
		wipeAll(fs)
		if err != nil {
			return SlotListing{}, err
		}
		out.Opened[set] = idx
	}
	return out, nil
}

// RemoveFactors removes the slot(s) the `remove` set opens. The `keep` set, a
// different way in, must still open the container afterwards; that is checked
// before anything is written, and a removal that would leave no way in is
// refused with ErrLastWay. It returns how many slots it removed. password is
// wiped by the caller.
func (s *Session) RemoveFactors(remove, keep FactorSet, password []byte, recovery string) (int, error) {
	sl, err := s.slotter()
	if err != nil {
		return 0, err
	}
	p, err := s.present(password, recovery)
	if err != nil {
		return 0, err
	}
	defer p.wipe()
	rm, err := p.factors(remove)
	if err != nil {
		return 0, err
	}
	defer wipeAll(rm)
	kp, err := p.factors(keep)
	if err != nil {
		return 0, err
	}
	defer wipeAll(kp)
	return sl.RemoveFactors(rm, kp)
}
