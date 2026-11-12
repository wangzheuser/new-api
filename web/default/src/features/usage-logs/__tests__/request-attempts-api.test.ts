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
import { afterEach, beforeEach, test } from 'node:test'

import { AxiosError, type InternalAxiosRequestConfig } from 'axios'

import { api } from '@/lib/api'

import { getRequestAttemptLogs } from '../api'

const originalAdapter = api.defaults.adapter
let calls: InternalAxiosRequestConfig[] = []
let responses: unknown[] = []
const row = {
  id: 1,
  user_id: 1,
  created_at: 10,
  type: 5,
  content: 'upstream failed',
}

beforeEach(() => {
  calls = []
  responses = []
  api.defaults.adapter = async (config) => {
    calls.push(config)
    const data = responses.shift()
    if (data instanceof Error) throw data
    return { data, status: 200, statusText: 'OK', headers: {}, config }
  }
})
afterEach(() => {
  api.defaults.adapter = originalAdapter
})

test('request detail lookup fetches every page with only request-specific filters', async () => {
  responses = [
    { success: true, data: { total: 101, items: Array(100).fill(row) } },
    { success: true, data: { total: 101, items: [row] } },
  ]
  const controller = new AbortController()
  const logs = await getRequestAttemptLogs('request&one', controller.signal)
  assert.equal(logs.length, 101)
  assert.equal(calls.length, 2)
  for (const [index, config] of calls.entries()) {
    const url = new URL(config.url ?? '', 'http://localhost')
    assert.equal(url.pathname, '/api/log')
    assert.deepEqual(Object.fromEntries(url.searchParams), {
      p: String(index + 1),
      page_size: '100',
      request_id: 'request&one',
      latest_per_request: 'false',
    })
    assert.equal(config.signal, controller.signal)
  }
})

test('a failed later page rejects instead of presenting partial details', async () => {
  responses = [
    { success: true, data: { total: 101, items: Array(100).fill(row) } },
    new Error('page two failed'),
  ]
  await assert.rejects(getRequestAttemptLogs('request'), /page two failed/)
})

test('HTTP 500 detail errors remain recoverable without the global error page', async () => {
  const upstream = new AxiosError('server failed')
  upstream.response = {
    status: 500,
    statusText: 'Internal Server Error',
    data: {},
    headers: {},
    config: {} as InternalAxiosRequestConfig,
  }
  responses = [upstream]
  await assert.rejects(getRequestAttemptLogs('request'), (error: unknown) => {
    assert(error instanceof Error)
    assert(!(error instanceof AxiosError))
    assert.equal(error.cause, upstream)
    return true
  })
})

test('business failures, incomplete pagination and invalid totals reject', async () => {
  for (const response of [
    { success: false, message: 'not allowed' },
    { success: true, data: { total: 1, items: [] } },
    { success: true, data: { total: -1, items: [] } },
  ]) {
    responses = [response]
    await assert.rejects(getRequestAttemptLogs('request'))
  }
})

test('missing request identifiers never trigger an unfiltered administrator query', async () => {
  await assert.rejects(getRequestAttemptLogs(''))
  assert.equal(calls.length, 0)
})
