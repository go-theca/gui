// SPDX-License-Identifier: BSD-3-Clause

package vault

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/go-theca/theca/profile"
	"github.com/go-theca/theca/session"
)

// ProfileStore is the profile file of this user, the one `theca profile` writes.
func ProfileStore() (profile.Store, error) {
	p, err := profile.DefaultPath()
	return profile.Store{Path: p}, err
}

// Sessions asks the background processes of `theca open`, by profile name, with
// theca's own session package.
type Sessions struct{}

// Status says what is open under the profile; session.ErrNotOpen when nothing.
func (Sessions) Status(ctx context.Context, name string) (session.Info, error) {
	sp, err := session.SocketPath(name)
	if err != nil {
		return session.Info{}, err
	}
	return session.Status(ctx, sp)
}

// Close unmounts, commits and closes the container of the profile.
func (Sessions) Close(ctx context.Context, name string) error {
	sp, err := session.SocketPath(name)
	if err != nil {
		return err
	}
	return session.Close(ctx, sp)
}

// Spawner starts the background process of `theca open`: session.Start run on
// the theca binary (the process is `theca _serve NAME`).
type Spawner struct {
	// Exe finds the theca binary; nil means FindTheca.
	Exe func() (string, error)
}

// Start unlocks and mounts the profile. Each line the process writes while it
// unlocks (the security key's touch notice) goes to notice.
func (s Spawner) Start(ctx context.Context, name string, notice func(string)) (session.Info, error) {
	find := s.Exe
	if find == nil {
		find = FindTheca
	}
	exe, err := find()
	if err != nil {
		return session.Info{}, err
	}
	return session.Start(ctx, exe, name, notice)
}

// ErrNoTheca is returned when the theca command cannot be found.
var ErrNoTheca = errors.New("the theca command is not installed: install it (go install github.com/go-theca/theca/cmd/theca@latest) and try again")

// Seams the tests replace.
var (
	osName   = runtime.GOOS
	lookPath = exec.LookPath
	homeDir  = os.UserHomeDir
	isFile   = func(p string) bool {
		st, err := os.Stat(p)
		return err == nil && st.Mode().IsRegular()
	}
)

// FindTheca finds the theca binary: on the PATH, else where a person usually
// installs it (a program started from the desktop has a short PATH).
func FindTheca() (string, error) {
	if p, err := lookPath("theca"); err == nil {
		return p, nil
	}
	name := "theca"
	if osName == "windows" {
		name += ".exe"
	}
	var dirs []string
	if h, err := homeDir(); err == nil {
		dirs = append(dirs, filepath.Join(h, ".local", "bin"), filepath.Join(h, "go", "bin"))
	}
	dirs = append(dirs, "/usr/local/bin", "/opt/homebrew/bin")
	for _, d := range dirs {
		if p := filepath.Join(d, name); isFile(p) {
			return p, nil
		}
	}
	return "", ErrNoTheca
}
