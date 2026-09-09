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
import { afterEach, beforeEach, describe, it } from 'node:test'

import { buildApiParams, buildBaseParams, getDefaultTimeRange } from './utils'

const NativeDate = Date
let now: number

beforeEach(() => {
  now = new NativeDate(2026, 8, 9, 12, 34, 56).getTime()
  // Freeze only the no-argument constructor; explicit timestamps stay intact.
  globalThis.Date = new Proxy(NativeDate, {
    construct(target, args) {
      return Reflect.construct(target, args.length ? args : [now])
    },
  })
})

afterEach(() => {
  globalThis.Date = NativeDate
})

describe('default log time range', () => {
  for (const [label, current] of [
    ['midday', new NativeDate(2026, 8, 9, 12, 34, 56)],
    ['late night', new NativeDate(2026, 8, 9, 23, 45)],
    ['month end', new NativeDate(2026, 8, 30, 23, 59, 59)],
    ['year end', new NativeDate(2026, 11, 31, 23, 59, 59)],
    ['DST transition date', new NativeDate(2026, 2, 8, 12)],
  ] as const) {
    it(`covers the complete local day at ${label}`, () => {
      now = current.getTime()
      const { start, end } = getDefaultTimeRange()
      const expectedStart = new NativeDate(now)
      const expectedEnd = new NativeDate(now)
      expectedStart.setHours(0, 0, 0, 0)
      expectedEnd.setHours(23, 59, 59, 999)
      assert.equal(start.getTime(), expectedStart.getTime())
      assert.equal(end.getTime(), expectedEnd.getTime())
    })
  }

  it('recomputes the day when filters are reset after midnight', () => {
    getDefaultTimeRange()
    now = new NativeDate(2026, 8, 10, 0, 1).getTime()
    const { start, end } = getDefaultTimeRange()
    assert.equal(start.getTime(), new NativeDate(2026, 8, 10).getTime())
    assert.equal(
      end.getTime(),
      new NativeDate(2026, 8, 10, 23, 59, 59, 999).getTime()
    )
  })
})

describe('log request time parameters', () => {
  for (const isAdmin of [false, true]) {
    for (const category of ['common', 'task', 'drawing'] as const) {
      for (const explicit of [false, true]) {
        it(`${category}: preserves ${explicit ? 'explicit' : 'default'} times for ${isAdmin ? 'admin' : 'user'}`, () => {
          const start = explicit
            ? new NativeDate(2026, 7, 1, 9, 15, 20, 123).getTime()
            : new NativeDate(2026, 8, 9).getTime()
          const end = explicit
            ? new NativeDate(2026, 7, 2, 10, 25, 30, 456).getTime()
            : new NativeDate(2026, 8, 9, 23, 59, 59, 999).getTime()
          const config = {
            page: 2,
            pageSize: 20,
            isAdmin,
            searchParams: explicit ? { startTime: start, endTime: end } : {},
          }
          const params =
            category === 'common'
              ? buildApiParams(config)
              : buildBaseParams({
                  ...config,
                  useMilliseconds: category === 'drawing',
                })
          const divisor = category === 'drawing' ? 1 : 1000
          assert.equal(params.start_timestamp, Math.floor(start / divisor))
          assert.equal(params.end_timestamp, Math.floor(end / divisor))
          assert.equal(params.p, 2)
          assert.equal(params.page_size, 20)
        })
      }
    }
  }
})
