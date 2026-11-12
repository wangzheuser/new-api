/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { usageLogSchema } from '../data/schema'
import { normalizeRequestAttempts } from '../lib/request-attempts'

function entry(
  attempt: number,
  options: {
    intermediate?: boolean
    publicError?: boolean
    channel?: number
    content?: string
    type?: number
    createdAt?: number
    other?: string
  } = {}
) {
  return usageLogSchema.parse({
    id: 1,
    user_id: 1,
    created_at: options.createdAt ?? attempt,
    type: options.type ?? 5,
    content: options.content ?? `error ${attempt}`,
    channel: options.channel ?? 118,
    is_intermediate: options.intermediate ?? false,
    other:
      options.other ??
      JSON.stringify({
        status_code: 429,
        error_code: 'rate_limit',
        public_error: options.publicError,
        admin_info: {
          use_channel: Array.from({ length: attempt }, () => '118'),
        },
      }),
  })
}

test('historical duplicates prefer intermediate records without merging A to A', () => {
  const result = normalizeRequestAttempts(
    [
      entry(2, { publicError: true, content: 'mapped 503' }),
      entry(2, { content: 'duplicate 2', createdAt: 20 }),
      entry(2, { intermediate: true }),
      entry(1, { intermediate: true }),
      entry(1, { content: 'duplicate 1' }),
    ],
    entry(2, { publicError: true })
  )
  assert.deepEqual(
    result.attempts.map((item) => [item.attempt, item.channel, item.message]),
    [
      [1, 118, 'error 1'],
      [2, 118, 'error 2'],
    ]
  )
  assert.equal(result.incomplete, false)
})

test('successful terminal requests display previous failures without inventing a last error', () => {
  const final = entry(2, { type: 2 })
  const result = normalizeRequestAttempts(
    [final, entry(1, { intermediate: true })],
    final
  )
  assert.equal(result.attempts.length, 1)
  assert.equal(result.incomplete, false)
})

test('missing final attempt error is reported as incomplete historical evidence', () => {
  const result = normalizeRequestAttempts(
    [entry(2, { publicError: true }), entry(1)],
    entry(2, { publicError: true })
  )
  assert.equal(result.attempts.length, 1)
  assert.equal(result.incomplete, true)
})

test('a partial consume record does not hide a missing final failed attempt', () => {
  const final = entry(2, { publicError: true })
  const result = normalizeRequestAttempts(
    [entry(2, { type: 2 }), final, entry(1)],
    final
  )
  assert.equal(result.incomplete, true)
})

test('policy attempt recovers an ordinal while unknown ordinals remain unknown', () => {
  const result = normalizeRequestAttempts(
    [
      entry(1, {
        other: JSON.stringify({
          status_code: 502,
          admin_info: { request_policy: [{ attempt: 3 }] },
        }),
      }),
      entry(1, { other: '{broken' }),
    ],
    entry(3, { publicError: true })
  )
  assert.deepEqual(
    result.attempts.map((item) => item.attempt),
    [3, null]
  )
  assert.equal(result.incomplete, true)
})

test('record time then API order resolve duplicates without using display IDs', () => {
  const result = normalizeRequestAttempts(
    [
      entry(1, { content: 'newest', createdAt: 9 }),
      entry(1, { content: 'same time later in API', createdAt: 9 }),
      entry(1, { content: 'older', createdAt: 8 }),
    ],
    entry(1, { publicError: true })
  )
  assert.equal(result.attempts[0].message, 'newest')
})

test('mixed channel ID types and full untrusted text are preserved as data', () => {
  const text = `<img src=x onerror=alert(1)>\n${'very long error '.repeat(500)}`
  const final = entry(2, { publicError: true })
  const result = normalizeRequestAttempts(
    [
      entry(2, {
        channel: 120,
        content: text,
        other:
          '{"status_code":502,"error_code":"upstream_failed","admin_info":{"use_channel":[118,"120"]}}',
      }),
      entry(1),
    ],
    final
  )
  assert.equal(result.attempts[1].channel, 120)
  assert.equal(result.attempts[1].message, text)
  assert.equal(result.attempts[1].statusCode, 502)
  assert.equal(result.attempts[1].errorCode, 'upstream_failed')
})
