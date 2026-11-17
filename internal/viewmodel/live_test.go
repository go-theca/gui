// SPDX-License-Identifier: BSD-3-Clause

package viewmodel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
// back: what was written went through the container. The container itself is
// created through the ViewModel's Create, so it also proves that what the GUI
// creates can be shared.
//
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
	a, q := newApp(Backend{V: &vault.Service{Hint: 1}})
	s := a.Container.Share

	// Create through the ViewModel, with the default filesystem (exFAT).
	a.GoCreate.Execute()
	a.Create.Path.Set(path)
	a.Create.Size.Set("64M")
	a.Create.Password.Set(pw)
	a.Create.Confirm.Set(pw)
	if a.Create.FS.Get() != FSExFAT {
		t.Fatal("exFAT is not the default")
	}
	a.Create.Submit.Execute()
	q.Drain()
	if a.Screen.Get() != ScreenRecovery {
		t.Fatalf("screen %q: %s", a.Screen.Get(), flat(a))
	}
	a.Create.Acked.Set(true)
	a.Create.Done.Execute()
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

// TestLiveUnlockTime times one real unlock with the production Argon2id
// profile (hint 0, 2 GiB). A single run: indicative, not a benchmark.
//
//	THECA_GUI_LIVE=1 go test -count=1 -run TestLiveUnlockTime -v ./internal/viewmodel
func TestLiveUnlockTime(t *testing.T) {
	if os.Getenv("THECA_GUI_LIVE") == "" {
		t.Skip("set THECA_GUI_LIVE=1 to time a real unlock")
	}
	svc := &vault.Service{} // hint 0: what the theca command and the GUI use
	path := filepath.Join(t.TempDir(), "t.theca")
	if _, err := svc.Create(vault.CreateRequest{Path: path, Size: "64K", Password: []byte("pw")}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	sess, err := svc.Open(context.Background(), vault.UnlockRequest{Path: path, Password: []byte("pw")})
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	sess.Close()
	t.Logf("unlock took %v", took.Round(time.Millisecond))
}

// TestLiveNetworkShare shares on the NETWORK through the ViewModel against a
// real container and a real mount, and asks the OS what it negotiated:
// smbutil must report encryption and signing REQUIRED. macOS only.
//
//	THECA_GUI_LIVE=1 go test -count=1 -run TestLiveNetworkShare -v ./internal/viewmodel
func TestLiveNetworkShare(t *testing.T) {
	if os.Getenv("THECA_GUI_LIVE") == "" {
		t.Skip("set THECA_GUI_LIVE=1 to mount a real share")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("smbutil is macOS only")
	}
	const pw = "live-network-password"
	path := filepath.Join(t.TempDir(), "net.theca")
	a, q := newApp(Backend{V: &vault.Service{Hint: 1}})
	a.GoCreate.Execute()
	a.Create.Path.Set(path)
	a.Create.Size.Set("64M")
	a.Create.Password.Set(pw)
	a.Create.Confirm.Set(pw)
	a.Create.Submit.Execute()
	q.Drain()
	a.Create.Acked.Set(true)
	a.Create.Done.Execute()
	a.Path.Set(path)
	a.Unlock.Password.Set(pw)
	a.Unlock.Submit.Execute()
	q.Drain()
	if a.Screen.Get() != ScreenContainer {
		t.Fatalf("screen %q: %s", a.Screen.Get(), flat(a))
	}
	s := a.Container.Share
	s.Network.Set(true)
	s.Toggle.Execute()
	q.Drain()
	if s.State.Get() != ShareShared {
		t.Fatalf("state %q: %s", s.State.Get(), flat(a))
	}
	t.Logf("scope %q; %s; mounted at %s", s.Scope.Get(), s.Addr.Get(), s.MountPath.Get())
	file := filepath.Join(s.MountPath.Get(), "net.txt")
	if err := os.WriteFile(file, []byte("over the network share"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "over the network share" {
		t.Fatalf("read back %q: %v", b, err)
	}
	out, err := exec.Command("smbutil", "statshares", "-a").CombinedOutput()
	if err != nil {
		t.Fatalf("smbutil: %v\n%s", err, out)
	}
	attrs := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			attrs[f[0]] = f[1]
		}
	}
	if attrs["ENCRYPTION_REQUIRED"] != "TRUE" || attrs["SIGNING_REQUIRED"] != "TRUE" ||
		!strings.HasPrefix(attrs["SMB_CURR_ENCRYPT_ALGORITHM"], "AES") {
		t.Errorf("smbutil: encryption required %q, signing required %q, cipher %q",
			attrs["ENCRYPTION_REQUIRED"], attrs["SIGNING_REQUIRED"], attrs["SMB_CURR_ENCRYPT_ALGORITHM"])
	}
	t.Logf("smbutil: encryption %s (required %s), signing %s (required %s)",
		attrs["SMB_CURR_ENCRYPT_ALGORITHM"], attrs["ENCRYPTION_REQUIRED"], attrs["SMB_CURR_SIGN_ALGORITHM"], attrs["SIGNING_REQUIRED"])
	// The password stays out of sight until asked.
	if s.Password.Get() != "" {
		t.Error("the password is shown")
	}
	s.Toggle.Execute()
	q.Drain()
	gone(t, filepath.Dir(file), "stopping")
	a.Shutdown()
}
