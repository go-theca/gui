// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"context"

	"github.com/go-widgets/mvvm"
)

// PINPrompt is the security key's PIN, asked for only if the key wants it. The
// key is driven from a background goroutine; when it calls the function Ask
// returns, the prompt becomes Needed, the View shows its PIN field, and the
// goroutine waits until Submit hands it what was typed (or until it is
// cancelled).
type PINPrompt struct {
	q *mvvm.Queue
	// Needed is true while a key is waiting for its PIN.
	Needed *mvvm.Observable[bool]
	// Value is what the user types, in a masked entry. It is emptied on Submit.
	Value *mvvm.Observable[string]
	// Submit hands the PIN to the waiting key.
	Submit *mvvm.Command

	ch chan string
}

func newPINPrompt(q *mvvm.Queue) *PINPrompt {
	p := &PINPrompt{
		q:      q,
		Needed: mvvm.NewObservable(false),
		Value:  mvvm.NewObservable(""),
		ch:     make(chan string, 1),
	}
	p.Submit = mvvm.NewCommand(p.submit, func() bool { return p.Needed.Get() })
	mvvm.BindCanExecute(p.Submit, p.Needed)
	return p
}

func (p *PINPrompt) submit() {
	pin := p.Value.Get()
	p.Value.Set("")
	p.Needed.Set(false)
	p.ch <- pin
}

// Ask returns the function a key calls to get its PIN. It is called from the
// background goroutine, so it only posts to the UI and waits; ctx releases it.
func (p *PINPrompt) Ask(ctx context.Context) func() (string, error) {
	return func() (string, error) {
		p.q.Post(func() { p.Needed.Set(true) })
		select {
		case pin := <-p.ch:
			return pin, nil
		case <-ctx.Done():
			p.q.Post(func() {
				p.Needed.Set(false)
				p.Value.Set("")
			})
			return "", ctx.Err()
		}
	}
}
