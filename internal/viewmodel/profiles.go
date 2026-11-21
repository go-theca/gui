// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-widgets/mvvm"

	"github.com/go-theca/theca/profile"
	"github.com/go-theca/theca/session"
)

// The states of a profile, as ProfileRow.State.
const (
	ProfileClosed  = "closed"
	ProfileOpen    = "open"
	ProfileOpening = "opening"
	ProfileTouch   = "touch"
	ProfileClosing = "closing"
	ProfileError   = "error"
)

// Timeouts: opening waits for a person to touch a key; closing waits for the
// unmount and the commit.
const (
	OpenTimeout  = 5 * time.Minute
	CloseTimeout = 2 * time.Minute
)

// NoProfiles is shown when containers.toml names nothing.
const NoProfiles = "No profile yet. Name a container once, in a terminal: theca profile add NAME FILE --password-from REFERENCE"

// ProfileStore lists the profiles of containers.toml: *profile.Store.
type ProfileStore interface {
	List() ([]profile.Profile, error)
}

// SessionClient talks to the background processes that hold open containers
// (theca's session package, by the profile's name).
type SessionClient interface {
	// Status says what is open under the profile; session.ErrNotOpen when nothing.
	Status(ctx context.Context, name string) (session.Info, error)
	// Close unmounts, commits and closes the container.
	Close(ctx context.Context, name string) error
}

// Spawner starts the background process that unlocks and mounts a profile
// (session.Start). notice receives each line the process writes while it
// unlocks, from another goroutine, until Start returns.
type Spawner interface {
	Start(ctx context.Context, name string, notice func(string)) (session.Info, error)
}

// Host is what the profiles need from the machine.
type Host struct {
	Store    ProfileStore
	Sessions SessionClient
	Spawner  Spawner
}

// ProfileRow is one profile and its state.
type ProfileRow struct {
	Name  string
	State string
	Mount string
	Since time.Time
	Error string
}

// Line is the row in words.
func (r ProfileRow) Line() string {
	switch r.State {
	case ProfileOpen:
		return r.Name + ": open, mounted at " + r.Mount + ", since " + r.Since.Local().Format("2006-01-02 15:04")
	case ProfileOpening:
		return r.Name + ": opening..."
	case ProfileTouch:
		return r.Name + ": opening - touch your security key now"
	case ProfileClosing:
		return r.Name + ": closing..."
	case ProfileError:
		return r.Name + ": " + r.Error
	}
	return r.Name + ": closed"
}

// ProfilesVM lists the profiles of containers.toml with their state, and opens
// and closes them with the code the command line uses: profile.Store,
// session.Status, session.Close and session.Start. Nothing of theca's logic is
// repeated here.
type ProfilesVM struct {
	app  *App
	host Host
	rows []ProfileRow
	// cancel ends an open in progress.
	cancel context.CancelFunc

	// Lines is the list shown, one line per profile; Selected is the index of the
	// chosen one, -1 for none.
	Lines    *mvvm.ObservableList[string]
	Selected *mvvm.Observable[int]
	// Empty is NoProfiles while there is no profile, "" otherwise.
	Empty *mvvm.Observable[string]
	// Busy is set while the list loads, or one profile opens or closes.
	Busy *mvvm.Observable[bool]
	// Opening is set while an open waits (for the unlock, or a key's touch).
	Opening *mvvm.Observable[bool]

	Refresh *mvvm.Command
	Open    *mvvm.Command
	Close   *mvvm.Command
	// Cancel gives up an open that waits.
	Cancel *mvvm.Command
	Back   *mvvm.Command
}

func newProfilesVM(a *App, h Host) *ProfilesVM {
	p := &ProfilesVM{
		app:      a,
		host:     h,
		Lines:    mvvm.NewObservableList[string](),
		Selected: mvvm.NewObservable(-1),
		Empty:    mvvm.NewObservable(""),
		Busy:     mvvm.NewObservable(false),
		Opening:  mvvm.NewObservable(false),
	}
	p.Refresh = mvvm.NewCommand(func() { p.refresh() }, func() bool { return !p.Busy.Get() })
	mvvm.BindCanExecute(p.Refresh, p.Busy)
	p.Open = mvvm.NewCommand(p.open, func() bool { return p.selectedIs(ProfileClosed) })
	mvvm.BindCanExecute(p.Open, p.Busy, p.Selected, p.Lines)
	p.Close = mvvm.NewCommand(p.close, func() bool { return p.selectedIs(ProfileOpen) })
	mvvm.BindCanExecute(p.Close, p.Busy, p.Selected, p.Lines)
	p.Cancel = mvvm.NewCommand(func() { p.cancel() }, func() bool { return p.Opening.Get() })
	mvvm.BindCanExecute(p.Cancel, p.Opening)
	p.Back = mvvm.NewCommand(func() { a.show(ScreenHome) }, func() bool { return !p.Busy.Get() })
	mvvm.BindCanExecute(p.Back, p.Busy)
	return p
}

// selectedIs reports whether nothing is running and the selected profile is in
// the state.
func (p *ProfilesVM) selectedIs(state string) bool {
	i := p.Selected.Get()
	return !p.Busy.Get() && i >= 0 && i < len(p.rows) && p.rows[i].State == state
}

// Rows is a copy of the profiles and their states.
func (p *ProfilesVM) Rows() []ProfileRow { return append([]ProfileRow(nil), p.rows...) }

// show opens the screen and reads the state afresh.
func (p *ProfilesVM) show() {
	p.app.show(ScreenProfiles)
	p.refresh()
}

// set replaces the rows, keeping the selection on the same name.
func (p *ProfilesVM) set(rows []ProfileRow) {
	keep := ""
	if i := p.Selected.Get(); i >= 0 && i < len(p.rows) {
		keep = p.rows[i].Name
	}
	p.rows = rows
	lines := make([]string, len(rows))
	sel := -1
	for i, r := range rows {
		lines[i] = r.Line()
		if r.Name == keep {
			sel = i
		}
	}
	p.Lines.Clear()
	p.Lines.Append(lines...)
	p.Selected.Set(sel)
	if len(rows) == 0 {
		p.Empty.Set(NoProfiles)
	} else {
		p.Empty.Set("")
	}
}

// update changes one row, looked up by name.
func (p *ProfilesVM) update(name string, f func(*ProfileRow)) {
	for i := range p.rows {
		if p.rows[i].Name == name {
			f(&p.rows[i])
			p.Lines.Set(i, p.rows[i].Line())
			return
		}
	}
}

// load reads the profiles and asks each one's session. It runs off the UI
// goroutine.
func (p *ProfilesVM) load() ([]ProfileRow, error) {
	ps, err := p.host.Store.List()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), CloseTimeout)
	defer cancel()
	rows := make([]ProfileRow, len(ps))
	for i, pr := range ps {
		rows[i] = ProfileRow{Name: pr.Name, State: ProfileClosed}
		info, err := p.host.Sessions.Status(ctx, pr.Name)
		switch {
		case err == nil:
			rows[i].State, rows[i].Mount, rows[i].Since = ProfileOpen, info.Mount, time.Unix(info.Since, 0)
		case errors.Is(err, session.ErrNotOpen):
		default:
			rows[i].State, rows[i].Error = ProfileError, err.Error()
		}
	}
	return rows, nil
}

func (p *ProfilesVM) refresh() {
	var rows []ProfileRow
	var err error
	p.app.async(p.Busy, func() { rows, err = p.load() }, func() {
		if err != nil {
			p.app.Fail(err)
			return
		}
		p.set(rows)
	})
}

// notice is a line the background process wrote while it unlocks: the key's
// touch notice moves the state to "touch".
func (p *ProfilesVM) notice(name, line string) {
	if !strings.Contains(strings.ToLower(line), "touch") {
		return
	}
	p.update(name, func(r *ProfileRow) { r.State = ProfileTouch })
	p.app.setKey(KeyTouch)
}

func (p *ProfilesVM) open() {
	name := p.rows[p.Selected.Get()].Name
	ctx, cancel := context.WithTimeout(context.Background(), OpenTimeout)
	p.cancel = cancel
	p.Opening.Set(true)
	p.update(name, func(r *ProfileRow) { r.State = ProfileOpening })
	p.app.Say("opening " + name + "...")
	var info session.Info
	var err error
	p.app.async(p.Busy, func() {
		info, err = p.host.Spawner.Start(ctx, name, func(line string) {
			p.app.q.Post(func() { p.notice(name, line) })
		})
	}, func() {
		cancel()
		p.Opening.Set(false)
		p.app.setKey(KeyIdle)
		if err != nil {
			p.update(name, func(r *ProfileRow) { r.State = ProfileClosed })
			p.app.Fail(err)
			return
		}
		p.update(name, func(r *ProfileRow) {
			r.State, r.Mount, r.Since = ProfileOpen, info.Mount, time.Unix(info.Since, 0)
		})
		p.app.Say(name + " is open, mounted at " + info.Mount)
	})
}

func (p *ProfilesVM) close() {
	name := p.rows[p.Selected.Get()].Name
	p.update(name, func(r *ProfileRow) { r.State = ProfileClosing })
	p.app.Say("closing " + name + "...")
	var err error
	p.app.async(p.Busy, func() {
		ctx, cancel := context.WithTimeout(context.Background(), CloseTimeout)
		defer cancel()
		err = p.host.Sessions.Close(ctx, name)
	}, func() {
		if errors.Is(err, session.ErrNotOpen) {
			// Closed by something else meanwhile: that is what is true.
			err = nil
		}
		if err != nil {
			p.update(name, func(r *ProfileRow) { r.State = ProfileOpen })
			p.app.Fail(err)
			return
		}
		p.update(name, func(r *ProfileRow) { r.State, r.Mount = ProfileClosed, "" })
		p.app.Say(name + " is closed")
	})
}
