// SPDX-License-Identifier: BSD-3-Clause

package view

import (
	"context"
	"strings"
	"testing"

	"github.com/go-theca/theca/profile"
	thsession "github.com/go-theca/theca/session"

	"github.com/go-theca/gui/internal/viewmodel"
)

// --- fakes: no file, no socket, no process ----------------------------------

type store struct{}

func (store) List() ([]profile.Profile, error) {
	return []profile.Profile{profile.New("bio", "/vault/bio.theca")}, nil
}

type sessions struct{ closed []string }

func (s *sessions) Status(context.Context, string) (thsession.Info, error) {
	return thsession.Info{}, thsession.ErrNotOpen
}

func (s *sessions) Close(_ context.Context, name string) error {
	s.closed = append(s.closed, name)
	return nil
}

type spawner struct{ started []string }

func (s *spawner) Start(_ context.Context, name string, _ func(string)) (thsession.Info, error) {
	s.started = append(s.started, name)
	return thsession.Info{Name: name, Mount: "/Volumes/THECA", Since: 1700000000}, nil
}

// The profiles screen is reached from the home screen, lists the profiles, and
// its buttons drive the view model.
func TestProfilesScreen(t *testing.T) {
	v, app, _ := newView()
	defer v.Close()
	frame(v)
	click(v, button(t, v, "My profiles..."))
	frame(v)
	if app.Screen.Get() != viewmodel.ScreenProfiles {
		t.Fatalf("screen %q", app.Screen.Get())
	}
	if got := app.Profiles.Lines.Slice(); len(got) != 1 || got[0] != "bio: closed" {
		t.Fatalf("lines %q", got)
	}
	// With nothing selected Open is disabled, and clicking it does nothing.
	click(v, button(t, v, "Open"))
	frame(v)
	if app.Profiles.Rows()[0].State != viewmodel.ProfileClosed {
		t.Fatal("opened without a selection")
	}
	app.Profiles.Selected.Set(0)
	click(v, button(t, v, "Open"))
	frame(v)
	if r := app.Profiles.Rows()[0]; r.State != viewmodel.ProfileOpen || r.Mount != "/Volumes/THECA" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(strings.Join(app.Profiles.Lines.Slice(), " "), "/Volumes/THECA") {
		t.Fatal(app.Profiles.Lines.Slice())
	}
	click(v, button(t, v, "Close"))
	frame(v)
	if app.Profiles.Rows()[0].State != viewmodel.ProfileClosed {
		t.Fatal("not closed")
	}
	click(v, button(t, v, "Refresh"))
	frame(v)
	click(v, button(t, v, "Back"))
	frame(v)
	if app.Screen.Get() != viewmodel.ScreenHome {
		t.Fatalf("screen %q", app.Screen.Get())
	}
}
