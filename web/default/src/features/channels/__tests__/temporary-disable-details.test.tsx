/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import { ChannelTemporaryDisableDetails } from '../components/channel-temporary-disable-details'
import { channelSchema, type Channel } from '../types'

const cooldown = {
  disabled_until: 1_794_192_000,
  sample_size: 5,
  minimum_sample_size: 3,
  requests: 5,
  errors: 4,
  error_rate_percent: 80,
  status_codes: '408,500-599',
  status_code: 503,
}

async function renderCooldown(
  overrides: Partial<NonNullable<Channel['temporary_auto_disable']>>,
  language = 'en'
) {
  const i18n = createInstance()
  await i18n.init({ lng: language, resources: {} })
  const info = channelSchema.shape.temporary_auto_disable
    .unwrap()
    .parse({ ...cooldown, ...overrides })

  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <ChannelTemporaryDisableDetails info={info} />
    </I18nextProvider>
  )
}

test('rolling-window cooldown shows its actual samples and error rate', async () => {
  const html = await renderCooldown({ mechanism: 'rolling_window' })
  assert.match(html, /Recent upstream sample size:.*5/)
  assert.match(html, /Minimum health sample size:.*3/)
  assert.match(html, /Upstream responses:.*5/)
  assert.match(html, /Matching errors:.*4/)
  assert.match(html, /Error rate:.*80\.0%/)
  assert.match(html, /Status codes:.*408,500-599/)
  assert.doesNotMatch(html, /Quota or rate-limit cooldown/)
})

test('quota cooldown shows its trigger and deadline without statistical fields', async () => {
  const html = await renderCooldown({
    mechanism: 'immediate_quota',
    status_code: 429,
    requests: 0,
    errors: 0,
    error_rate_percent: 0,
  })
  assert.match(html, /Quota or rate-limit cooldown/)
  assert.match(html, /HTTP status.*429/)
  assert.match(html, /Disabled until:/)
  assert.doesNotMatch(
    html,
    /sample size:|Upstream responses:|Matching errors:|Error rate:|Status codes:/
  )
})

test('legacy cooldowns show statistics only when actual samples exist', async () => {
  const statistical = await renderCooldown({})
  assert.match(statistical, /Upstream responses:/)
  const generic = await renderCooldown({ requests: 0 })
  assert.match(generic, /Disabled until:/)
  assert.doesNotMatch(generic, /sample size:|Error rate:/)
})

test('unknown cooldown mechanisms remain accepted and use generic details', async () => {
  const info = channelSchema.shape.temporary_auto_disable
    .unwrap()
    .parse({ ...cooldown, mechanism: 'future_mechanism' })
  assert.equal(info.mechanism, 'future_mechanism')
  const html = await renderCooldown(info)
  assert.match(html, /Disabled until:/)
  assert.match(html, /HTTP status.*503/)
  assert.doesNotMatch(
    html,
    /sample size:|Error rate:|Quota or rate-limit cooldown/
  )
})

test('cooldown statistics support every interface locale and an invalid-locale fallback', async () => {
  for (const language of [
    'en',
    'zhCN',
    'zhTW',
    'fr',
    'ja',
    'ru',
    'vi',
    'invalid',
  ]) {
    const html = await renderCooldown({ mechanism: 'rolling_window' }, language)
    assert.match(html, /Error rate:/)
    assert.match(html, /Disabled until:/)
  }
})

test('changing the interface language updates cooldown number formatting', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources: {} })
  const details = (
    <I18nextProvider i18n={i18n}>
      <ChannelTemporaryDisableDetails
        info={{ ...cooldown, mechanism: 'rolling_window' }}
      />
    </I18nextProvider>
  )
  assert.match(renderToStaticMarkup(details), /80\.0%/)
  await i18n.changeLanguage('fr')
  assert.match(renderToStaticMarkup(details), /80,0%/)
})
