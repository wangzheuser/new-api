import { expect, test } from 'bun:test'

import {
  cleanBackupCode,
  formatBackupCode,
  isValidBackupCode,
} from '@/features/auth/lib/validation'

import { isVerificationRequiredError } from './secure-verification'

test('accepts alphanumeric backup codes without numeric input restrictions', () => {
  const formatted = formatBackupCode('cAwD-oQdV')

  expect(formatted).toBe('CAWD-OQDV')
  expect(isValidBackupCode(formatted)).toBe(true)
  expect(cleanBackupCode(formatted)).toBe('CAWDOQDV')
})

test('recognizes migrated proof errors', () => {
    expect(
      isVerificationRequiredError({
        response: {
          status: 403,
          data: { code: 'SECURITY_PROOF_REQUIRED' },
        },
      })
    ).toBe(true)
    expect(
      isVerificationRequiredError({
        response: {
          status: 403,
          data: { code: 'SECURITY_PROOF_CONTEXT_MISMATCH' },
        },
      })
    ).toBe(true)
})

test('does not treat unrelated forbidden responses as step-up requests', () => {
    expect(
      isVerificationRequiredError({
        response: {
          status: 403,
          data: { code: 'SECURITY_ACTION_FORBIDDEN' },
        },
      })
    ).toBe(false)
})
