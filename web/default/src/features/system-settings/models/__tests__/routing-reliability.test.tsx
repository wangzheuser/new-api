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

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import { firstPayloadTimeoutSchema } from '../../utils/first-payload-timeout'
import { RoutingReliabilitySection } from '../routing-reliability-section'

test('first payload timeout accepts zero and whole seconds while rejecting invalid drafts', () => {
  for (const value of [0, 180, '60', 9223372036]) {
    assert.equal(firstPayloadTimeoutSchema.parse(value), Number(value))
  }
  for (const value of ['', ' ', -1, 1.5, 'bad', 9223372037]) {
    assert.equal(firstPayloadTimeoutSchema.safeParse(value).success, false)
  }
})

test('retry settings display the timeout before retry count with the active value and accessible numeric bounds', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: {},
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const defaults = {
    RelayFirstPayloadTimeout: 180,
    RetryTimes: 4,
    ChannelDisableThreshold: '120',
    AutomaticDisableChannelEnabled: false,
    AutomaticEnableChannelEnabled: false,
    AutomaticDisableKeywords: '',
    AutomaticDisableStatusCodes: '401',
    AutomaticRetryStatusCodes: '400-599',
    'channel_auto_disable_setting.status_codes': '402-599',
    'channel_auto_disable_setting.sample_size': 5,
    'channel_auto_disable_setting.minimum_sample_size': 3,
    'channel_auto_disable_setting.error_rate_percent': 80,
    'channel_auto_disable_setting.disable_minutes': 10,
    'multi_key_auto_disable_setting.temporary_status_codes': '429',
    'multi_key_auto_disable_setting.persistent_status_codes': '401',
    'multi_key_auto_disable_setting.temporary_disable_minutes': 10,
    'monitor_setting.auto_test_channel_enabled': false,
    'monitor_setting.auto_test_channel_minutes': 30,
    'monitor_setting.channel_test_mode': 'passive_recovery' as const,
  }
  const render = (seconds: number) =>
    renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <RoutingReliabilitySection
            defaultValues={{ ...defaults, RelayFirstPayloadTimeout: seconds }}
          />
        </I18nextProvider>
      </QueryClientProvider>
    )
  const html = render(180)
  assert.ok(
    html.indexOf('First payload timeout per attempt') <
      html.indexOf('Retry Times')
  )
  assert.match(html, /name="RelayFirstPayloadTimeout"/)
  assert.match(html, /max="9223372036"/)
  assert.match(html, /value="180"/)
  assert.match(render(60), /value="60"/)
  client.clear()
})
