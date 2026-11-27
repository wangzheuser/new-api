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

import { createInstance } from 'i18next'

import zh from '@/i18n/locales/zh.json'

import type { ChannelProtocolProbeResponse } from '../types'
import {
  TEXT_PROTOCOLS,
  REQUEST_MODES,
  applyDetectedProtocols,
  withAvailability,
  emptyPolicy,
  profileFor,
  protocolOverrides,
  setModelOverrides,
  textProtocolLabel,
} from './protocol-policy'

test('protocol names use Chinese labels and preserve unfamiliar endpoint IDs', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'zh', resources: { zh } })
  assert.deepEqual(
    TEXT_PROTOCOLS.map((e) => textProtocolLabel(e, i18n.t)),
    ['聊天', 'OpenAI 响应', 'Claude 消息', 'Gemini 内容生成']
  )
  assert.equal(textProtocolLabel('other-endpoint', i18n.t), 'other-endpoint')
})
const policy = {
  ...emptyPolicy,
  native: {
    openai: { non_stream: true, stream: true },
    anthropic: {
      non_stream: true,
      stream: false,
      mode: 'normalized' as const,
      reasoning_history: 'preserve' as const,
    },
  },
}
const model = 'alpha'
const measured: ChannelProtocolProbeResponse[] = TEXT_PROTOCOLS.flatMap(
  (endpoint_type) =>
    REQUEST_MODES.map((key) => {
      const stream = key === 'stream',
        success =
          endpoint_type === 'anthropic' ||
          (endpoint_type === 'openai-response' && !stream)
      return {
        model,
        endpoint_type,
        stream,
        success,
        classification: success
          ? ('confirmed' as const)
          : ('path_mismatch' as const),
        http_status: success ? 200 : 404,
        capability_level: 'endpoint' as const,
        probe_case: 'basic' as const,
        effective_route_mode: 'native' as const,
        recommended_mode: 'unsupported' as const,
      }
    })
)
test('replacing a channel matrix respects each wire mode and preserves handling options', () => {
  const result = applyDetectedProtocols(
    policy,
    model,
    measured,
    'channel',
    true
  )
  assert.ok(result)
  assert.deepEqual(Object.keys(result.native).sort(), [
    'anthropic',
    'openai-response',
  ])
  assert.equal(result.native['openai-response']?.stream, false)
  assert.deepEqual(result.native.anthropic, {
    ...policy.native.anthropic,
    stream: true,
  })
  assert.deepEqual(
    withAvailability(result.native, 'anthropic', { non_stream: false }, true)
      .anthropic,
    { ...result.native.anthropic, non_stream: false }
  )
  assert.equal(
    applyDetectedProtocols(policy, model, measured.slice(1), 'channel', true),
    null
  )
  assert.equal(
    applyDetectedProtocols(
      policy,
      model,
      measured.map((r, i) =>
        i === 0 ? { ...r, classification: 'rate_limited' } : r
      ),
      'model',
      true
    ),
    null
  )
})
test('sampled model records differences and restoring inheritance removes the profile', () => {
  const result = applyDetectedProtocols(policy, model, measured, 'model', true)
  assert.ok(result)
  assert.deepEqual(result.native, policy.native)
  const patch = profileFor(result, model)?.overrides
  assert.deepEqual(patch?.anthropic, { stream: true })
  assert.deepEqual(patch?.openai, { non_stream: false, stream: false })
  assert.equal(
    profileFor(setModelOverrides(result, model, {}), model),
    undefined
  )
  assert.equal(profileFor(result, 'constructor'), undefined)
})
test('an existing absolute profile keeps its explicitly available and unavailable modes when edited', () => {
  const legacy = protocolOverrides({
    native: { anthropic: policy.native.anthropic },
  })
  assert.deepEqual(legacy.anthropic, policy.native.anthropic)
  assert.deepEqual(legacy.openai, { non_stream: false, stream: false })
})
