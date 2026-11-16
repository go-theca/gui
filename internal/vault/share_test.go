// SPDX-License-Identifier: BSD-3-Clause

package vault

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	exfat "github.com/go-filesystems/exfat"
	"github.com/go-theca/theca/share"
)

// openForShare opens a fresh container, formatted with exFAT when asked, with
// the mount turned off: a test never mounts anything in the file manager.
func openForShare(t *testing.T, withFS bool) *Session {
	t.Helper()
	// 4 MiB is about the least exFAT takes.
	path := filepath.Join(t.TempDir(), "c.theca")
	fs := FSRaw
	if withFS {
		fs = FSExFAT // made by Create itself, as the GUI does
	}
	if _, err := (&Service{Hint: 1}).Create(CreateRequest{Path: path, Size: "4M", FS: fs, Password: []byte("pw")}); err != nil {
		t.Fatal(err)
	}
	sess, err := (&Service{Hint: 1}).Open(context.Background(), UnlockRequest{Path: path, Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	sess.shareOpts.Mount = false
	return sess
}

func TestShareDefaultsAreLoopbackAndMounted(t *testing.T) {
	want := share.Options{Addr: "127.0.0.1:0", Name: "theca", Sync: 5 * time.Second, Mount: true}
	if shareOptions != want {
		t.Fatalf("shareOptions %+v", shareOptions)
	}
	path, _ := newContainer(t, PolicyAny)
	sess, err := (&Service{Hint: 1}).Open(context.Background(), UnlockRequest{Path: path, Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if sess.shareOpts != want {
		t.Fatalf("a session shares with %+v", sess.shareOpts)
	}
}

func TestShareStartsAndStops(t *testing.T) {
	sess := openForShare(t, true)
	sh, err := sess.Share(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sh.URL(), "smb://127.0.0.1:") || !strings.HasSuffix(sh.URL(), "/theca") || sh.MountPath() != "" {
		t.Fatalf("URL %q, mount %q", sh.URL(), sh.MountPath())
	}
	select {
	case <-sh.Done():
		t.Fatal("ended at once")
	default:
	}
	if err := sh.Stop(); err != nil {
		t.Fatal(err)
	}
	<-sh.Done()
	if sh.Err() != nil {
		t.Fatal(sh.Err())
	}
	// The session still owns the container: it can be used and closed.
	if _, err := sess.Verify(); err != nil {
		t.Fatalf("verify after the share: %v", err)
	}
}

func TestShareNeverExposesThePassword(t *testing.T) {
	typ := reflect.TypeOf((*Share)(nil)).Elem()
	for i := 0; i < typ.NumMethod(); i++ {
		if strings.Contains(strings.ToLower(typ.Method(i).Name), "password") {
			t.Errorf("Share has %s", typ.Method(i).Name)
		}
	}
}

func TestCreateExFAT(t *testing.T) {
	// The volume holds exFAT, laid through the container.
	sess := openForShare(t, true)
	fsys, err := exfat.OpenReader(sess.c, sess.c.Size())
	if err != nil {
		t.Fatalf("no exFAT inside: %v", err)
	}
	fsys.Close()
	// A raw volume holds none.
	raw := openForShare(t, false)
	if fsys, err := exfat.OpenReader(raw.c, raw.c.Size()); err == nil {
		fsys.Close()
		t.Fatal("a raw volume holds exFAT")
	}
}

func TestCreateExFATRefusesATooSmallVolume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.theca")
	code, err := (&Service{Hint: 1}).Create(CreateRequest{Path: path, Size: "4K", FS: FSExFAT, Password: []byte("pw")})
	if err == nil || code != "" || !strings.Contains(err.Error(), "formatting exFAT") {
		t.Fatalf("code %q, err %v", code, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the half-made container was left: %v", err)
	}
}

func TestShareRefusals(t *testing.T) {
	// A container with no exFAT inside: a plain refusal, no share left behind.
	sess := openForShare(t, false)
	sh, err := sess.Share(context.Background())
	if sh != nil || err == nil || !strings.Contains(err.Error(), "nothing to share") || !strings.Contains(err.Error(), share.ErrNoFilesystem.Error()) {
		t.Fatalf("raw container: %v %v", sh, err)
	}
	// A volume that is not a real container cannot be served.
	s := &Session{c: &brokenVolume{}}
	if sh, err := s.Share(context.Background()); sh != nil || err == nil {
		t.Fatalf("not a container: %v %v", sh, err)
	}
}

func TestFileManagerProgram(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "open", "windows": "explorer", "linux": "xdg-open", "freebsd": "xdg-open"} {
		if got := fileManagerProgram(goos); got != want {
			t.Errorf("fileManagerProgram(%q) = %q, want %q", goos, got, want)
		}
	}
}

func TestReveal(t *testing.T) {
	savedExec, savedOS := execCommand, goos
	t.Cleanup(func() { execCommand, goos = savedExec, savedOS })
	goos = "darwin"
	var got []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		got = append([]string{name}, args...)
		return exec.Command(os.Args[0], "-test.run=^$")
	}
	if err := Reveal("/Volumes/theca"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "open /Volumes/theca" {
		t.Fatalf("ran %q", got)
	}
	// A file manager that cannot be started is an error.
	execCommand = func(string, ...string) *exec.Cmd {
		return exec.Command(filepath.Join(t.TempDir(), "no-such-program"))
	}
	if err := Reveal("/Volumes/theca"); err == nil {
		t.Fatal("a missing file manager is no error")
	}
}
