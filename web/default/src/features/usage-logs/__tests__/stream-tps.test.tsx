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
import type { ComponentProps } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import { StreamTpsCell } from '../components/timing-metrics-cell'

const i18n = createInstance()
await i18n.init({ lng: 'en', resources: {} })

const stream = {
  isStream: true,
  useTimeSec: 32,
  completionTokens: 265,
  frtMs: 25631,
}

/** Render the speed shared by desktop and mobile logs. */
function renderSpeed(props: ComponentProps<typeof StreamTpsCell>): string {
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <StreamTpsCell {...props} />
    </I18nextProvider>
  )
}

test('streaming speed excludes the first packet wait before rounding', () => {
  assert.match(renderSpeed(stream), />42 t\/s</)
})

test('non-streaming speed still uses the full duration', () => {
  assert.match(renderSpeed({ ...stream, isStream: false }), />8 t\/s</)
})

test('streaming speed is unavailable without valid timing or output', () => {
  for (const props of [
    { ...stream, frtMs: undefined },
    { ...stream, frtMs: -1000 },
    { ...stream, frtMs: 32000 },
    { ...stream, frtMs: 33000 },
    { ...stream, completionTokens: 0 },
  ]) {
    assert.match(renderSpeed(props), />—</)
  }
})
