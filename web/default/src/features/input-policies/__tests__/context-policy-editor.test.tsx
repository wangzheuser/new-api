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

import { ContextPolicyEditor } from '../context-policy-editor'
import type { ContextPolicy } from '../policy'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: {},
  interpolation: { escapeValue: false },
})
const inherited: ContextPolicy = {
  models: {
    'glm-5.2': {
      mode: 'custom',
      window_tokens: 220000,
      output_reserve_tokens: 32000,
    },
  },
}

/** Render the public editor contract without a network or browser dependency. */
function render(
  value: ContextPolicy,
  channelModels?: readonly string[],
  global = inherited
) {
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <ContextPolicyEditor
        value={value}
        channelModels={channelModels}
        inherited={global}
        onChange={() => {}}
      />
    </I18nextProvider>
  )
}

test('global and channel custom rules expose only two numeric inputs', () => {
  for (const channelModels of [undefined, ['glm-5.2']]) {
    const html = render(inherited, channelModels)
    assert.equal((html.match(/type="number"/g) ?? []).length, 2)
    assert.ok(html.includes('value="220000"'))
    assert.ok(html.includes('value="32000"'))
    assert.ok(!html.includes('Truncation threshold (%)'))
    assert.ok(!html.includes('Keep recent turns'))
    assert.ok(!html.includes('Safety margin (blank = automatic)'))
    assert.equal(
      html.includes('Enable context truncation for all channels'),
      channelModels === undefined
    )
  }
})

test('implicit inheritance renders a read-only global summary without persisting an override', () => {
  const value: ContextPolicy = { models: {} }
  const html = render(value, ['glm-5.2'])
  assert.ok(html.includes('glm-5.2'))
  assert.ok(
    html.includes('Window: 220000 tokens; output reserve: 32000 tokens')
  )
  assert.equal((html.match(/type="number"/g) ?? []).length, 0)
  assert.deepEqual(value.models, {})
})

test('global stop and channel off hide inherited budgets', () => {
  const stopped = render({ models: {} }, ['glm-5.2'], {
    ...inherited,
    force_disabled: true,
  })
  assert.ok(!stopped.includes('Window: 220000'))
  const off = render({ models: { 'glm-5.2': { mode: 'off' } } }, ['glm-5.2'])
  assert.equal((off.match(/type="number"/g) ?? []).length, 0)
})

test('channel rules inherit only global models in the current channel', () => {
  const html = render({ models: {} }, ['glm-5.2'], {
    models: {
      ...inherited.models,
      'grok-4.6': {
        mode: 'custom',
        window_tokens: 256000,
        output_reserve_tokens: 32000,
      },
    },
  })

  assert.match(html, /glm-5\.2/)
  assert.doesNotMatch(html, /grok-4\.6/)
})

test('channel model picker offers each selected model without a rule once', () => {
  const html = render({ models: { 'gpt-5': { mode: 'off' } } }, [
    'glm-5.2',
    'gpt-5',
    'gpt-5-mini',
    'gpt-5-mini',
  ])

  assert.equal((html.match(/<option[^>]* value="gpt-5-mini"/g) ?? []).length, 1)
  assert.doesNotMatch(html, /<option[^>]* value="(?:glm-5\.2|gpt-5)"/)
})

test('a channel with no selected models shows where to configure them', () => {
  const html = render({ models: {} }, [])

  assert.match(html, /Select models in Models &amp; Groups first\./)
  assert.doesNotMatch(html, /glm-5\.2|Add model/)
})

test('removing a model collapses its saved rule and selecting it again restores its budgets', () => {
  const hidden = render(inherited, ['gpt-5'])
  const visible = render(inherited, ['glm-5.2', 'gpt-5'])

  assert.match(hidden, /Rules for removed models \(1\)/)
  assert.match(hidden, /aria-expanded="false"/)
  assert.doesNotMatch(hidden, /glm-5\.2|220000/)
  assert.doesNotMatch(visible, /Rules for removed models/)
  assert.match(visible, /value="220000"/)
  assert.match(visible, /value="32000"/)
})
