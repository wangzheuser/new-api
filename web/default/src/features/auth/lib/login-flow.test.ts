import { expect, test } from 'bun:test'

import { isAuthBundle, isLoginChallenge, safeLoginRedirect } from './login-flow'

test('recognizes a login challenge without treating it as an auth bundle', () => {
  const challenge = {
    require_verification: true,
    flow_token: 'flow-token',
    expires_at: 1,
    methods: [{ method: '2fa', available: true }],
  }

  expect(isLoginChallenge(challenge)).toBe(true)
  expect(isAuthBundle(challenge)).toBe(false)
  expect(isLoginChallenge({ ...challenge, flow_token: '' })).toBe(false)
})

test('accepts a completed bundle and only internal redirect paths', () => {
  const bundle = {
    access_token: 'access-token',
    token_type: 'Bearer',
    access_expires_at: 1,
    user: { id: 1, username: 'user', role: 1 },
    session: {
      sid: 'session',
      current: true,
      login_method: 'password',
      ip: '',
      user_agent: '',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2,
    },
  }

  expect(isAuthBundle(bundle)).toBe(true)
  expect(safeLoginRedirect('/dashboard')).toBe('/dashboard')
  expect(safeLoginRedirect('/dashboard?redirect=\\\\evil.example')).toBeUndefined()
  expect(safeLoginRedirect('//attacker.example')).toBeUndefined()
  expect(safeLoginRedirect('https://attacker.example')).toBeUndefined()
})
