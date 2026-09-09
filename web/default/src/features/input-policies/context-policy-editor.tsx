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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'

import type { ContextPolicy, ContextRule } from './policy'

type Props = {
  value: ContextPolicy
  onChange: (value: ContextPolicy) => void
  channel?: boolean
  inherited?: ContextPolicy
}

/** Edit exact logical-model rules without coupling to request or billing forms. */
export function ContextPolicyEditor({
  value,
  onChange,
  channel = false,
  inherited,
}: Props) {
  const { t } = useTranslation()
  const [model, setModel] = useState('')
  const fields = [
    ['window_tokens', t('Context window (tokens)')],
    ['output_reserve_tokens', t('Output reserve (tokens)')],
  ] as const
  const models: Record<string, ContextRule> = { ...value.models }
  if (channel) {
    for (const id of Object.keys(inherited?.models ?? {})) {
      models[id] ??= { mode: 'inherit' }
    }
  }
  /** Replace one rule, preserving every other model. */
  function updateRule(id: string, rule: ContextRule) {
    onChange({ ...value, models: { ...value.models, [id]: rule } })
  }
  return (
    <fieldset className='space-y-4 rounded-lg border p-4'>
      <legend className='px-1 font-medium'>{t('Context truncation')}</legend>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Trim oldest complete turns before sending. Billing uses the full input before trimming. Client usage is unchanged. Model IDs match before upstream mapping.'
        )}
      </p>
      {!channel && (
        <label className='flex items-center gap-2 text-sm'>
          <Switch
            checked={!value.force_disabled}
            onCheckedChange={(enabled) =>
              onChange({ ...value, force_disabled: !enabled })
            }
          />
          {t('Enable context truncation for all channels')}
        </label>
      )}
      {Object.entries(models).map(([id, rule]) => (
        <div key={id} className='space-y-3 rounded-md border p-3'>
          <div className='flex flex-wrap items-center gap-2'>
            <span className='min-w-0 flex-1 font-mono text-sm break-all'>
              {id}
            </span>
            <NativeSelect
              aria-label={t('Mode')}
              value={rule.mode}
              onChange={(e) =>
                updateRule(id, {
                  ...rule,
                  mode: e.target.value as ContextRule['mode'],
                })
              }
            >
              {channel && (
                <NativeSelectOption value='inherit'>
                  {t('Inherit global settings')}
                </NativeSelectOption>
              )}
              <NativeSelectOption value='off'>
                {t('Disabled')}
              </NativeSelectOption>
              <NativeSelectOption value='custom'>
                {channel ? t('Custom') : t('Enabled')}
              </NativeSelectOption>
            </NativeSelect>
            <Button
              type='button'
              variant='outline'
              disabled={channel && !Object.hasOwn(value.models, id)}
              onClick={() => {
                const models = { ...value.models }
                delete models[id]
                onChange({ ...value, models })
              }}
            >
              {t('Remove')}
            </Button>
          </div>
          {channel && rule.mode === 'inherit' && (
            <p className='text-muted-foreground text-sm' role='status'>
              {t('Inherit global settings')}:{' '}
              {!inherited?.force_disabled &&
              inherited?.models[id]?.mode === 'custom'
                ? t(
                    'Window: {{window}} tokens; output reserve: {{reserve}} tokens',
                    {
                      window: inherited.models[id].window_tokens,
                      reserve: inherited.models[id].output_reserve_tokens,
                    }
                  )
                : t('Disabled')}
            </p>
          )}
          {rule.mode === 'custom' && (
            <div className='grid gap-3 sm:grid-cols-2'>
              {fields.map(([key, label]) => (
                <label key={key} className='space-y-1 text-sm'>
                  {label}
                  <Input
                    required
                    type='number'
                    min={1}
                    max={key === 'window_tokens' ? 2147483647 : 1073741823}
                    step={1}
                    value={rule[key] ?? ''}
                    onChange={(e) =>
                      updateRule(id, {
                        ...rule,
                        [key]:
                          e.target.value === ''
                            ? undefined
                            : Number(e.target.value),
                      })
                    }
                  />
                </label>
              ))}
            </div>
          )}
        </div>
      ))}
      <div className='flex gap-2'>
        <Input
          aria-label={t('Model ID')}
          placeholder={t('Model ID')}
          value={model}
          onChange={(e) => setModel(e.target.value)}
        />
        <Button
          type='button'
          variant='outline'
          disabled={
            !model.trim() ||
            model.trim().length > 255 ||
            Object.hasOwn(models, model.trim()) ||
            Object.keys(value.models).length >= 256
          }
          onClick={() => {
            updateRule(model.trim(), {
              mode: channel ? 'inherit' : 'custom',
            })
            setModel('')
          }}
        >
          {t('Add model')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Set the real context window and output reserve. Trimming keeps the latest complete turn, uses a 90% threshold and reserves an automatic safety margin. Output limits are not changed.'
        )}
      </p>
    </fieldset>
  )
}
