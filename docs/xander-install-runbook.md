# Xander — Install Runbook

Xander is otoxan's primary system-administrating agent. This runbook covers
installing the systemd user unit so Xander runs as a long-lived daemon on a
development machine.

---

## Prerequisites

- `~/.otoxan/run/` directory exists
- `~/code/otoxan/otoxan/bin/xander` binary is built and executable
  (build: `cd ~/code/otoxan/otoxan && go build -tags=xander -o bin/xander ./cmd/xander/`)
- systemd user instance is enabled (`loginctl enable-linger $USER`)

Verify linger:

```bash
loginctl show-user $USER | grep Linger
# Linger=yes  ← good
# Linger=no   ← run: loginctl enable-linger $USER
```

Create the run directory if missing:

```bash
mkdir -p ~/.otoxan/run
```

---

## Install

### Step 1 — Copy the unit file

Place `xander.service` from the repo at the user systemd directory:

```bash
mkdir -p ~/.config/systemd/user
cp ~/code/otoxan/otoxan/deploy/systemd/xander.service ~/.config/systemd/user/xander.service
```

### Step 2 — Reload systemd

```bash
systemctl --user daemon-reload
```

### Step 3 — Start and enable

```bash
# Start now (one-shot)
systemctl --user start xander

# Enable at login
systemctl --user enable xander
```

### Step 4 — Verify

```bash
# Check status
systemctl --user status xander

# Should show: Active: active (running)
# with a recent journal entry containing "persona loaded"
journalctl --user -u xander --since "1 minute ago"
```

### Step 5 — Confirm graceful stop

```bash
systemctl --user stop xander
# Watch journal for clean exit line
journalctl --user -u xander --since "10 seconds ago" | grep -E "stopping|stopped|shutdown|exiting"
```

Restart to confirm it comes back up:

```bash
systemctl --user start xander
systemctl --user status xander
```

---

## Update / Reinstall

After replacing the binary or editing the unit file:

```bash
systemctl --user daemon-reload
systemctl --user restart xander
```

---

## Uninstall

```bash
systemctl --user stop xander
systemctl --user disable xander
rm ~/.config/systemd/user/xander.service
systemctl --user daemon-reload
```

The `~/.otoxan/run/` directory and socket are not removed automatically.

---

## Troubleshooting

### Service won't start

```bash
# View full journal
journalctl --user -u xander -n 50

# Common cause: missing ~/.otoxan/run/
mkdir -p ~/.otoxan/run
systemctl --user start xander
```

### Permission denied on socket

```bash
# Ensure the run directory belongs to the user
ls -la ~/.otoxan/run/
# Should be owned by $USER
```

### Type=simple vs Type=exec

This unit uses `Type=simple` (foreground, systemd manages the process). If
`xander serve` forks a daemon internally, change to `Type=exec` so systemd
tracks the actual PID.

### "Failed to determine supplementary groups"

This happens if `User=%u` is set in a user unit. User services already run as
the owner — remove the `User=` directive.

### journalctl shows nothing

```bash
# Enable user journal if needed
systemctl --user enable systemd-user-sessions
```

---

## Log Format

Xander logs to journal with `SyslogIdentifier=xander`. Filter with:

```bash
journalctl --user -u xander -f      # follow
journalctl --user -u xander -n 100  # last 100 lines
journalctl --user -u xander --since "1 hour ago"
```
