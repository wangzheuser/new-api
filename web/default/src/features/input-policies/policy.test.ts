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
import { describe, test } from 'node:test'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  transformFormDataToCreatePayload,
} from '@/features/channels/lib/channel-form'

import {
  cachePolicySchema,
  contextPolicySchema,
  mergeInputPolicy,
  validateInputPolicies,
} from './policy'

describe('input policy form contracts', () => {
  test('independent defaults and explicit zero survive save/reload', () => {
    const p = cachePolicySchema.parse({ read_trigger_percent: 0 })
    assert.equal(p.creation_trigger_percent, 20)
    assert.equal(p.read_trigger_percent, 0)
    assert.equal(p.creation_token_percent, 30)
    assert.equal(p.read_token_percent, 50)
    // JSON serialization, rather than cloning, is the persistence contract.
    // eslint-disable-next-line unicorn/prefer-structured-clone
    assert.deepEqual(cachePolicySchema.parse(JSON.parse(JSON.stringify(p))), p)
  })
  test('invalid combinations and non-integer input are rejected', () => {
    for (const p of [
      { creation_trigger_percent: 100, read_trigger_percent: 100 },
      { creation_token_percent: 60, read_token_percent: 50 },
      { read_trigger_percent: 1.5 },
      { creation_trigger_percent: null },
      { read_trigger_percent: -1 },
      { read_trigger_percent: 101 },
    ]) {
      assert.equal(cachePolicySchema.safeParse(p).success, false)
    }
  })
  test('custom context requires two numbers and ignores removed settings', () => {
    for (const rule of [
      { mode: 'custom', window_tokens: 1000 },
      { mode: 'custom', window_tokens: 1000, output_reserve_tokens: null },
      { mode: 'custom', window_tokens: 1000, output_reserve_tokens: 980 },
      {
        mode: 'custom',
        window_tokens: 2147483647,
        output_reserve_tokens: 1073741824,
      },
    ]) {
      assert.equal(
        contextPolicySchema.safeParse({ models: { m: rule } }).success,
        false
      )
    }
    const current = {
      mode: 'custom',
      window_tokens: 1000,
      output_reserve_tokens: 100,
    }
    const parsed = contextPolicySchema.parse({
      models: {
        m: {
          ...current,
          threshold_percent: 'ignored',
          keep_recent_turns: {},
          safety_tokens: false,
        },
      },
    })
    assert.deepEqual(parsed.models.m, current)
    assert.equal(
      validateInputPolicies(JSON.stringify({ context_truncation: parsed })),
      true
    )
  })
  test('channel/global scope, invalid objects and byte ceiling match the server', () => {
    assert.equal(
      validateInputPolicies('{"cache_usage_simulation":{"enabled":true}}'),
      false
    )
    assert.equal(
      validateInputPolicies(
        '{"cache_usage_simulation":{"mode":"custom"}}',
        false
      ),
      false
    )
    assert.equal(validateInputPolicies('{"context_truncation":null}'), false)
    assert.equal(validateInputPolicies('[]'), false)
    assert.equal(
      validateInputPolicies(JSON.stringify({ extra: '中'.repeat(22000) })),
      false
    )
  })
  test('merging policies preserves unknown provider fields', () => {
    const raw = mergeInputPolicy(
      '{"provider_future":{"key":0},"cache_usage_simulation":{"mode":"off"}}',
      'context_truncation',
      { models: { m: { mode: 'off' } } }
    )
    assert.deepEqual(JSON.parse(raw).provider_future, { key: 0 })
    assert.equal(JSON.parse(raw).cache_usage_simulation.mode, 'off')
    assert.equal(validateInputPolicies(raw), true)
    const payload = transformFormDataToCreatePayload({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'policy channel',
      key: 'fixture',
      models: 'm',
      settings: raw,
    })
    assert.equal(
      JSON.parse(payload.channel.settings ?? '{}').context_truncation.models.m
        .mode,
      'off'
    )
    assert.deepEqual(
      JSON.parse(payload.channel.settings ?? '{}').provider_future,
      { key: 0 }
    )
  })
})
