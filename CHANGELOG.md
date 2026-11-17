# Changelog

## Unreleased (v0.3.0)

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
