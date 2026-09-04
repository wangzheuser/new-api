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
import { describe, it } from 'node:test'

import type { MultiKeyTestResult } from '../types'
import {
  getMultiKeyCooldownMinutes,
  getMultiKeyEffectiveStatusConfig,
  getMultiKeyTestActionIndexes,
} from './multi-key-utils'

describe('getMultiKeyTestActionIndexes', () => {
  it('partitions completed results by availability', () => {
    const results: MultiKeyTestResult[] = [
      {
        key_index: 0,
        success: true,
        classification: 'available',
        tested_at: 1,
      },
      {
        key_index: 1,
        success: false,
        classification: 'auth_failed',
        tested_at: 1,
      },
      {
        key_index: 2,
        success: false,
        classification: 'quota_exhausted',
        tested_at: 1,
      },
      {
        key_index: 3,
        success: false,
        classification: 'rate_limited',
        tested_at: 1,
      },
      {
        key_index: 4,
        success: false,
        classification: 'model_forbidden',
        tested_at: 1,
      },
      {
        key_index: 5,
        success: false,
        classification: 'configuration_error',
        tested_at: 1,
      },
      {
        key_index: 6,
        success: false,
        classification: 'upstream_error',
        tested_at: 1,
      },
      {
        key_index: 7,
        success: false,
        classification: 'network_error',
        tested_at: 1,
      },
      {
        key_index: 8,
        success: false,
        classification: 'response_error',
        tested_at: 1,
      },
    ]

    assert.deepEqual(getMultiKeyTestActionIndexes(results), {
      available: [0],
      unavailable: [1, 2, 3, 4, 5, 6, 7, 8],
    })
  })

  it('does not include keys without a completed result', () => {
    const results = new Map<number, MultiKeyTestResult>()
    results.set(2, {
      key_index: 2,
      success: true,
      classification: 'available',
      tested_at: 1,
    })

    assert.deepEqual(getMultiKeyTestActionIndexes(results.values()), {
      available: [2],
      unavailable: [],
    })
  })
})

describe('multi-key effective health status', () => {
  it('shows temporary disable without changing the persisted status', () => {
    const key = {
      index: 0,
      status: 1,
      effective_status: 'temporary_disabled' as const,
      temporary_disabled: true,
      disabled_until: 1_600,
      last_status_code: 429,
      reason: 'status_code=429, limited',
    }

    assert.deepEqual(getMultiKeyEffectiveStatusConfig(key), {
      variant: 'warning',
      label: 'Temporary Disabled',
    })
    assert.equal(key.status, 1)
  })

  it('rounds the recovery countdown up and stops at zero', () => {
    assert.equal(getMultiKeyCooldownMinutes(1_600, 1_541), 1)
    assert.equal(getMultiKeyCooldownMinutes(1_600, 1_481), 2)
    assert.equal(getMultiKeyCooldownMinutes(1_600, 1_601), 0)
  })
})
