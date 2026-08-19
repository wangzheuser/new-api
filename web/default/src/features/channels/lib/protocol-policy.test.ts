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
  ChannelNativeProbeResponse,
  ModelProtocolProfile,
  TextEndpointType,
} from '../types'
import {
  applyNativeProtocolProbeResults,
  createNativeProtocolProbeBatch,
  isNativeProtocolProbeBatchComplete,
  nativeProtocolProbeKey,
  parseModelOverridesDraft,
  promoteCommonModelProtocolCapabilities,
  summarizeModelProtocolOverrides,
  TEXT_PROTOCOLS,
  type NativeProtocolProbeBatch,
  type NativeProtocolProbeResultMap,
} from './protocol-policy'

type ConfirmedMode = {
  model: string
  endpointType: TextEndpointType
  stream: boolean
}

/**
 * Build a complete deterministic result set for the supplied probe batch.
 */
function createCompleteProbeResults(
  batch: NativeProtocolProbeBatch,
  confirmedModes: readonly ConfirmedMode[] = []
): NativeProtocolProbeResultMap {
  const confirmedKeys = new Set(
    confirmedModes.map((mode) =>
      nativeProtocolProbeKey(mode.model, mode.endpointType, mode.stream)
    )
  )
  const results: NativeProtocolProbeResultMap = {}

  for (const model of batch.models) {
    for (const endpointType of TEXT_PROTOCOLS) {
      for (const stream of [false, true]) {
        const key = nativeProtocolProbeKey(model, endpointType, stream)
        const classification: ChannelNativeProbeResponse['classification'] =
          confirmedKeys.has(key) ? 'confirmed' : 'path_mismatch'
        results[key] = {
          success: classification === 'confirmed',
          model,
          endpoint_type: endpointType,
          stream,
          http_status: classification === 'confirmed' ? 200 : 404,
          classification,
        }
      }
    }
  }

  return results
}

describe('model protocol overrides draft', () => {
  test('treats blank input as an empty object', () => {
    assert.deepEqual(parseModelOverridesDraft('   \n'), {
      success: true,
      value: {},
    })
  })

  test('accepts JSON objects and rejects invalid or non-object JSON', () => {
    const valid = parseModelOverridesDraft(
      '{"MODEL_A":{"native":{"openai":{"non_stream":true,"stream":false}}}}'
    )

    assert.equal(valid.success, true)
    assert.deepEqual(parseModelOverridesDraft('{'), {
      success: false,
      error: 'invalid_json',
    })
    for (const value of ['null', '[]', 'true', '1', '"value"']) {
      assert.deepEqual(parseModelOverridesDraft(value), {
        success: false,
        error: 'not_object',
      })
    }
  })
})

describe('native protocol probe batch', () => {
  test('captures the model selection and requires every expected result', () => {
    const selectedModels = ['MODEL_A']
    const batch = createNativeProtocolProbeBatch(selectedModels)
    selectedModels.push('MODEL_B')
    const results = createCompleteProbeResults(batch)

    assert.deepEqual(batch.models, ['MODEL_A'])
    assert.equal(batch.expectedResultKeys.length, 8)
    assert.equal(isNativeProtocolProbeBatchComplete(batch, results), true)

    delete results[batch.expectedResultKeys[0]]
    assert.equal(isNativeProtocolProbeBatchComplete(batch, results), false)
    assert.equal(
      isNativeProtocolProbeBatchComplete({ ...batch, stopped: true }, results),
      false
    )
  })
})

describe('applying native protocol probe results', () => {
  test('replaces probed models and preserves models outside the batch', () => {
    const batch = createNativeProtocolProbeBatch(['MODEL_A'])
    const results = createCompleteProbeResults(batch, [
      { model: 'MODEL_A', endpointType: 'gemini', stream: false },
    ])
    const applied = applyNativeProtocolProbeResults(
      {
        MODEL_A: {
          native: {
            openai: { non_stream: true, stream: true },
            anthropic: { non_stream: true, stream: true },
          },
        },
        MODEL_B: {
          native: {
            openai: { non_stream: true, stream: false },
          },
        },
      },
      batch,
      results
    )

    assert.deepEqual(applied, {
      MODEL_A: {
        native: {
          gemini: { non_stream: true, stream: false },
        },
      },
      MODEL_B: {
        native: {
          openai: { non_stream: true, stream: false },
        },
      },
    })
  })

  test('deletes an old override when a complete probe confirms no protocol', () => {
    const batch = createNativeProtocolProbeBatch(['MODEL_A'])
    const applied = applyNativeProtocolProbeResults(
      {
        MODEL_A: {
          native: { openai: { non_stream: true, stream: true } },
        },
      },
      batch,
      createCompleteProbeResults(batch)
    )

    assert.deepEqual(applied, {})
  })

  test('preserves unsaved draft entries while replacing batch models', () => {
    const batch = createNativeProtocolProbeBatch(['MODEL_A'])
    const parsedDraft = parseModelOverridesDraft(
      '{"DRAFT_MODEL":{"native":{"anthropic":{"non_stream":false,"stream":true}}}}'
    )
    assert.equal(parsedDraft.success, true)
    if (!parsedDraft.success) return

    const applied = applyNativeProtocolProbeResults(
      parsedDraft.value,
      batch,
      createCompleteProbeResults(batch, [
        { model: 'MODEL_A', endpointType: 'openai', stream: false },
        { model: 'MODEL_A', endpointType: 'openai', stream: true },
      ])
    )

    assert.deepEqual(applied, {
      DRAFT_MODEL: {
        native: { anthropic: { non_stream: false, stream: true } },
      },
      MODEL_A: {
        native: { openai: { non_stream: true, stream: true } },
      },
    })
  })

  test('does not apply incomplete or stopped batches', () => {
    const batch = createNativeProtocolProbeBatch(['MODEL_A'])
    const results = createCompleteProbeResults(batch)
    delete results[batch.expectedResultKeys[0]]

    assert.equal(applyNativeProtocolProbeResults({}, batch, results), null)
    assert.equal(
      applyNativeProtocolProbeResults(
        {},
        { ...batch, stopped: true },
        createCompleteProbeResults(batch)
      ),
      null
    )
  })

  test('preserves normal and streaming confirmations independently', () => {
    const batch = createNativeProtocolProbeBatch(['MODEL_A'])
    const applied = applyNativeProtocolProbeResults(
      {},
      batch,
      createCompleteProbeResults(batch, [
        { model: 'MODEL_A', endpointType: 'openai', stream: false },
        { model: 'MODEL_A', endpointType: 'anthropic', stream: true },
        { model: 'MODEL_A', endpointType: 'gemini', stream: false },
        { model: 'MODEL_A', endpointType: 'gemini', stream: true },
      ])
    )

    assert.deepEqual(applied, {
      MODEL_A: {
        native: {
          openai: { non_stream: true, stream: false },
          anthropic: { non_stream: false, stream: true },
          gemini: { non_stream: true, stream: true },
        },
      },
    })
  })
})

describe('model protocol capability summary and promotion', () => {
  test('summarizes support counts for covered channel models', () => {
    const summary = summarizeModelProtocolOverrides(
      ['MODEL_A', 'MODEL_B', 'MODEL_C'],
      {
        MODEL_A: {
          native: {
            openai: { non_stream: true, stream: true },
            anthropic: { non_stream: true, stream: true },
          },
        },
        MODEL_B: {
          native: {
            openai: { non_stream: true, stream: true },
            anthropic: { non_stream: true, stream: false },
          },
        },
      }
    )

    assert.equal(summary.totalModels, 3)
    assert.equal(summary.coveredModels, 2)
    assert.deepEqual(summary.capabilities.openai, {
      nonStream: 2,
      stream: 2,
    })
    assert.deepEqual(summary.capabilities.anthropic, {
      nonStream: 2,
      stream: 1,
    })
  })

  test('promotes identical capabilities and removes redundant overrides', () => {
    const sharedProfile = {
      native: {
        openai: { non_stream: true, stream: true },
        'openai-response': { non_stream: true, stream: true },
        anthropic: { non_stream: true, stream: true },
      },
    }
    const promotion = promoteCommonModelProtocolCapabilities(
      ['MODEL_A', 'MODEL_B', 'MODEL_C'],
      {
        MODEL_A: structuredClone(sharedProfile),
        MODEL_B: structuredClone(sharedProfile),
        MODEL_C: structuredClone(sharedProfile),
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

  test('keeps differing overrides after promoting their common intersection', () => {
    const promotion = promoteCommonModelProtocolCapabilities(
      ['MODEL_A', 'MODEL_B'],
      {
        MODEL_A: {
          native: {
            openai: { non_stream: true, stream: true },
            anthropic: { non_stream: true, stream: true },
          },
        },
        MODEL_B: {
          native: {
            openai: { non_stream: true, stream: true },
            gemini: { non_stream: true, stream: false },
          },
        },
      }
    )

    assert.deepEqual(promotion?.native, {
      openai: { non_stream: true, stream: true },
    })
    assert.deepEqual(Object.keys(promotion?.modelOverrides || {}).sort(), [
      'MODEL_A',
      'MODEL_B',
    ])
  })

  test('requires every channel model to have an override before promotion', () => {
    assert.equal(
      promoteCommonModelProtocolCapabilities(['MODEL_A', 'MODEL_B'], {
        MODEL_A: {
          native: { openai: { non_stream: true, stream: true } },
        },
      }),
      null
    )
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
