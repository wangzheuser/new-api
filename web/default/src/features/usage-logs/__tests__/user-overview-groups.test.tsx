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

import { UserOverviewGroupFields } from '../components/dialogs/user-info-dialog'

test('a subscription upgrade and personal benefits use separate labels in the user overview', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources: {} })

  const overview = {
    base_group: 'fallback-group',
    current_group: 'upgraded-group',
    benefit_groups: ['personal-grant'],
  }
  const visible = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <UserOverviewGroupFields overview={overview} sensitiveVisible />
    </I18nextProvider>
  )
  assert.match(
    visible,
    /Current User Group.*upgraded-group.*Subscription Upgrade/
  )
  assert.match(visible, /Benefit Groups.*personal-grant/)
  assert.doesNotMatch(visible, /fallback-group/)

  const masked = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <UserOverviewGroupFields overview={overview} sensitiveVisible={false} />
    </I18nextProvider>
  )
  assert.doesNotMatch(masked, /upgraded-group|personal-grant|fallback-group/)
})

test('the user overview displays the account group and an empty state without personal benefits', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources: {} })

  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <UserOverviewGroupFields
        overview={{
          base_group: 'default',
          current_group: 'default',
          benefit_groups: [],
        }}
        sensitiveVisible
      />
    </I18nextProvider>
  )
  assert.match(html, /Current User Group.*>default</)
  assert.match(html, /Benefit Groups.*>None</)
  assert.doesNotMatch(html, /Subscription Upgrade/)
})
