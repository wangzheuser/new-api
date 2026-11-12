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
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import { RetryAttemptList } from '../components/dialogs/retry-details'
import { usageLogSchema } from '../data/schema'
import { canShowRetryDetails } from '../lib/request-attempts'

test('retry details require an administrator, request ID and multiple attempts', () => {
  const log = usageLogSchema.parse({
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 5,
    content: 'final error',
    request_id: 'request',
    other: '{"admin_info":{"use_channel":["118",118]}}',
  })
  assert.equal(canShowRetryDetails(log, true), true)
  assert.equal(canShowRetryDetails(log, false), false)
  assert.equal(canShowRetryDetails({ ...log, request_id: '' }, true), false)
  assert.equal(
    canShowRetryDetails(
      { ...log, other: '{"admin_info":{"use_channel":["118"]}}' },
      true
    ),
    false
  )
  assert.equal(
    canShowRetryDetails(
      { ...log, other: '{"admin_info":{"request_policy":[{"attempt":2}]}}' },
      true
    ),
    true
  )
})

test('attempt messages remain complete escaped text with an accessible copy action', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources: {} })
  const message = `<img src=x onerror=alert(1)>\n${'long error '.repeat(300)}`
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <RetryAttemptList
        incomplete
        attempts={[
          {
            attempt: null,
            channel: 118,
            channelName: 'upstream',
            statusCode: 429,
            errorCode: 'rate_limit',
            message,
          },
        ]}
      />
    </I18nextProvider>
  )
  assert.match(html, /Historical retry details are incomplete/)
  assert.match(html, /Unknown attempt/)
  assert.match(html, /429/)
  assert.match(html, /rate_limit/)
  assert.match(html, /&lt;img/)
  assert.doesNotMatch(html, /<img/)
  assert.match(html, /long error long error long error /)
  assert.match(html, /aria-label="Copy to clipboard"/)
})
