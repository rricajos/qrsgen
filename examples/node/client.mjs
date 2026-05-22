#!/usr/bin/env node
// Cliente Node.js (ESM, sin deps externas) para qrsgen.
//
// Uso:
//   QRSGEN_URL=http://qrsgen:3100 QRSGEN_TOKEN=... node client.mjs <cmd> ...args
//
// Commands:
//   list
//   provision <name> [webhook_url]
//   status <name>
//   qr <name>                            (guarda /tmp/<name>_qr.png)
//   send <name> <jid> <content>
//   listen                               (webhook receiver en :8000)

import { writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';

const URL_BASE = process.env.QRSGEN_URL || 'http://qrsgen:3100';
const TOKEN = process.env.QRSGEN_TOKEN;
if (!TOKEN) {
  console.error('QRSGEN_TOKEN env var required');
  process.exit(1);
}

const authHeaders = { Authorization: `Bearer ${TOKEN}` };

async function req(method, path, body = null) {
  const opts = {
    method,
    headers: {
      ...authHeaders,
      ...(body ? { 'Content-Type': 'application/json' } : {}),
    },
  };
  if (body) opts.body = JSON.stringify(body);
  const r = await fetch(URL_BASE + path, opts);
  if (!r.ok) throw new Error(`${method} ${path} → ${r.status}`);
  const ct = r.headers.get('content-type') || '';
  if (ct.startsWith('image/')) return new Uint8Array(await r.arrayBuffer());
  return r.json();
}

async function list() {
  const arr = await req('GET', '/api/instances');
  for (const i of arr) console.log(`  ${i.name.padEnd(20)} ${i.state.padEnd(15)} ${i.jid || ''}`);
}

async function provision(name, webhook) {
  const body = { name };
  if (webhook) body.events_webhook_url = webhook;
  console.log(await req('POST', '/api/instances', body));
}

async function status(name) {
  const s = await req('GET', `/api/instances/${name}`);
  for (const [k, v] of Object.entries(s)) console.log(`  ${k}: ${JSON.stringify(v)}`);
}

async function qr(name) {
  try {
    const png = await req('GET', `/api/instances/${name}/qr`);
    const path = `/tmp/${name}_qr.png`;
    await writeFile(path, png);
    console.log(`saved ${path}`);
  } catch (e) {
    if (e.message.includes('404')) console.error('no QR available');
    else throw e;
  }
}

async function send(name, jid, content) {
  // /webhook está exento de auth
  const r = await fetch(`${URL_BASE}/api/instances/${name}/webhook`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      event: 'message_created',
      message_type: 'outgoing',
      content,
      conversation: { id: 1, meta: { sender: { identifier: jid } } },
      id: Date.now(),
      private: false,
    }),
  });
  if (!r.ok) throw new Error(`send failed: ${r.status}`);
  console.log('sent');
}

function listen() {
  createServer(async (req, res) => {
    let body = '';
    for await (const chunk of req) body += chunk;
    let event = {};
    try { event = JSON.parse(body); } catch {}
    console.log(`[${event.instance || '?'}] event=${event.event || '?'} path=${req.url}`);
    if (event.event === 'qr_generated') {
      try {
        const png = await req('GET', `/api/instances/${event.instance}/qr`);
        await writeFile(`/tmp/${event.instance}_qr.png`, png);
        console.log(`  → saved /tmp/${event.instance}_qr.png`);
      } catch {}
    } else if (event.event === 'strike') {
      console.log(`  🚨 STRIKE en ${event.instance} — frena envíos!`);
    }
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end('{"ok":true}');
  }).listen(8000, '0.0.0.0', () => console.log('Listening on :8000'));
}

const [cmd, ...args] = process.argv.slice(2);
const handlers = { list, provision, status, qr, send, listen };
if (!handlers[cmd]) {
  console.error('cmds: list | provision <name> [webhook] | status <name> | qr <name> | send <name> <jid> <content> | listen');
  process.exit(1);
}
await handlers[cmd](...args);
