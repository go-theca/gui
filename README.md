# gui

A portable desktop GUI for [Theca](https://github.com/go-theca/theca) encrypted
containers, built with [go-widgets](https://github.com/go-widgets).

It does what the `theca` command does, in a window: create a container, unlock
it, look at it, verify it, copy a file in and out, and pair a FIDO2 security key
with it. It does not mount anything: Theca is a library over a file, and so is
this.

## Run it

From a clone (there is no release yet):

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
- **Create.** File, size (`16M`, `1G`, or a multiple of 4096 bytes), policy
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
| FIDO2 security key | **yes** | no | no |

- **FIDO2 is macOS-only for now**, because the library's transport is
  ([go-theca/theca](https://github.com/go-theca/theca) `fido2`, purego). Elsewhere,
  *Use security key* and *Enroll security key* fail, and the error the library
  gives is shown as it is. Everything else works.
- The Linux and Windows windows are built and cross-compiled in CI, but have not
  been run by hand yet; macOS is the platform this was developed and run on.
- **Verify has no progress bar of its own**: the library verifies in one call and
  reports nothing until it is done, so the bar fills when it finishes. Import and
  export do report progress.
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

## License

BSD-3-Clause, see [LICENSE](LICENSE).
