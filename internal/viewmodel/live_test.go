// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	exfat "github.com/go-filesystems/exfat"
	"github.com/go-theca/theca"

	"github.com/go-theca/gui/internal/vault"
)

// TestLiveShare drives the ViewModel against a REAL container and a REAL mount
// in the file manager. It mounts a volume on the machine it runs on, so it
// runs only when asked:
//
//	THECA_GUI_LIVE=1 go test -count=1 -run TestLiveShare -v ./internal/viewmodel
//
// It needs a desktop where go-fileshare/automount works (macOS today). It
// shares, writes and reads a file through the mounted volume, stops, checks
// the volume is gone, locks, unlocks again, shares again, and reads the file
// back: what was written went through the container.
// gone waits for the OS to drop a mount point: the unmount call returns before
// the mount table is always updated, so it is polled, and how long it took is
// logged.
func gone(t *testing.T, mount, after string) {
	t.Helper()
	start := time.Now()
	for time.Since(start) < 10*time.Second {
		if _, err := os.Stat(mount); os.IsNotExist(err) {
			t.Logf("%s gone %v after %s", mount, time.Since(start).Round(time.Millisecond), after)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s is still there 10 s after %s", mount, after)
}

func TestLiveShare(t *testing.T) {
	if os.Getenv("THECA_GUI_LIVE") == "" {
		t.Skip("set THECA_GUI_LIVE=1 to mount a real share")
	}
	const pw = "live-share-password"
	path := filepath.Join(t.TempDir(), "live.theca")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	c, _, err := theca.Create(theca.FileBacking{File: f}, theca.CreateOptions{
		Sectors: (64 << 20) / theca.SectorSize, Hint: 1, Password: []byte(pw),
	})
	if err != nil {
		t.Fatal(err)
	}
	fsys, err := exfat.FormatImage(c, c.Size(), exfat.FormatConfig{Label: "THECA"})
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	a, q := newApp(Backend{V: &vault.Service{Hint: 1}})
	s := a.Container.Share
	unlock := func() {
		a.Path.Set(path)
		a.Unlock.Password.Set(pw)
		a.Unlock.Submit.Execute()
		q.Drain()
		if a.Screen.Get() != ScreenContainer {
			t.Fatalf("screen %q: %s", a.Screen.Get(), flat(a))
		}
	}
	share := func() string {
		s.Toggle.Execute()
		q.Drain()
		if s.State.Get() != ShareShared || s.MountPath.Get() == "" {
			t.Fatalf("state %q mount %q: %s", s.State.Get(), s.MountPath.Get(), flat(a))
		}
		t.Logf("shared at %s, mounted at %s", s.URL.Get(), s.MountPath.Get())
		return s.MountPath.Get()
	}
	stop := func(mount string) {
		s.Toggle.Execute()
		q.Drain()
		if s.State.Get() != ShareIdle {
			t.Fatalf("state %q: %s", s.State.Get(), flat(a))
		}
		gone(t, mount, "stopping")
	}

	unlock()
	mount := share()
	file := filepath.Join(mount, "from-the-viewmodel.txt")
	if err := os.WriteFile(file, []byte("written through the share"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "written through the share" {
		t.Fatalf("read back %q: %v", b, err)
	}
	time.Sleep(200 * time.Millisecond)
	// Lock with the share still running: the share stops first, then the
	// container commits and closes.
	a.Container.Close.Execute()
	q.Drain()
	if a.Screen.Get() != ScreenHome || messages(a) != "" {
		t.Fatalf("screen %q: %s", a.Screen.Get(), flat(a))
	}
	gone(t, mount, "locking")

	unlock()
	a.Container.Verify.Execute()
	q.Drain()
	mount = share()
	if b, err := os.ReadFile(filepath.Join(mount, "from-the-viewmodel.txt")); err != nil || string(b) != "written through the share" {
		t.Fatalf("after locking and unlocking: %q: %v", b, err)
	}
	stop(mount)
	a.Shutdown() // the window closing, with no share running
	if a.Container.sess != nil {
		t.Error("shutdown left the container open")
	}
}
