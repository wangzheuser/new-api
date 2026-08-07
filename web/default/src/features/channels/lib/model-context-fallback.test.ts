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
import { describe, expect, test } from 'bun:test'

import {
  calculateContextFallbackTriggerTokens,
  parseContextFallbackValue,
  serializeContextFallbackDrafts,
  validateContextFallbackDrafts,
} from './model-context-fallback'

const VALID_RULE = {
  source_context_window_tokens: 262144,
  fallback_model: 'fallback-model',
  fallback_context_window_tokens: 1048576,
  route_mode: 'cross_channel',
  target_channel_ids: [8, 3],
  future_field: { enabled: true },
}

describe('model context fallback rules', () => {
  test('parses defaults and preserves unknown fields and target order', () => {
    const parsed = parseContextFallbackValue(
      JSON.stringify({ 'source-model': VALID_RULE })
    )

    expect(parsed.error).toBeNull()
    expect(parsed.rules).toHaveLength(1)
    expect(parsed.rules[0]?.id).toBe('context-fallback-1')
    expect(parsed.rules[0]?.thresholdPercent).toBe('90')
    expect(parsed.rules[0]?.targetMode).toBe('limited')
    expect(parsed.rules[0]?.targetChannelIds).toEqual([8, 3])

    const serialized = JSON.parse(serializeContextFallbackDrafts(parsed.rules))
    expect(serialized['source-model'].target_channel_ids).toEqual([8, 3])
    expect(serialized['source-model'].future_field).toEqual({ enabled: true })
  })

  test('uses backend integer math and omits targets for automatic routing', () => {
    expect(calculateContextFallbackTriggerTokens('262144', '90')).toBe(235929)

    const parsed = parseContextFallbackValue(
      JSON.stringify({
        'source-model': {
          ...VALID_RULE,
          threshold_percent: 100,
          target_channel_ids: [],
        },
      })
    )
    expect(parsed.rules[0]?.targetMode).toBe('auto')
    const serialized = JSON.parse(serializeContextFallbackDrafts(parsed.rules))
    expect(serialized['source-model'].target_channel_ids).toBeUndefined()
  })

  test('clears targets for same-channel routing', () => {
    const parsed = parseContextFallbackValue(
      JSON.stringify({
        'source-model': {
          ...VALID_RULE,
          route_mode: 'same_channel',
          target_channel_ids: [],
        },
      })
    )
    const [rule] = parsed.rules
    expect(rule).toBeDefined()
    if (!rule) throw new Error('expected parsed rule')
    rule.targetChannelIds = [9]
    const serialized = JSON.parse(serializeContextFallbackDrafts(parsed.rules))
    expect(serialized['source-model'].target_channel_ids).toBeUndefined()
  })

  test('reports malformed JSON and invalid visual drafts', () => {
    expect(parseContextFallbackValue('{').error?.message).toBe(
      'Context fallback rules must be valid JSON'
    )

    const parsed = parseContextFallbackValue(
      JSON.stringify({ 'source-model': VALID_RULE })
    )
    const [rule] = parsed.rules
    expect(rule).toBeDefined()
    if (!rule) throw new Error('expected parsed rule')
    const duplicate = {
      ...rule,
      id: 'duplicate',
      targetMode: 'limited' as const,
      targetChannelIds: [],
    }
    const errors = validateContextFallbackDrafts([rule, duplicate])
    expect(errors.map((error) => error.message)).toContain(
      'Duplicate source model rules are not allowed'
    )
    expect(errors.map((error) => error.message)).toContain(
      'Select at least one target channel'
    )
  })
})
