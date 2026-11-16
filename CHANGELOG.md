# Changelog

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
