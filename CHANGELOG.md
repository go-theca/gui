# Changelog

## v0.4.0

- **My profiles** (go-theca/gui#12): a screen, from Home, that lists the profiles
  of `containers.toml` with their state (closed; open with the mount point and
  since when; or the error), and opens and closes them like `theca open NAME`
  and `theca close NAME`. Open starts the same background process through the
  new `session.Start` of theca v0.23.0; the secrets come from the references in
  the profile, so the screen never asks for or holds one. While the process
  waits for the security key the row says so and the key hint is shown; Cancel
  ends the wait. Needs the `theca` command (PATH, then `~/.local/bin`,
  `~/go/bin`, `/usr/local/bin`, `/opt/homebrew/bin`). Tested with fakes for
  the store, the session client and the spawner, and against a real session
  socket; the app was started once on macOS against a temporary
  `containers.toml` (it started; no container was opened and the screen was
  not clicked through by hand).
- theca v0.23.0.

## v0.3.1

- theca v0.10.2: **fixes a second mount failing on macOS** ("authentication
  error (errno 80)"). Every share now has its own random account name; macOS
  replays a stale credential for a fixed name on 127.0.0.1. Live tests: network
  share 6 of 6, local share 3 of 3 (the failure was intermittent before; the
  cause is in theca's CHANGELOG). The README's note about the flake is removed.

## v0.3.0

- **Share on the network** (a toggle beside Share): serves on every interface
  with message signing AND encryption required of every client (theca
  `share.Options{Network: true}`). A warning says that anyone on the network
  with the password can connect. The screen shows where it listens and the
  port; the password stays hidden until *Show password*, and is cleared at
  stop, lock and close. Live-tested on macOS: smbutil reports encryption and
  signing required.
- The window is 960 points tall.
- **Keyslots screen** (from the Container screen): shows the recorded slots and
  which ones the password and/or recovery code you type now open, and removes
  the slot of a factor set (password, recovery code, or both) while a DIFFERENT
  set to keep must still open the container. A removal that would leave no way
  in is refused with a clear message and changes nothing. The secrets are typed
  again, emptied and wiped after use. A removal only stops access to this copy.
- theca v0.8.0 (FIDO2 on Linux and Windows).
- The security key's state is a ViewModel state shown on screen: "touch your
  key", or "type its PIN" (masked, emptied on Submit, never in a message), then
  touch again. On Windows the screen says that Windows shows its own dialog.
  The window is 800 points tall.

## v0.2.1

- theca v0.7.0: faster unlock on Apple Silicon (Argon2id NEON; one unlock with
  the default profile took 389 ms before and 281 ms after in a single run on
  one Mac, indicative only), smb v0.16.1 per-source connection caps, bounded
  metadata and node caches.
- `THECA_GUI_LIVE=1 go test -run TestLiveUnlockTime` times one real unlock.
- No code change was needed for the bump.

## v0.2.0

- **Create can make a shareable container.** A filesystem choice, exFAT (the
  default) or a raw volume. exFAT is laid through the container by
  `share.Format` (go-theca/theca v0.4.0), the same code as `theca create --fs
  exfat`. A volume too small for exFAT is refused and the half-made file removed.
- The live test now creates the container through the ViewModel, then shares,
  writes and reads.

## v0.1.0

The first release.

- Create, unlock (password, recovery code, FIDO2 key on macOS), inspect, verify,
  import, export and enroll a security key, in a go-widgets window.
- **Share.** The Container screen has a *Share* / *Stop sharing* toggle: the
  exFAT filesystem inside the container is served over SMB on loopback and
  mounted in the file manager (go-theca/theca v0.3.0, package `share`). It shows
  where it is shared and mounted, with *Open in file manager*. It stops, before
  the container is closed, on the toggle, on *Close container* and on closing the
  window. The password of the share's account is never shown. A container with no
  exFAT inside shows the refusal.
- The window is taller (740 points) to hold the share rows.
- `THECA_GUI_LIVE=1 go test -run TestLiveShare ./internal/viewmodel` runs the
  ViewModel against a real container and a real mount.
- Closing the window now closes an open container (it used to be left to the
  process exit).
