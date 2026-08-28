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
  buildGlobalInputModalityModelOptions,
  enableChannelInputModalityOverride,
  filterChannelInputModalityModels,
  filterModelInputModalitiesForModels,
  getAvailableInputModalityModelOptions,
  getModelInputModalityNameError,
  groupChannelInputModalityModels,
  modelInputModalitiesSchema,
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

  test('keeps only exact channel models when persisting declarations', () => {
    assert.deepEqual(
      filterModelInputModalitiesForModels(
        {
          MODEL_A: ['image', 'text'],
          'model-a': ['text'],
          MODEL_B: ['text'],
        },
        [' MODEL_A ', 'model-a', 'MODEL_A']
      ),
      {
        MODEL_A: ['text', 'image'],
        'model-a': ['text'],
      }
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

  test('builds searchable global options without configured duplicates', () => {
    const value: ModelInputModalities = {
      MODEL_B: ['text'],
      CUSTOM_MODEL: ['text', 'image'],
    }
    const options = buildGlobalInputModalityModelOptions(
      ['MODEL_C', 'MODEL_B', 'MODEL_A', 'MODEL_A'],
      value
    )

    assert.deepEqual(options, ['CUSTOM_MODEL', 'MODEL_A', 'MODEL_B', 'MODEL_C'])
    assert.deepEqual(getAvailableInputModalityModelOptions(options, value), [
      'MODEL_A',
      'MODEL_C',
    ])
    assert.deepEqual(
      getAvailableInputModalityModelOptions(options, value, 'MODEL_B'),
      ['MODEL_A', 'MODEL_B', 'MODEL_C']
    )
  })

  test('preserves exact long marketplace model IDs and ignores empty options', () => {
    const longModelId =
      'provider/very-long-model-identifier-with-version-and-preview-suffix-2026-08-20'
    const options = buildGlobalInputModalityModelOptions(
      [
        ' ',
        longModelId,
        'Model-Case-Sensitive',
        'model-case-sensitive',
        longModelId,
      ],
      {}
    )

    assert.deepEqual(options, [
      'Model-Case-Sensitive',
      'model-case-sensitive',
      longModelId,
    ])
  })

  test('validates exact custom model names before add or rename', () => {
    const value: ModelInputModalities = { MODEL_A: ['text'] }

    assert.equal(getModelInputModalityNameError('', value), 'required')
    assert.equal(getModelInputModalityNameError('MODEL_A', value), 'duplicate')
    assert.equal(
      getModelInputModalityNameError('MODEL_A', value, 'MODEL_A'),
      null
    )
    assert.equal(getModelInputModalityNameError('CUSTOM_MODEL', value), null)
    assert.equal(
      getModelInputModalityNameError('模'.repeat(86), value),
      'too_long'
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

  test('groups live channel models and keeps removed overrides visible', () => {
    const channelValue: ModelInputModalities = {
      MODEL_B: ['text'],
      REMOVED_MODEL: ['text', 'image'],
    }

    assert.deepEqual(
      groupChannelInputModalityModels(
        ['MODEL_B', 'MODEL_A', 'MODEL_B'],
        channelValue
      ),
      {
        currentModels: ['MODEL_B', 'MODEL_A'],
        removedModels: ['REMOVED_MODEL'],
      }
    )
    assert.deepEqual(
      groupChannelInputModalityModels(
        ['MODEL_B', 'REMOVED_MODEL'],
        channelValue
      ),
      {
        currentModels: ['MODEL_B', 'REMOVED_MODEL'],
        removedModels: [],
      }
    )
  })

  test('filters channel rows by requested model or mapping target', () => {
    const models = ['SOURCE_MODEL', 'TEXT_MODEL', 'VISION_MODEL']
    const mapping = { SOURCE_MODEL: 'UPSTREAM_VISION_MODEL' }

    assert.deepEqual(
      filterChannelInputModalityModels(models, mapping, 'source'),
      ['SOURCE_MODEL']
    )
    assert.deepEqual(
      filterChannelInputModalityModels(models, mapping, 'upstream_vision'),
      ['SOURCE_MODEL']
    )
    assert.deepEqual(
      filterChannelInputModalityModels(models, mapping, 'model'),
      models
    )
  })
})
