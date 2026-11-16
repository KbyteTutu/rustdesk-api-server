import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

// Load the actual dependency-free ES modules, not copies of their implementation.
async function load(path) {
  const source = await readFile(new URL(path, import.meta.url), 'utf8')
  return import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`)
}
const auth = await load('../src/utils/auth.js')
const peer = await load('../src/utils/peer.js')
function storage(t) {
  const values = new Map()
  const old = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: key => values.delete(key),
  } })
  t.after(() => {
    if (old) Object.defineProperty(globalThis, 'localStorage', old)
    else delete globalThis.localStorage
  })
  return values
}

test('session token round trip and replacement synchronizes native webclient storage', t => {
  const values = storage(t)
  assert.equal(auth.getToken(), null)
  for (const token of ['first-session', 'replacement-session']) {
    auth.setToken(token)
    assert.equal(auth.getToken(), token)
    assert.equal(values.get('wc-option:local:access_token'), token)
  }
})

test('OIDC state replacement starts a new 60-second lifetime', t => {
  const values = storage(t)
  let now = 1800000000000
  t.mock.method(Date, 'now', () => now)
  auth.setCode('first-state')
  assert.equal(values.get('oidc_code_expiry'), String(now + 60000))
  now += 30000
  auth.setCode('second-state')
  assert.equal(values.get('oidc_code_expiry'), String(now + 60000))
  assert.equal(auth.getCode(), 'second-state')
  now += 60000
  assert.equal(auth.getCode(), null)
})

test('removing OIDC state preserves session and repeated logout is idempotent', t => {
  const values = storage(t)
  auth.setToken('session')
  auth.setCode('state')
  auth.removeCode()
  auth.removeCode()
  assert.equal(auth.getCode(), null)
  assert.equal(auth.getToken(), 'session')
  auth.removeToken()
  auth.removeToken()
  assert.equal(values.size, 0)
})

test('native client connection dispatches the RustDesk protocol in the current window', t => {
  const anchor = { clicks: 0, click() { this.clicks++ } }
  const old = Object.getOwnPropertyDescriptor(globalThis, 'document')
  Object.defineProperty(globalThis, 'document', { configurable: true, value: {
    createElement(tag) { assert.equal(tag, 'a'); return anchor },
  } })
  t.after(() => {
    if (old) Object.defineProperty(globalThis, 'document', old)
    else delete globalThis.document
  })
  peer.connectByClient('123456789')
  assert.equal(anchor.href, 'rustdesk://123456789')
  assert.equal(anchor.target, '_self')
  assert.equal(anchor.clicks, 1)
})
