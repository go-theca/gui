// SPDX-License-Identifier: BSD-3-Clause

package vault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/go-theca/theca/session"
)

// TestMain lets the test binary stand in for `theca _serve NAME`: when it is
// started that way with THECA_FAKE_DAEMON set, it answers on the descriptor the
// parent passed (3) the way the real process does, and exits. No container, no
// key, no password manager.
func TestMain(m *testing.M) {
	if mode := os.Getenv("THECA_FAKE_DAEMON"); mode != "" && len(os.Args) == 3 && os.Args[1] == "_serve" {
		os.Stderr.WriteString("Touch the security key now\n")
		ready := os.NewFile(3, "ready")
		if mode == "fail" {
			session.Report(ready, session.Response{Error: "the password manager refused"})
		} else {
			session.Report(ready, session.Response{OK: true, Info: &session.Info{Name: os.Args[2], Mount: "/Volumes/FAKE", Since: 1700000000}})
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeHandler struct {
	info session.Info
	err  error
}

func (h fakeHandler) Status() session.Info { return h.info }
func (h fakeHandler) Close() error         { return h.err }

// shortRuntimeDir is a directory whose sockets fit sun_path.
func shortRuntimeDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "g")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	t.Setenv("XDG_RUNTIME_DIR", d)
	return d
}

func TestSessionsAgainstARealSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets")
	}
	shortRuntimeDir(t)
	sp, err := session.SocketPath("bio")
	if err != nil {
		t.Fatal(err)
	}
	s := Sessions{}
	if _, err := s.Status(context.Background(), "bio"); err == nil {
		t.Fatal("a status with nothing listening")
	}
	srv, err := session.Listen(sp)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		srv.Serve(fakeHandler{info: session.Info{Name: "bio", Mount: "/Volumes/BIO", Since: 5}})
	}()
	defer func() { srv.Shutdown(); wg.Wait() }()

	info, err := s.Status(context.Background(), "bio")
	if err != nil || info.Mount != "/Volumes/BIO" || info.Since != 5 {
		t.Fatalf("%+v %v", info, err)
	}
	if err := s.Close(context.Background(), "bio"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionsRefuseABadName(t *testing.T) {
	shortRuntimeDir(t)
	// The refusal is the name's own, not a failure to dial an empty path.
	if _, err := (Sessions{}).Status(context.Background(), "a/b"); err == nil || !strings.Contains(err.Error(), "bad name") {
		t.Fatalf("status of a bad name: %v", err)
	}
	if err := (Sessions{}).Close(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "bad name") {
		t.Fatalf("close of a bad name: %v", err)
	}
}

func TestProfileStore(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HOME", cfg)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("APPDATA", cfg)
	st, err := ProfileStore()
	if err != nil || filepath.Base(st.Path) != "containers.toml" {
		t.Fatalf("%+v %v", st, err)
	}
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")
	if _, err := ProfileStore(); err == nil {
		t.Fatal("a store without a configuration directory")
	}
}

func selfExe() (string, error) { return os.Args[0], nil }

func TestSpawnerStarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("descriptor 3")
	}
	t.Setenv("THECA_FAKE_DAEMON", "ok")
	info, err := Spawner{Exe: selfExe}.Start(context.Background(), "bio", nil)
	if err != nil || info.Name != "bio" || info.Mount != "/Volumes/FAKE" {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestSpawnerHandsOverTheNotices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("descriptor 3")
	}
	t.Setenv("THECA_FAKE_DAEMON", "fail")
	var mu sync.Mutex
	var got []string
	_, err := Spawner{Exe: selfExe}.Start(context.Background(), "bio", func(l string) {
		mu.Lock()
		got = append(got, l)
		mu.Unlock()
	})
	if err == nil || !strings.Contains(err.Error(), "the password manager refused") {
		t.Fatalf("%v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || !strings.Contains(got[0], "Touch") {
		t.Fatalf("notices %q", got)
	}
}

func TestSpawnerWithoutTheca(t *testing.T) {
	want := errors.New("no such binary")
	_, err := Spawner{Exe: func() (string, error) { return "", want }}.Start(context.Background(), "bio", nil)
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestSpawnerFindsTheca(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("descriptor 3")
	}
	saved := lookPath
	t.Cleanup(func() { lookPath = saved })
	lookPath = func(string) (string, error) { return os.Args[0], nil }
	t.Setenv("THECA_FAKE_DAEMON", "ok")
	if _, err := (Spawner{}).Start(context.Background(), "bio", nil); err != nil {
		t.Fatal(err)
	}
}

func TestFindTheca(t *testing.T) {
	sl, sh, sf := lookPath, homeDir, isFile
	t.Cleanup(func() { lookPath, homeDir, isFile = sl, sh, sf })
	notFound := func(string) (string, error) { return "", errors.New("not found") }

	lookPath = func(string) (string, error) { return "/on/path/theca", nil }
	if p, err := FindTheca(); err != nil || p != "/on/path/theca" {
		t.Fatalf("%q %v", p, err)
	}

	name := "theca"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	lookPath = notFound
	homeDir = func() (string, error) { return "/home/x", nil }
	var asked []string
	isFile = func(p string) bool { asked = append(asked, p); return p == filepath.Join("/home/x", "go", "bin", name) }
	if p, err := FindTheca(); err != nil || p != filepath.Join("/home/x", "go", "bin", name) {
		t.Fatalf("%q %v", p, err)
	}
	if len(asked) != 2 || asked[0] != filepath.Join("/home/x", ".local", "bin", name) {
		t.Fatalf("looked at %q", asked)
	}

	// No home: only the system directories are looked at.
	homeDir = func() (string, error) { return "", errors.New("no home") }
	asked = nil
	isFile = func(p string) bool { asked = append(asked, p); return false }
	if _, err := FindTheca(); !errors.Is(err, ErrNoTheca) {
		t.Fatal(err)
	}
	if len(asked) != 2 || asked[0] != filepath.Join("/usr/local/bin", name) || asked[1] != filepath.Join("/opt/homebrew/bin", name) {
		t.Fatalf("looked at %q", asked)
	}
}

func TestFindThecaOnWindowsLooksForTheExe(t *testing.T) {
	sl, sh, sf, so := lookPath, homeDir, isFile, osName
	t.Cleanup(func() { lookPath, homeDir, isFile, osName = sl, sh, sf, so })
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	homeDir = func() (string, error) { return "C:\\Users\\x", nil }
	osName = "windows"
	var asked []string
	isFile = func(p string) bool { asked = append(asked, p); return false }
	FindTheca()
	if len(asked) == 0 || !strings.HasSuffix(asked[0], "theca.exe") {
		t.Fatalf("looked at %q", asked)
	}
}

func TestIsFile(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, "f")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !isFile(f) || isFile(d) || isFile(filepath.Join(d, "missing")) {
		t.Fatal("isFile")
	}
}
