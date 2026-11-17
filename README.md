# gui

A portable desktop GUI for [Theca](https://github.com/go-theca/theca) encrypted
containers, built with [go-widgets](https://github.com/go-widgets).

It does what the `theca` command does, in a window: create a container, unlock
it, look at it, verify it, copy a file in and out, pair a FIDO2 security key
with it, and **share** the exFAT filesystem inside it, mounted in the file
manager.

## Run it

```sh
go install github.com/go-theca/gui@latest   # the binary is named gui
```

Or from a clone:

```sh
go run .
go build -o theca-gui .
```

It is pure Go (`CGO_ENABLED=0` builds everywhere): no C toolchain, no GTK, no Qt.
[go-widgets/window](https://github.com/go-widgets/window) opens a Cocoa window on
macOS, an X11 or Wayland window on Linux, a Win32 window on Windows.

## What it does

- **Home.** Name the container file, then *Unlock container* or *Create new
  container*.
- **Create.** File, size (`16M`, `1G`, or a multiple of 4096 bytes), filesystem
  (*exFAT*, the default, or a *raw volume*), policy
  (*any*: the password or the recovery code opens it; *2of3*: both are needed),
  and the password twice. The **recovery code is shown once**, on its own screen,
  and the screen cannot be left until you tick *I have written it down*. It is
  never stored and not kept after that screen closes.
- **Unlock.** Password and/or recovery code, and a *Use security key* toggle. A
  PIN field is for the key's PIN and is used only if the key asks for it.
- **Container.** Size, epoch, key slots, journal, rollback status; *Verify*
  (authenticates every sector, metadata block and tree node); *Import* a file at
  offset 0 (a file larger than the volume is refused before anything is written);
  *Export* the whole decrypted volume to a **new** file (created readable by its
  owner only, never over an existing file, and removed again if the copy fails);
  *Enroll security key*, which asks for the password and/or the recovery code
  **again**, derives the partner factors then, and wipes them when done (unlock
  keeps nothing but the open container, and derives the key once); *Close
  container*.
- **Share.** On the Container screen, *Share* serves the exFAT filesystem inside
  the container (made with `theca create --fs exfat`) over SMB on a loopback
  address and mounts it in the file manager with no password dialog
  ([go-theca/theca](https://github.com/go-theca/theca) `share`, v0.3.0). The
  screen shows where it is shared and mounted, and *Open in file manager* shows
  the volume. The button then reads *Stop sharing*. The share stops, unmounting
  first, when you press it, when you **close the container** and when you close
  the window: always before the container is committed and closed. A failed
  commit or server ends the share and says why. **The share's account password
  is never shown**: the library hands it to the OS mount service and nowhere
  else. A container with no exFAT inside is refused with a message, not a crash.
- **Keyslots.** *Keyslots...* opens a screen that lists the slots the container
  records and which of them the password and/or recovery code you type there
  open (a slot holds nothing that says what opens it, so the container cannot
  say more). *Remove that slot* removes the slot of one factor set; you also
  pick a DIFFERENT set to keep, which must still open the container. That is
  checked before anything is written: a removal that would leave no way in is
  refused ("Refused, nothing was changed"). Removing only stops access to this
  copy: anyone holding an older copy can still open it with the removed factor.
- **Every error is shown**, as the library words it, in the status area at the
  bottom. So is the notice `theca` prints at every unlock: **this container has
  no rollback counter: an older copy of it would be accepted.**

## Secrets

Passwords, recovery codes and PINs live only in masked entry fields. They never
go on the clipboard (the toolkit's masked entries refuse copy and cut), are never
logged and never appear in a message. Each is copied into a byte slice for the
library, which is cleared (`clear`) as soon as the library is done with it, and
the entry is emptied the moment an operation starts.

One honest limit: an entry's text is a Go `string`, which cannot be wiped, so the
typed secret stays in memory until the garbage collector reclaims it. The GUI
shortens that window; it cannot close it. Do not run it on a machine you do not
trust.

## Platforms and limits

| | macOS | Linux | Windows |
|---|---|---|---|
| Window | Cocoa | X11, Wayland | Win32 |
| Containers | yes | yes | yes |
| FIDO2 security key | **measured** (YubiKey 5) | built, not measured | built, not measured |

- **FIDO2** goes through [go-theca/theca](https://github.com/go-theca/theca)
  `fido2` v0.8.0: CTAP over HID on macOS and Linux, `webauthn.dll` on Windows.
  *Use security key* pairs the key with the password or the recovery code (it
  never opens a container alone), and *Enroll security key* needs two touches.
  The screen says what the key is waiting for ("Touch your security key now",
  or its PIN, asked in a masked field that is emptied on Submit and never
  logged). **On Windows, Windows shows its own dialog for the PIN or
  fingerprint**, and the GUI says so; there is no PIN field to fill. Linux needs
  a udev rule giving your user access to `/dev/hidraw*`. The GUI's key states
  are tested with a fake key; a real-key run needs a touch, and has been done
  by hand on macOS only.
- The Linux and Windows windows are built and cross-compiled in CI, but have not
  been run by hand yet; macOS is the platform this was developed and run on.
- **Verify has no progress bar of its own**: the library verifies in one call and
  reports nothing until it is done, so the bar fills when it finishes. Import and
  export do report progress.
- **Share needs an exFAT volume.** *Create* offers the filesystem: exFAT (the
  default, laid through the container exactly as `theca create --fs exfat` does,
  and a volume too small for exFAT is refused and the half-made file removed)
  or a raw volume. A raw one cannot be shared. Mounting works on
  macOS (measured); Linux (gvfs) and Windows (`MSFT_SmbMapping`) go through the
  same library but have not been run from this GUI. *Open in file manager* runs
  `open`, `xdg-open` or `explorer`.
- **Network sharing is opt-in.** *Share on the network* (beside *Share*) serves
  on every interface instead of `127.0.0.1`, as `theca serve --network` does:
  the server then REQUIRES message signing and encryption of every client, so a
  plainer or older client is refused. It is still open to anyone on the network
  who can reach this computer and has the password, and the screen says so
  before you share. It shows where the share listens and the port another
  machine connects to. **The password is a secret**: it is not on screen until
  you press *Show password* (and *Hide password* takes it off); stopping,
  locking or closing the window clears it. It is never logged. Measured on
  macOS 27.2.1: `smbutil statshares` reports AES-128-GCM encryption and
  AES_128_GMAC signing, both required. Another machine as a client is not
  measured.
- There is no file picker yet: paths are typed (or pasted).
- No rollback counter: there is no hardware counter support in the GUI, and no
  *accept rollback* option.
- Key slots cannot be removed, and a container cannot be resized, from the GUI.

## How it is built

Everything goes through MVVM ([go-widgets/mvvm](https://github.com/go-widgets/mvvm),
bound to the widgets with [go-widgets/mvvmtk](https://github.com/go-widgets/mvvmtk)):

| Package | |
|---|---|
| `internal/vault` | the library calls, mirroring `cmd/theca` |
| `internal/viewmodel` | all state and logic as Observables and Commands; imports no widget |
| `internal/view` | the toolkit widgets, and their bindings to the ViewModel |
| `main.go` | opens the window and wires the three together |

Background work (Argon2id at unlock, verify, copies, a key's touch) runs on its
own goroutine and returns through an `mvvm.Queue` that the View drains at the
start of each frame.

CI runs `gofmt`, `go vet`, the tests with `-race` and **100% statement coverage**
of the three packages, a CGO-off build for Linux, macOS and Windows, `govulncheck`,
and the go-widgets `mvvmlint` (no direct widget-state writes) and `bricolint` (no
hand-drawn UI) gates.

`hack/livecheck` is a developer tool: it opens the real window, drives the real
widgets through creating, unlocking and verifying a throwaway container, and
screenshots each step to a directory under `os.UserConfigDir()` (never inside a
repository). It does not touch a security key.

```sh
go run ./hack/livecheck -dir /some/scratch/directory
```

The share is proven against a real container and a real mount by a test that
runs only when asked (it mounts a volume on the machine, so CI skips it):

```sh
THECA_GUI_LIVE=1 go test -count=1 -run 'TestLiveShare|TestLiveNetworkShare' -v ./internal/viewmodel
```

(`TestLiveNetworkShare` also asks `smbutil` what was negotiated, so it is macOS
only. On one run in six the second mount of a test failed with macOS's
"authentication error (errno 80)", the first mount of the same test having
passed; it did not recur on a re-run and is not understood.)

## License

BSD-3-Clause, see [LICENSE](LICENSE).
