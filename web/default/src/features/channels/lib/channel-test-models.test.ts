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
  getChannelTestModelsWithStatus,
  getSelectedChannelTestModels,
} from './channel-test-models'

test('failure selection includes only failed models from the full channel', () => {
  const models = [
    'failed-first-page',
    'success',
    'untested',
    'failed-last-page',
  ]
  const results = {
    'failed-first-page': { status: 'error' },
    success: { status: 'success' },
    'failed-last-page': { status: 'error' },
  }

  assert.deepEqual(getChannelTestModelsWithStatus(models, results, 'error'), [
    'failed-first-page',
    'failed-last-page',
  ])
  assert.deepEqual(getChannelTestModelsWithStatus(models, results, 'success'), [
    'success',
  ])
})

test('selected models remain available across pagination and search', () => {
  const models = ['first-page', 'second-page', 'hidden-by-search']
  const selection = {
    'first-page': true,
    'second-page': true,
    'hidden-by-search': true,
    removed: true,
  }

  assert.deepEqual(getSelectedChannelTestModels(models, selection), models)
  assert.deepEqual(
    getSelectedChannelTestModels(models, {
      ...selection,
      'second-page': false,
    }),
    ['first-page', 'hidden-by-search']
  )
})
