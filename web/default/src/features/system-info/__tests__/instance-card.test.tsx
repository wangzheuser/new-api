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

import { SystemInstanceCard } from '../components/system-instances-panel'
import type { SystemInstance } from '../types'

const instance: SystemInstance = {
  node_name: 'node-1',
  status: 'stale',
  started_at: 1,
  last_seen_at: 2,
  stale_after_seconds: 30,
  info: {
    node: { should_configure_manually: true },
    resources: {
      cpu: { usage_percent: 10 },
      memory: { usage_percent: 20 },
      storage: { used_percent: 30 },
    },
  },
}

async function renderCard(value: SystemInstance, deleting = false) {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', resources: {} })
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <SystemInstanceCard
        instance={value}
        isDeletingInstance={deleting}
        deletingNodeName={value.node_name}
        onDeleteStaleInstance={() => {}}
      />
    </I18nextProvider>
  )
}

test('stale mobile instance offers deletion, node guidance and labelled resources', async () => {
  const html = await renderCard(instance)
  assert.match(html, /aria-label="Delete stale instance"/)
  assert.match(html, /aria-label="Configure NODE_NAME"/)
  for (const label of ['CPU', 'Memory', 'Storage', 'Started']) {
    assert.match(html, new RegExp(`>${label}<`))
  }
})

test('online instances cannot be deleted and pending deletion disables the action', async () => {
  assert.doesNotMatch(
    await renderCard({ ...instance, status: 'online' }),
    /aria-label="Delete stale instance"/
  )
  assert.match(await renderCard(instance, true), /disabled=""/)
})
