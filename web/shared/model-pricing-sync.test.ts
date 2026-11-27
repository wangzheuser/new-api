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

import {
  evalExprLocally,
  generateExprFromVisualConfig,
  tryParseVisualConfig,
} from '../default/src/features/pricing/lib/tier-expr'
import {
  buildModelPricingSyncChanges,
  type PricingSyncSnapshot,
} from './model-pricing-sync'

test('sync submits complete selected model prices with their original versions and explicit zero values', () => {
  const snapshot: PricingSyncSnapshot = {
    empty_version: 'empty',
    entries: [
      {
        model_name: 'existing',
        version: 'v1',
        effective: {},
        configured: {
          ModelPrice: 0.5,
          'billing_setting.plugin_billing_expr': { plugin: 'tier("base", 1)' },
        },
      },
    ],
  }
  const expression = 'tier("base", p * 2 + cc12h * 5)'
  const changes = buildModelPricingSyncChanges(snapshot, {
    existing: { model_ratio: 0, completion_ratio: 0 },
    new: { billing_mode: 'tiered_expr', billing_expr: expression },
  })
  assert.deepEqual(changes, [
    {
      model_name: 'existing',
      expected_version: 'v1',
      pricing: {
        ModelRatio: 0,
        CompletionRatio: 0,
        'billing_setting.billing_mode': 'ratio',
        'billing_setting.plugin_billing_expr': { plugin: 'tier("base", 1)' },
      },
    },
    {
      model_name: 'new',
      expected_version: 'empty',
      pricing: {
        'billing_setting.billing_mode': 'tiered_expr',
        'billing_setting.billing_expr': expression,
      },
    },
  ])
  assert.equal(snapshot.entries[0].configured.ModelPrice, 0.5)
  assert.deepEqual(buildModelPricingSyncChanges(snapshot, {}), [])
})

test('12h cache prices survive visual editing and participate in the estimate', () => {
  const expression =
    'tier("base", p * 2 + c * 10 + cr * 0.1 + cc * 2.5 + cc12h * 5)'
  const visual = tryParseVisualConfig(expression)
  assert.ok(visual)
  assert.equal(visual.tiers[0].cache_create_12h_unit_cost, 5)
  assert.equal(generateExprFromVisualConfig(visual), expression)
  const estimate = evalExprLocally(expression, 1000, 0, {
    cacheReadTokens: 4000,
    cacheCreateTokens: 3000,
    cacheCreate1hTokens: 0,
    cacheCreate12hTokens: 4000,
    imageTokens: 0,
    imageOutputTokens: 0,
    audioInputTokens: 0,
    audioOutputTokens: 0,
  })
  assert.equal(estimate.error, null)
  assert.equal(estimate.cost, 29900)
})
