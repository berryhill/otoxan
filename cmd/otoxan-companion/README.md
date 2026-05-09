# Otoxan Companion Daemon

`otoxan-companion` is the local trusted bridge between the Otoxan browser extension and the Otoxan backend. It speaks Chrome's native-messaging protocol (length-prefixed JSON over stdio) and is the replacement for the dashboard-cookie auth path when the extension runs on a different machine from the dashboard server.

## Build

From the `otoxan` repo root:

```bash
go build ./cmd/otoxan-companion
```

The binary is self-contained (statically linked). No runtime dependencies beyond a working Go toolchain.

## Commands

### `otoxan-companion` (default — native-messaging loop)

When launched by Chrome with no subcommand, the daemon enters the native-messaging loop: it reads 32-bit little-endian length-prefixed JSON from stdin and writes replies to stdout. This is the mode Chrome uses when the extension connects.

### `otoxan-companion version`

Prints the daemon version.

### `otoxan-companion check`

Verifies MongoDB connectivity using the same auth path as the rest of the otoxan stack (Infisical → config → Mongo URI). On success prints `OK` and exits 0; on failure prints the error to stderr and exits 1.

### `otoxan-companion install-native-host --extension-id <ID>`

Installs the Chrome native-messaging host manifest under `~/.config/google-chrome/NativeMessagingHosts/com.otoxan.companion.json`. The manifest tells Chrome which binary to spawn and which extension origins are allowed to talk to it.

- `--extension-id` is optional but recommended. Without it the manifest has an empty `allowed_origins` list, which means no extension can connect (useful for testing the install path).
- The command is idempotent: running it twice overwrites the manifest with the same content.

### `otoxan-companion uninstall-native-host`

Removes the manifest. Safe to run even if the manifest is already absent (no-op with a message).

## Quick Install (one-liner)

If you already have the `otoxan` CLI, use the shorthand:

```bash
otoxan companion init
```

This builds the daemon, installs the native host manifest with the dev extension ID, runs `check`, and prints `OK` when everything is wired.

The command is idempotent: run it again after pulling new code and it will rebuild + reinstall with the same extension ID.

## Manual Install Walkthrough

1. **Build the binary**
   ```bash
   cd ~/code/otoxan/otoxan
   go build -o ~/.local/bin/otoxan-companion ./cmd/otoxan-companion
   ```

2. **Install the manifest**
   ```bash
   ~/.local/bin/otoxan-companion install-native-host \
       --extension-id abcdefghijklmnopqrstuvwxyzabcdef
   ```
   Replace the extension ID with the one shown on `chrome://extensions` for your unpacked Otoxan extension.

3. **Verify connectivity**
   ```bash
   ~/.local/bin/otoxan-companion check
   ```
   Expected output: `OK`

4. **Reload the extension**
   Go to `chrome://extensions`, find Otoxan, and click the reload arrow. The extension will now auto-detect the daemon and route captures through it instead of the dashboard cookie path.

## Troubleshooting

### Chrome says "Native host not found"

- Confirm the manifest exists:
  ```bash
  ls ~/.config/google-chrome/NativeMessagingHosts/com.otoxan.companion.json
  ```
- Confirm the `path` field in the manifest points to an absolute path that exists and is executable.
- Confirm the extension ID in `allowed_origins` matches the ID on `chrome://extensions`.

### `check` fails with a Mongo error

- Verify `otoxan init` has been run and `~/.local/share/otoxan/config.yaml` exists.
- Verify the Infisical project / env / path that holds your Mongo URI is accessible from this machine.
- Run with `OTOXAN_HOME` set to a custom directory if you store config elsewhere.

### Inspect native messaging in Chrome

Chrome has a built-in inspector for native-messaging traffic:

1. Open `chrome://inspect/#native-messaging`
2. Find `com.otoxan.companion` in the list
3. Click **Inspect** — you will see every length-prefixed frame sent and received

This is the fastest way to verify the extension is talking to the daemon and to see protocol-level errors.

### Daemon logs

The daemon writes structured errors to stderr. When launched by Chrome, stderr is captured in Chrome's native-messaging log (visible in `chrome://extensions` → **service worker** console for the Otoxan background script). Look for lines starting with `otoxan-companion:`.

### `otoxan companion init` says "build failed"

- Ensure you are in the `otoxan` repo root (where `go.mod` lives) when running the command.
- Ensure Go 1.22+ is installed: `go version`.
- If the binary builds but `check` fails, see the Mongo section above.

## Protocol

The wire format is Chrome's standard native-messaging protocol:

```
[4 bytes: uint32 LE length][N bytes: JSON payload]
```

Messages are JSON objects with a `type` field. Current message types:

| Direction | Type      | Description                          |
|-----------|-----------|--------------------------------------|
| ext → daemon | `hello` | Handshake. Daemon replies `welcome`. |
| daemon → ext | `welcome` | `{version, go_version, daemon_name}` |
| daemon → ext | `error` | `{error}` — structured error reply   |

The protocol will expand in later phases to carry capture payloads, session start requests, and SSE-style streaming replies.

## Architecture

```
┌─────────────┐     native messaging      ┌─────────────────┐
│ Chrome tab  │ ─────────────────────────→ │ otoxan-companion│
│ (any page)  │  (stdio, length-prefixed) │  (local daemon) │
└─────────────┘                           └────────┬────────┘
                                                  │
                    ┌─────────────────────────────┘
                    ↓
           ┌─────────────────┐
           │   MongoDB       │ ← companion_captures (24h TTL)
           │   Infisical     │   auth, plans, tasks
           └─────────────────┘
```

When the daemon is present, the extension routes all backend traffic through it. When the daemon is absent, the extension falls back to the existing dashboard-fetch path (cookie auth). This lets users opt into the daemon gradually.
