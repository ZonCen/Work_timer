<!-- SPDX-License-Identifier: MIT -->

# Focus Tracker

![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)

A small macOS utility that records which application windows you focus on and for how long. It groups time into "work hours" vs "outside hours", merges any existing log for the same day, and writes a daily summary.

## Features
- Tracks frontmost application + window title.
- Splits totals into work vs outside hours (configurable).
- Merges with existing daily logs on startup.
- Handles lock screen / idle time as separate entries.
- Writes daily summary logs.

## Requirements
- macOS (uses `osascript` & `ioreg`)
- Go 1.18+

## Build
From the project root:
```sh
go build -o focus-tracker main.go
```

## Run
Run without sudo (avoids root-owned log files):
```sh
./focus-tracker
```

If you prefer a custom log directory (recommended), set LOG_PATH to a writable directory:
```sh
export LOG_PATH="$HOME/Library/Logs/focus-tracker"
mkdir -p "$LOG_PATH"
./focus-tracker
```

## Environment variables
- IDLE_TIME — seconds of inactivity before treating the screen as "locked" (default: 120)
- WORK_DAYS — CSV weekdays for work, default `Mon,Tue,Wed,Thu,Fri`
- WORK_START — work window start `HH:MM` (default: `08:00`)
- WORK_END — work window end `HH:MM` (default: `17:00`)
- LOG_PATH — directory for daily logs (default in code: `/var/logs`)

Note: default `/var/logs` requires elevated privileges; prefer a per-user log folder to avoid permission issues.

## Permissions
Grant the built binary Accessibility / Automation permissions in System Settings → Privacy & Security → Accessibility (or Automation) so it can query System Events and window titles. Do not use sudo as a workaround for permission prompts — it will create root-owned files.

## Logs
Daily logs are written as:
- focus_tracker_YYYY-MM-DD.log
- focus_tracker_YYYY-MM-DD_outside.log

The program attempts to merge any existing same-day log on startup.

## Troubleshooting
- "permission denied" when writing logs: change LOG_PATH to a writable directory or fix ownership (avoid running the binary with sudo).
- If window titles or app names are empty, ensure Accessibility is allowed for the binary.

## Contributing
Pull requests and issues welcome. Add tests or small improvements first; open an issue to discuss larger changes.

## License
This project is licensed under the MIT License — see the LICENSE file for details.