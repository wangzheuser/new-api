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
import { useForm } from 'react-hook-form'
import { I18nextProvider } from 'react-i18next'

import { Form } from '@/components/ui/form'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  type ChannelFormValues,
} from '../../lib/channel-form'
import { AgentHeaderPresetFields } from '../agent-header-preset-fields'

const i18n = createInstance()
await i18n.init({ lng: 'en', resources: { en: { translation: {} } } })

function PresetForm(props: { client?: '' | 'codex'; disabled: boolean }) {
  const form = useForm<ChannelFormValues>({
    defaultValues: {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      agent_header_preset: props.client || '',
      agent_header_defaults: '{"User-Agent":"custom-version"}',
    },
  })
  return (
    <I18nextProvider i18n={i18n}>
      <Form {...form}>
        <AgentHeaderPresetFields disabled={props.disabled} />
      </Form>
    </I18nextProvider>
  )
}

test('an unconfigured channel offers presets without showing default headers', () => {
  const html = renderToStaticMarkup(<PresetForm disabled={false} />)
  assert.match(html, /Client header preset/)
  assert.doesNotMatch(html, /<textarea/)
})

test('an existing preset displays editable saved headers and respects the permission lock', () => {
  const editable = renderToStaticMarkup(
    <PresetForm client='codex' disabled={false} />
  )
  assert.match(editable, /Default client headers/)
  assert.match(editable, /custom-version/)
  assert.doesNotMatch(editable, /<textarea[^>]*\sdisabled(?:=|\s|>)/)
  const locked = renderToStaticMarkup(<PresetForm client='codex' disabled />)
  assert.match(locked, /<textarea[^>]*\sdisabled(?:=|\s|>)/)
})
