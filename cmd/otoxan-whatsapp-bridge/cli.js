#!/usr/bin/env node
import { readFileSync } from 'fs'
import { fileURLToPath } from 'url'
import { dirname, join } from 'path'

const __dirname = dirname(fileURLToPath(import.meta.url))
const pkg = JSON.parse(readFileSync(join(__dirname, 'package.json'), 'utf8'))

const args = process.argv.slice(2)
const command = args[0]

if (command === '--help' || command === '-h' || !command) {
  console.log(`otoxan-whatsapp-bridge v${pkg.version}

Subcommands:
  pair    Print QR code and persist session post-scan
  send    Send a message (read from stdin as JSON-line)
  serve   Start the bridge daemon (JSON-line Unix socket)
  status  Check connection status

Environment:
  OTOXAN_HOME              Otoxan home directory (default: ~/.local/share/otoxan)
  OTOXAN_WHATSAPP_SOCKET   Unix socket path (default: <home>/whatsapp-bridge.sock)
`)
  process.exit(0)
}

if (command === 'serve') {
  await import('./bridge.js')
} else if (command === 'pair') {
  // Pairing is handled by the bridge daemon; this is a convenience wrapper
  console.error('pair: start the bridge with "serve" and scan the QR code')
  process.exit(1)
} else if (command === 'send') {
  console.error('send: use the Unix socket interface or start the bridge with "serve"')
  process.exit(1)
} else if (command === 'status') {
  console.error('status: use the Unix socket interface or start the bridge with "serve"')
  process.exit(1)
} else {
  console.error(`Unknown command: ${command}`)
  process.exit(1)
}
