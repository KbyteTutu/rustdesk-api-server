import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

// Execute the actual dependency-free storage module, without a build transform.
const source = await readFile(new URL('../src/utils/auth.js', import.meta.url), 'utf8')
const auth = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`)
function storage() {
  const values = new Map()
  globalThis.localStorage = {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: key => values.delete(key),
  }
  return values
}

test('F17 logout clears both tokens, user and OIDC state, retains unrelated settings', () => {
  const values = storage()
  auth.setToken('synthetic-session')
  auth.setCode('synthetic-state')
  localStorage.setItem('user_info', '{}')
  localStorage.setItem('theme', 'dark')
  auth.removeToken()
  assert.deepEqual([...values.entries()], [['theme', 'dark']])
})

test('F17 valid OIDC state is available before expiry', () => {
  storage()
  auth.setCode('synthetic-state')
  assert.equal(auth.getCode(), 'synthetic-state')
})

test('F17 expired OIDC state is consumed', () => {
  storage()
  localStorage.setItem('oidc_code', 'expired-state')
  localStorage.setItem('oidc_code_expiry', Date.now() - 1000)
  assert.equal(auth.getCode(), null)
  assert.equal(localStorage.getItem('oidc_code'), null)
  assert.equal(localStorage.getItem('oidc_code_expiry'), null)
})

for (const [label, expiry] of [
  ['missing', null], ['malformed', 'NaN'], ['trailing-junk', `${Date.now() + 60000}garbage`],
  ['empty', ''], ['whitespace', ' '], ['negative', '-1'], ['zero', '0'],
  ['fraction', '9999999999999.5'], ['exponent', '9e15'], ['infinity', 'Infinity'],
  ['unsafe-integer', '9007199254740993'], ['padded', ' 9999999999999 '],
]) {
  test(`F17 invalid ${label} expiry must invalidate persisted OIDC state`, () => {
    storage()
    localStorage.setItem('oidc_code', 'stale-state')
    if (expiry !== null) localStorage.setItem('oidc_code_expiry', expiry)
    assert.equal(auth.getCode(), null, 'invalid persisted state must not restart login polling')
    assert.equal(localStorage.getItem('oidc_code'), null)
    assert.equal(localStorage.getItem('oidc_code_expiry'), null)
  })
}

test('F17 expiry equality is expired, one millisecond before is valid', t => {
  const now = 1800000000000
  t.mock.method(Date, 'now', () => now)
  storage()
  localStorage.setItem('oidc_code', 'synthetic-state')
  localStorage.setItem('oidc_code_expiry', now + 1)
  assert.equal(auth.getCode(), 'synthetic-state')
  localStorage.setItem('oidc_code_expiry', now)
  assert.equal(auth.getCode(), null)
  assert.equal(localStorage.getItem('oidc_code'), null)
  assert.equal(localStorage.getItem('oidc_code_expiry'), null)
})

for (const code of [null, '']) {
  test(`F17 missing or empty code (${JSON.stringify(code)}) clears orphan expiry`, () => {
    storage()
    if (code !== null) localStorage.setItem('oidc_code', code)
    localStorage.setItem('oidc_code_expiry', Date.now() + 60000)
    assert.equal(auth.getCode(), null)
    assert.equal(localStorage.getItem('oidc_code_expiry'), null)
  })
}
