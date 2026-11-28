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
import { ChevronDown } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { ContextPolicy, ContextRule } from './policy'

type Props = {
  value: ContextPolicy
  onChange: (value: ContextPolicy) => void
  channelModels?: readonly string[]
  inherited?: ContextPolicy
}

/** Edit exact logical-model rules without coupling to request or billing forms. */
export function ContextPolicyEditor({
  value,
  onChange,
  channelModels,
  inherited,
}: Props) {
  const { t, i18n } = useTranslation()
  const [model, setModel] = useState('')
  const fields = [
    ['window_tokens', t('Context window (tokens)')],
    ['output_reserve_tokens', t('Output reserve (tokens)')],
  ] as const
  const channel = channelModels !== undefined
  const channelModelSet = channelModels && new Set(channelModels)
  const rules = new Map(Object.entries(value.models))
  for (const id of Object.keys(inherited?.models ?? {})) {
    if (channelModelSet?.has(id) && !rules.has(id)) {
      rules.set(id, { mode: 'inherit' })
    }
  }
  const visibleRules = [...rules].filter(
    ([id]) => !channelModelSet || channelModelSet.has(id)
  )
  const removedModels = channelModelSet
    ? Object.keys(value.models).filter((id) => !channelModelSet.has(id))
    : []
  const candidates = channelModelSet
    ? [...channelModelSet].filter((id) => id.length <= 255 && !rules.has(id))
    : []
  let modelId = model.trim()
  if (channel && !candidates.includes(modelId)) modelId = ''

  function updateRule(id: string, rule: ContextRule) {
    onChange({ ...value, models: { ...value.models, [id]: rule } })
  }

  function removeRule(id: string) {
    const models = { ...value.models }
    delete models[id]
    onChange({ ...value, models })
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
      {visibleRules.map(([id, rule]) => (
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
              onClick={() => removeRule(id)}
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
      {channelModels?.length === 0 && (
        <p className='text-muted-foreground text-sm'>
          {t('Select models in Models & Groups first.')}
        </p>
      )}
      {(!channel || candidates.length > 0) && (
        <div className='flex gap-2'>
          {channel ? (
            <NativeSelect
              aria-label={t('Model ID')}
              className='min-w-0 flex-1'
              value={modelId}
              onChange={(e) => setModel(e.target.value)}
            >
              <NativeSelectOption value=''>
                {t('Select a model')}
              </NativeSelectOption>
              {candidates.map((id) => (
                <NativeSelectOption key={id} value={id}>
                  {id}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          ) : (
            <Input
              aria-label={t('Model ID')}
              placeholder={t('Model ID')}
              value={model}
              onChange={(e) => setModel(e.target.value)}
            />
          )}
          <Button
            type='button'
            variant='outline'
            disabled={
              !modelId ||
              modelId.length > 255 ||
              rules.has(modelId) ||
              Object.keys(value.models).length >= 256
            }
            onClick={() => {
              updateRule(modelId, { mode: channel ? 'inherit' : 'custom' })
              setModel('')
            }}
          >
            {t('Add model')}
          </Button>
        </div>
      )}
      {removedModels.length > 0 && (
        <Collapsible>
          <CollapsibleTrigger
            render={
              <Button
                variant='ghost'
                type='button'
                className='h-auto min-h-8 max-w-full justify-start text-left whitespace-normal'
              />
            }
          >
            <ChevronDown
              className='transition-transform group-aria-expanded/button:rotate-180'
              aria-hidden='true'
            />
            {t('Rules for removed models ({{total}})', {
              total: formatNumber(
                removedModels.length,
                toIntlLocale(i18n.resolvedLanguage || i18n.language)
              ),
            })}
          </CollapsibleTrigger>
          <CollapsibleContent>
            <ul className='mt-2 space-y-2'>
              {removedModels.map((id) => (
                <li key={id} className='flex items-center gap-2'>
                  <span className='min-w-0 flex-1 font-mono text-sm break-all'>
                    {id}
                  </span>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    aria-label={`${t('Remove')} ${id}`}
                    onClick={() => removeRule(id)}
                  >
                    {t('Remove')}
                  </Button>
                </li>
              ))}
            </ul>
          </CollapsibleContent>
        </Collapsible>
      )}
      <p className='text-muted-foreground text-xs'>
        {t(
          'Set the real context window and output reserve. Trimming keeps the latest complete turn, uses a 90% threshold and reserves an automatic safety margin. Output limits are not changed.'
        )}
      </p>
    </fieldset>
  )
}
