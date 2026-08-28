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

import type {
  ChannelProtocolProbeResponse,
  ModelProtocolProfile,
  TextEndpointType,
} from '../types'
import {
  applyProtocolProbeResults,
  createProtocolProbeBatch,
  isProtocolProbeBatchComplete,
  isProtocolProbeReady,
  parseModelOverridesDraft,
  promoteCommonModelProtocolCapabilities,
  protocolCapabilityState,
  protocolProbeKey,
  summarizeModelProtocolOverrides,
  TEXT_PROTOCOLS,
  updateProtocolCapabilityState,
  type ProtocolProbeBatch,
  type ProtocolProbeResultMap,
} from './protocol-policy'

type ProbeRecommendation = 'native' | 'normalized' | 'unsupported'

/**
 * Build a deterministic complete result set for one probe batch.
 */
function createCompleteProbeResults(
  batch: ProtocolProbeBatch,
  recommendationFor: (
    endpointType: TextEndpointType
  ) => ProbeRecommendation = () => 'unsupported'
): ProtocolProbeResultMap {
  const results: ProtocolProbeResultMap = {}
  for (const model of batch.models) {
    for (const endpointType of TEXT_PROTOCOLS) {
      for (const stream of [false, true]) {
        for (const probeCase of batch.probeCases) {
          const recommendation = recommendationFor(endpointType)
          const capabilityLevel =
            probeCase === 'basic' ? 'endpoint' : 'semantic'
          const result: ChannelProtocolProbeResponse = {
            success: true,
            model,
            endpoint_type: endpointType,
            stream,
            http_status: 200,
            classification: 'confirmed',
            probe_case: probeCase,
            capability_level: capabilityLevel,
            effective_route_mode:
              recommendation === 'normalized' ? 'normalized' : 'native',
            recommended_mode:
              capabilityLevel === 'endpoint' && recommendation === 'native'
                ? 'unsupported'
                : recommendation,
          }
          results[protocolProbeKey(model, endpointType, stream, probeCase)] =
            result
        }
      }
    }
  }
  return results
}

describe('model protocol overrides draft', () => {
  test('treats blank input as an empty object and validates JSON shape', () => {
    assert.deepEqual(parseModelOverridesDraft('   \n'), {
      success: true,
      value: {},
    })
    assert.equal(
      parseModelOverridesDraft(
        '{"MODEL_A":{"native":{"anthropic":{"non_stream":true,"stream":true,"mode":"normalized"}}}}'
      ).success,
      true
    )
    assert.deepEqual(parseModelOverridesDraft('{'), {
      success: false,
      error: 'invalid_json',
    })
    assert.deepEqual(parseModelOverridesDraft('[]'), {
      success: false,
      error: 'not_object',
    })
  })
})

describe('protocol probe readiness', () => {
  test('accepts saved credentials or a current unsaved key after model selection', () => {
    assert.equal(isProtocolProbeReady(42, '', 1), true)
    assert.equal(isProtocolProbeReady(undefined, ' draft-key ', 1), true)
    assert.equal(isProtocolProbeReady(undefined, '', 1), false)
    assert.equal(isProtocolProbeReady(42, '', 0), false)
  })
})

describe('three-state protocol capabilities', () => {
  test('round-trips unavailable, native, and normalized states', () => {
    const native = updateProtocolCapabilityState(
      undefined,
      'anthropic',
      'non_stream',
      'native'
    )
    assert.deepEqual(native, { non_stream: true, stream: false })
    assert.equal(
      protocolCapabilityState(native || undefined, 'non_stream'),
      'native'
    )

    const normalized = updateProtocolCapabilityState(
      native || undefined,
      'anthropic',
      'stream',
      'normalized'
    )
    assert.deepEqual(normalized, {
      non_stream: true,
      stream: true,
      mode: 'normalized',
      reasoning_history: 'strip',
    })
    assert.equal(
      protocolCapabilityState(normalized || undefined, 'non_stream'),
      'normalized'
    )
    assert.equal(
      protocolCapabilityState(normalized || undefined, 'stream'),
      'normalized'
    )

    const withoutNormal = updateProtocolCapabilityState(
      normalized || undefined,
      'anthropic',
      'non_stream',
      'unavailable'
    )
    assert.deepEqual(withoutNormal, {
      non_stream: false,
      stream: true,
      mode: 'normalized',
      reasoning_history: 'strip',
    })
  })
})

describe('protocol probe batches', () => {
  test('distinguishes endpoint-only and semantic task scopes', () => {
    const endpointBatch = createProtocolProbeBatch(['MODEL_A'], 'endpoint')
    const semanticBatch = createProtocolProbeBatch(['MODEL_A'], 'semantic')
    assert.equal(endpointBatch.expectedResultKeys.length, 8)
    assert.equal(semanticBatch.expectedResultKeys.length, 48)
    assert.equal(
      isProtocolProbeBatchComplete(
        endpointBatch,
        createCompleteProbeResults(endpointBatch)
      ),
      true
    )

    const results = createCompleteProbeResults(semanticBatch)
    delete results[semanticBatch.expectedResultKeys[0]]
    assert.equal(isProtocolProbeBatchComplete(semanticBatch, results), false)
    assert.equal(
      isProtocolProbeBatchComplete(
        { ...semanticBatch, stopped: true },
        createCompleteProbeResults(semanticBatch)
      ),
      false
    )
  })

  test('never applies basic reachability as a native capability', () => {
    const batch = createProtocolProbeBatch(['MODEL_A'], 'endpoint')
    const applied = applyProtocolProbeResults(
      {
        MODEL_A: {
          native: { openai: { non_stream: true, stream: true } },
        },
      },
      batch,
      createCompleteProbeResults(batch, () => 'native')
    )
    assert.equal(applied, null)
  })

  test('applies a complete semantic suite without upgrading normalized routes', () => {
    const batch = createProtocolProbeBatch(['MODEL_A'], 'semantic')
    const applied = applyProtocolProbeResults(
      {
        MODEL_B: {
          native: { openai: { non_stream: true, stream: false } },
        },
      },
      batch,
      createCompleteProbeResults(batch, (endpointType) => {
        if (
          endpointType === 'anthropic' ||
          endpointType === 'openai-response'
        ) {
          return 'normalized'
        }
        return 'unsupported'
      })
    )

    assert.deepEqual(applied, {
      MODEL_A: {
        native: {
          anthropic: {
            non_stream: true,
            stream: true,
            mode: 'normalized',
            reasoning_history: 'strip',
          },
          'openai-response': {
            non_stream: true,
            stream: true,
            mode: 'normalized',
          },
        },
      },
      MODEL_B: {
        native: { openai: { non_stream: true, stream: false } },
      },
    })
  })

  test('leaves failed semantic protocols unavailable in the form draft', () => {
    const batch = createProtocolProbeBatch(['MODEL_A'], 'semantic')
    const results = createCompleteProbeResults(batch, () => 'normalized')
    const failedKey = protocolProbeKey(
      'MODEL_A',
      'anthropic',
      false,
      'tool_cycle'
    )
    results[failedKey] = {
      ...results[failedKey],
      success: false,
      classification: 'upstream_error',
      recommended_mode: 'unsupported',
    }

    const applied = applyProtocolProbeResults({}, batch, results)
    assert.equal(applied?.MODEL_A.native.anthropic?.non_stream, false)
    assert.equal(applied?.MODEL_A.native.anthropic?.stream, true)
  })
})

describe('model protocol capability summary and promotion', () => {
  test('counts native and normalized coverage separately', () => {
    const summary = summarizeModelProtocolOverrides(
      ['MODEL_A', 'MODEL_B', 'MODEL_C'],
      {
        MODEL_A: {
          native: {
            anthropic: {
              non_stream: true,
              stream: true,
              mode: 'normalized',
              reasoning_history: 'strip',
            },
          },
        },
        MODEL_B: {
          native: {
            anthropic: { non_stream: true, stream: false },
          },
        },
      }
    )

    assert.equal(summary.coveredModels, 2)
    assert.deepEqual(summary.capabilities.anthropic.nonStream, {
      native: 1,
      normalized: 1,
    })
    assert.deepEqual(summary.capabilities.anthropic.stream, {
      native: 0,
      normalized: 1,
    })
  })

  test('promotes matching normalized capabilities and preserves their mode', () => {
    const sharedProfile = {
      native: {
        anthropic: {
          non_stream: true,
          stream: true,
          mode: 'normalized' as const,
          reasoning_history: 'strip' as const,
        },
      },
    }
    const promotion = promoteCommonModelProtocolCapabilities(
      ['MODEL_A', 'MODEL_B'],
      {
        MODEL_A: structuredClone(sharedProfile),
        MODEL_B: structuredClone(sharedProfile),
        STALE_MODEL: {
          native: { gemini: { non_stream: true, stream: false } },
        },
      }
    )

    assert.deepEqual(promotion, {
      native: sharedProfile.native,
      modelOverrides: {
        STALE_MODEL: {
          native: { gemini: { non_stream: true, stream: false } },
        },
      },
    })
  })

  test('does not merge different handling modes into one channel default', () => {
    const promotion = promoteCommonModelProtocolCapabilities(
      ['MODEL_A', 'MODEL_B'],
      {
        MODEL_A: {
          native: {
            anthropic: {
              non_stream: true,
              stream: true,
              mode: 'normalized',
            },
          },
        },
        MODEL_B: {
          native: {
            anthropic: { non_stream: true, stream: true },
          },
        },
      }
    )
    assert.equal(promotion, null)
  })

  test('ignores incomplete editable JSON profiles without throwing', () => {
    const incompleteOverrides = {
      MODEL_A: {},
      MODEL_B: { native: null },
    } as unknown as Record<string, ModelProtocolProfile>
    const summary = summarizeModelProtocolOverrides(
      ['MODEL_A', 'MODEL_B'],
      incompleteOverrides
    )
    assert.equal(summary.coveredModels, 0)
    assert.equal(
      promoteCommonModelProtocolCapabilities(
        ['MODEL_A', 'MODEL_B'],
        incompleteOverrides
      ),
      null
    )
  })
})
