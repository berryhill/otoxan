import makeWASocket, { DisconnectReason, useMultiFileAuthState, fetchLatestBaileysVersion } from '@whiskeysockets/baileys'
import { Boom } from '@hapi/boom'
import fs from 'fs'
import path from 'path'
import net from 'net'
import os from 'os'

const HOME = process.env.OTOXAN_HOME || path.join(os.homedir(), '.local', 'share', 'otoxan')
const AUTH_DIR = path.join(HOME, 'whatsapp-auth')
const SOCKET_PATH = process.env.OTOXAN_WHATSAPP_SOCKET || path.join(HOME, 'whatsapp-bridge.sock')

let sock = null
let socketServer = null
let clients = new Set()

async function startBridge() {
  const { state, saveCreds } = await useMultiFileAuthState(AUTH_DIR)
  const { version } = await fetchLatestBaileysVersion()

  sock = makeWASocket({
    version,
    auth: state,
    logger: { info:()=>{}, error:()=>{}, debug:()=>{}, warn:()=>{}, trace:()=>{}, child:()=>({info:()=>{},error:()=>{},debug:()=>{},warn:()=>{},trace:()=>{}}) }
  })

  sock.ev.on('creds.update', saveCreds)

  sock.ev.on('connection.update', ({connection, lastDisconnect, qr}) => {
    if (qr) {
      console.log(JSON.stringify({ type: 'qr', qr }))
    }
    if (connection === 'close') {
      const shouldReconnect = (lastDisconnect?.error instanceof Boom)
        ? lastDisconnect.error.output.statusCode !== DisconnectReason.loggedOut
        : true
      console.error(JSON.stringify({ type: 'connection', status: 'closed', reconnect: shouldReconnect }))
      if (shouldReconnect) {
        setTimeout(startBridge, 5000)
      }
    } else if (connection === 'open') {
      console.log(JSON.stringify({ type: 'connection', status: 'open' }))
    }
  })

  // Remove old socket file if it exists
  try { fs.unlinkSync(SOCKET_PATH) } catch (e) {}

  socketServer = net.createServer((client) => {
    clients.add(client)
    let buffer = ''

    client.on('data', (data) => {
      buffer += data.toString()
      let lines = buffer.split('\n')
      buffer = lines.pop()
      for (const line of lines) {
        if (!line.trim()) continue
        handleCommand(client, line.trim())
      }
    })

    client.on('end', () => {
      clients.delete(client)
    })

    client.on('error', (err) => {
      console.error(JSON.stringify({ type: 'error', message: err.message }))
      clients.delete(client)
    })
  })

  socketServer.listen(SOCKET_PATH, () => {
    console.log(JSON.stringify({ type: 'ready', socket: SOCKET_PATH }))
  })

  socketServer.on('error', (err) => {
    console.error(JSON.stringify({ type: 'error', message: err.message }))
  })
}

async function handleCommand(client, line) {
  let req
  try {
    req = JSON.parse(line)
  } catch (e) {
    client.write(JSON.stringify({ type: 'error', error: 'invalid JSON' }) + '\n')
    return
  }

  if (req.type === 'send') {
    await handleSend(client, req)
  } else if (req.type === 'status') {
    const connected = sock && sock.ws && sock.ws.readyState === 1
    client.write(JSON.stringify({ type: 'status', connected }) + '\n')
  } else if (req.type === 'pair') {
    await handlePair(client, req)
  } else {
    client.write(JSON.stringify({ type: 'error', error: 'unknown command type: ' + req.type }) + '\n')
  }
}

async function handleSend(client, req) {
  if (!sock) {
    client.write(JSON.stringify({ type: 'error', id: req.id, error: 'not connected' }) + '\n')
    return
  }

  const jid = req.jid || (req.phone ? `${req.phone}@s.whatsapp.net` : null)
  if (!jid) {
    client.write(JSON.stringify({ type: 'error', id: req.id, error: 'missing jid or phone' }) + '\n')
    return
  }

  try {
    if (!sock.ws || sock.ws.readyState !== 1) {
      client.write(JSON.stringify({ type: 'error', id: req.id, error: 'not connected to WhatsApp' }) + '\n')
      return
    }
    let result
    if (req.document) {
      const docPath = req.document.path
      const docBuffer = fs.readFileSync(docPath)
      result = await sock.sendMessage(jid, {
        document: docBuffer,
        mimetype: req.document.mimetype || 'application/pdf',
        fileName: req.document.fileName || 'document.pdf',
        caption: req.text || ''
      })
    } else if (req.image) {
      const imgPath = req.image.path
      const imgBuffer = fs.readFileSync(imgPath)
      result = await sock.sendMessage(jid, {
        image: imgBuffer,
        caption: req.text || ''
      })
    } else {
      result = await sock.sendMessage(jid, { text: req.text || '' })
    }
    const messageId = result && result.key && result.key.id ? result.key.id : null
    client.write(JSON.stringify({ type: 'sent', id: req.id, messageId }) + '\n')
  } catch (err) {
    client.write(JSON.stringify({ type: 'error', id: req.id, error: err.message }) + '\n')
  }
}

async function handlePair(client, req) {
  if (!sock) {
    client.write(JSON.stringify({ type: 'error', error: 'not connected' }) + '\n')
    return
  }
  if (!sock.authState.creds.registered) {
    const phoneNumber = req.phone
    if (!phoneNumber) {
      client.write(JSON.stringify({ type: 'error', error: 'missing phone number for pairing' }) + '\n')
      return
    }
    try {
      const code = await sock.requestPairingCode(phoneNumber)
      client.write(JSON.stringify({ type: 'pairing_code', code }) + '\n')
    } catch (err) {
      client.write(JSON.stringify({ type: 'error', error: err.message }) + '\n')
    }
  } else {
    client.write(JSON.stringify({ type: 'status', registered: true }) + '\n')
  }
}

// Graceful shutdown
process.on('SIGINT', () => {
  if (socketServer) socketServer.close()
  if (sock) sock.end()
  try { fs.unlinkSync(SOCKET_PATH) } catch (e) {}
  process.exit(0)
})

process.on('SIGTERM', () => {
  if (socketServer) socketServer.close()
  if (sock) sock.end()
  try { fs.unlinkSync(SOCKET_PATH) } catch (e) {}
  process.exit(0)
})

startBridge().catch((err) => {
  console.error(JSON.stringify({ type: 'fatal', error: err.message }))
  process.exit(1)
})
