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

import { GlobalModelSystemPrompts } from './global-model-system-prompts'
import {
  systemPromptPolicySchema,
  updateGlobalModelSystemPrompts,
} from './policy'

test('editing a global prompt replaces its model key without changing other entries', () => {
  const original = { old: 'Original prompt', other: 'Keep this prompt' }
  const renamed = updateGlobalModelSystemPrompts(
    original,
    'old',
    ' renamed ',
    'Updated prompt'
  )

  assert.deepEqual(renamed, {
    other: 'Keep this prompt',
    renamed: 'Updated prompt',
  })
  // The option is persisted as JSON, so exercise that exact round trip.
  // eslint-disable-next-line unicorn/prefer-structured-clone
  const reloaded = JSON.parse(JSON.stringify({ models: renamed }))
  assert.deepEqual(systemPromptPolicySchema.parse(reloaded).models, renamed)
  assert.deepEqual(original, {
    old: 'Original prompt',
    other: 'Keep this prompt',
  })
  assert.deepEqual(
    updateGlobalModelSystemPrompts(original, 'old', 'old', 'New text'),
    { old: 'New text', other: 'Keep this prompt' }
  )
  assert.equal(
    Object.hasOwn(
      updateGlobalModelSystemPrompts(original, null, '__proto__', 'Prompt') ??
        {},
      '__proto__'
    ),
    true
  )
})

test('editing and adding cannot overwrite an existing model or save invalid prompts', () => {
  const original = { first: 'One', second: 'Two' }

  assert.equal(
    updateGlobalModelSystemPrompts(original, 'first', 'second', 'Three'),
    null
  )
  assert.equal(
    updateGlobalModelSystemPrompts(original, null, 'first', 'Three'),
    null
  )
  assert.equal(
    updateGlobalModelSystemPrompts(original, 'first', ' ', 'Three'),
    null
  )
  assert.equal(
    updateGlobalModelSystemPrompts(original, 'first', 'first', ' '),
    null
  )
  assert.equal(
    updateGlobalModelSystemPrompts(original, 'first', 'x'.repeat(256), 'Three'),
    null
  )
  assert.equal(
    updateGlobalModelSystemPrompts(
      original,
      'first',
      'first',
      'x'.repeat(65536)
    ),
    null
  )
  assert.deepEqual(original, { first: 'One', second: 'Two' })
})

test('configured prompts expose an edit action without changing their displayed text', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources: {} })

  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <GlobalModelSystemPrompts
        value={{ 'test-model': 'Existing prompt' }}
        onChange={() => {}}
      />
    </I18nextProvider>
  )

  assert.match(html, /test-model/)
  assert.match(html, /Existing prompt/)
  assert.match(html, /Edit/)
  assert.match(html, /Remove/)
})
