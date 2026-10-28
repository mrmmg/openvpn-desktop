# Changelog

All notable changes to this project are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/); a release is created from each `vX.Y.Z` git tag.

## [Unreleased]

## [1.0.0] - 2026-10-28

First stable release (after the 0.1.0 release candidates).

### Added
- Import OpenVPN profiles by drag & drop or file picker; hand-placed `.ovpn`/`.conf` files in the profiles folder are picked up too.
- Connect and disconnect one profile at a time, with credential prompts for profiles that need a username/password.
- Live status, VPN IP, public IP, interface, uptime, routes, DNS and a real-time traffic chart.
- Desktop notifications for connect, disconnect, connection loss and reconnect.
- Automatic reconnect with configurable attempts and interval.
- Logs page with filtering and export.
- Settings for theme, window size, OpenVPN binary, profiles folder, log verbosity and public IP services.
- Light and dark themes; the app starts in a compact phone-like window with a hidden side menu.
- System tray icon: green when connected, orange while connecting, grey when disconnected, with Show, Disconnect and Quit. Closing the window keeps the app running in the tray (Settings → Keep running in the tray); where the desktop has no tray, closing quits as before.
- Check for updates: a button in About and an optional start-up check (Settings → Updates) that shows the command to update.
- `install.sh`: one-line install/update script that picks the right package for Linux or macOS from the latest GitHub release.
- Linux (pkexec) and macOS (osascript) privilege elevation; the app itself never runs as root and stores no passwords.
- Packages: `.deb`, `.rpm` and `.tar.gz` for Linux (amd64), universal `.dmg`/`.zip` for macOS (not signed yet).
