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

import en from '@/i18n/locales/en.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

import classicZhCN from '../../../../../classic/src/i18n/locales/zh-CN.json'
import classicZhTW from '../../../../../classic/src/i18n/locales/zh-TW.json'
import {
  formatReasoningEffortLabel,
  textProtocolLabel,
} from '../../../../../shared/usage-display-labels'
import { DynamicPricingBreakdown } from '../components/dynamic-pricing-breakdown'
import { formatBillingTierLabel } from '../lib/billing-expr'

for (const [language, standard, longContext] of [
  ['zh-CN', '标准', '长上下文'],
  ['zh-TW', '標準', '長上下文'],
  ['en', 'Standard', 'Long context'],
]) {
  test(`known pricing tiers follow ${language} and custom labels stay intact`, async () => {
    const i18n = createInstance()
    await i18n.init({
      lng: language,
      fallbackLng: 'en',
      resources: { en, zh, 'zh-TW': zhTW },
    })
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <DynamicPricingBreakdown
          billingExpr='len <= 200000 ? tier("standard", p * 2 + c * 10) : len <= 500000 ? tier("long_context", p * 4 + c * 20) : tier("custom-pro", p * 6 + c * 30)'
          matchedTierLabel='standard'
        />
      </I18nextProvider>
    )
    assert.ok(html.includes(`>${standard}<`))
    assert.ok(html.includes(`>${longContext}<`))
    assert.ok(html.includes('>custom-pro<'))
    assert.ok(html.includes(`>${i18n.t('Matched')}<`))
  })
}

test('the classic theme translates known tier labels in both Chinese locales', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'zh-CN',
    resources: { 'zh-CN': classicZhCN, 'zh-TW': classicZhTW },
  })
  assert.equal(formatBillingTierLabel('standard', i18n.t), '标准')
  await i18n.changeLanguage('zh-TW')
  assert.equal(formatBillingTierLabel('long_context', i18n.t), '長上下文')
  assert.equal(formatBillingTierLabel('custom-pro', i18n.t), 'custom-pro')
})

test('known conversion and reasoning labels follow the active Chinese locale in both themes', async () => {
  for (const [locale, resources, responses, high] of [
    ['zh-CN', zh, 'OpenAI 响应', '高'],
    ['zh-TW', zhTW, 'OpenAI 回應', '高'],
    ['zh-CN', classicZhCN, 'OpenAI 响应', '高'],
    ['zh-TW', classicZhTW, 'OpenAI 回應', '高'],
  ] as const) {
    const i18n = createInstance()
    await i18n.init({ lng: locale, resources: { [locale]: resources } })
    assert.equal(textProtocolLabel('OpenAI Responses', i18n.t), responses)
    assert.equal(
      textProtocolLabel('openai_responses_compatible', i18n.t),
      responses
    )
    assert.equal(formatReasoningEffortLabel('high', i18n.t), high)
    assert.equal(
      formatReasoningEffortLabel('provider-effort', i18n.t),
      'provider-effort'
    )
  }
})
