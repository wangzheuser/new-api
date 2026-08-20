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
  modelInputModalitiesSchema,
  enableChannelInputModalityOverride,
  normalizeModelInputModalities,
  parseInputModalityModelMapping,
  parseModelInputModalities,
  parseModelInputModalitiesWithStatus,
  removeModelInputModalityDeclaration,
  resolveModelInputModalities,
  stringifyModelInputModalities,
  type ModelInputModalities,
} from './model-input-modalities'

describe('model input modality persistence helpers', () => {
  test('normalizes model keys and modalities into a stable JSON value', () => {
    const value: ModelInputModalities = {
      VISION_MODEL: ['image', 'text'],
      TEXT_MODEL: ['text'],
    }

    assert.deepEqual(normalizeModelInputModalities(value), {
      TEXT_MODEL: ['text'],
      VISION_MODEL: ['text', 'image'],
    })
    assert.equal(
      stringifyModelInputModalities(value),
      '{"TEXT_MODEL":["text"],"VISION_MODEL":["text","image"]}'
    )
  })

  test('returns an empty configuration and invalid status for malformed values', () => {
    assert.deepEqual(parseModelInputModalities('{invalid'), {})
    assert.deepEqual(parseModelInputModalities({ MODEL_A: ['image'] }), {})
    assert.deepEqual(parseModelInputModalitiesWithStatus('{invalid'), {
      value: {},
      valid: false,
    })
  })

  test('rejects duplicate, blank, trimmed, and over-limit model declarations', () => {
    assert.equal(
      modelInputModalitiesSchema.safeParse({ MODEL_A: ['text', 'text'] })
        .success,
      false
    )
    assert.equal(
      modelInputModalitiesSchema.safeParse({ ' MODEL_A': ['text'] }).success,
      false
    )
    assert.equal(
      modelInputModalitiesSchema.safeParse({ '': ['text'] }).success,
      false
    )
    assert.equal(
      modelInputModalitiesSchema.safeParse(
        Object.fromEntries(
          Array.from({ length: 257 }, (_, index) => [
            `MODEL_${index}`,
            ['text'],
          ])
        )
      ).success,
      false
    )
  })
})

describe('model input modality resolution', () => {
  test('resolves channel, global, and unconfigured declarations in order', () => {
    const globalValue: ModelInputModalities = {
      MODEL_A: ['text', 'image'],
      MODEL_B: ['text', 'image'],
    }
    const channelValue: ModelInputModalities = { MODEL_A: ['text'] }

    assert.deepEqual(
      resolveModelInputModalities('MODEL_A', channelValue, globalValue),
      { modalities: ['text'], configured: true, source: 'channel' }
    )
    assert.deepEqual(
      resolveModelInputModalities('MODEL_B', channelValue, globalValue),
      {
        modalities: ['text', 'image'],
        configured: true,
        source: 'global',
      }
    )
    assert.deepEqual(
      resolveModelInputModalities('model_b', channelValue, globalValue),
      {
        modalities: ['text', 'image'],
        configured: false,
        source: 'unconfigured',
      }
    )
  })

  test('enables an override from global or compatibility defaults and restores inheritance', () => {
    const inherited = enableChannelInputModalityOverride(
      'MODEL_A',
      {},
      { MODEL_A: ['text'] }
    )
    const compatible = enableChannelInputModalityOverride(
      'MODEL_B',
      inherited,
      {}
    )

    assert.deepEqual(inherited, { MODEL_A: ['text'] })
    assert.deepEqual(compatible, {
      MODEL_A: ['text'],
      MODEL_B: ['text', 'image'],
    })
    assert.deepEqual(
      removeModelInputModalityDeclaration('MODEL_A', compatible),
      {
        MODEL_B: ['text', 'image'],
      }
    )
  })

  test('parses only valid source and target pairs from model mapping JSON', () => {
    assert.deepEqual(
      parseInputModalityModelMapping(
        '{" SOURCE_MODEL ":" UPSTREAM_MODEL ","EMPTY":"","BAD":1}'
      ),
      { SOURCE_MODEL: 'UPSTREAM_MODEL' }
    )
  })
})
