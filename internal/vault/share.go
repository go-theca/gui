// SPDX-License-Identifier: BSD-3-Clause

package vault

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/go-theca/theca"
	"github.com/go-theca/theca/share"
)

// Share is a running share of the exFAT filesystem inside an unlocked
// container, as the GUI uses it: *share.Share without its Password. The
// account's password is never shown, so it is not reachable from here.
type Share interface {
	// URL is the share's address, smb://127.0.0.1:<port>/theca.
	URL() string
	// MountPath is where the file manager mounted it.
	MountPath() string
	// Done is closed when the share ends by itself (a failed commit or server).
	Done() <-chan struct{}
	// Err is why it ended, once Done is closed.
	Err() error
	// Stop unmounts, closes the server and the filesystem; it never closes the
	// container, and may be called again.
	Stop() error
}

// shareOptions is what the GUI always asks for: loopback only, a free port, a
// commit every five seconds, mounted in the file manager with no dialog.
var shareOptions = share.Options{Addr: "127.0.0.1:0", Name: "theca", Sync: 5 * time.Second, Mount: true}

// startShare is share.Start, replaceable by tests; it keeps a nil *share.Share
// from becoming a non-nil interface.
var startShare = func(ctx context.Context, c *theca.Container, o share.Options) (Share, error) {
	sh, err := share.Start(ctx, c, o)
	if err != nil {
		return nil, err
	}
	return sh, nil
}

// Share serves the exFAT filesystem inside the container over SMB on loopback
// and mounts it in the file manager. The container stays open and owned by the
// Session: stop the share, then close the session.
func (s *Session) Share(ctx context.Context) (Share, error) {
	c, ok := s.c.(*theca.Container)
	if !ok {
		return nil, errors.New("this container cannot be shared")
	}
	sh, err := startShare(ctx, c, s.shareOpts)
	if errors.Is(err, share.ErrNoFilesystem) {
		return nil, fmt.Errorf("nothing to share, %w", err)
	}
	return sh, err
}

// Replaceable by tests.
var (
	execCommand = exec.Command
	goos        = runtime.GOOS
)

// fileManagerProgram is the program that opens a folder in the file manager on
// a platform.
func fileManagerProgram(goos string) string {
	switch goos {
	case "darwin":
		return "open"
	case "windows":
		return "explorer"
	}
	return "xdg-open"
}

// Reveal opens path in the file manager. It does not wait for the file
// manager: a path that does not open is only seen by the user.
func Reveal(path string) error {
	cmd := execCommand(fileManagerProgram(goos), path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
