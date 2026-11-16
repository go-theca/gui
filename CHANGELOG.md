# Changelog

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
