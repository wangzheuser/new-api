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
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'

import { DEFAULT_CACHE, type CachePolicy } from './policy'

type Props = {
  value: CachePolicy
  onChange: (value: CachePolicy) => void
  channel?: boolean
}

/** Edit cache accounting independently from the channel or system form. */
export function CachePolicyEditor({ value, onChange, channel = false }: Props) {
  const { t } = useTranslation()
  const fields = [
    ['creation_trigger_percent', t('Cache creation trigger (%)')],
    ['read_trigger_percent', t('Cache read trigger (%)')],
    ['creation_token_percent', t('Created cache share (%)')],
    ['read_token_percent', t('Read cache share (%)')],
  ] as const
  return (
    <fieldset className='space-y-4 rounded-lg border p-4'>
      <legend className='px-1 font-medium'>
        {t('Cache usage simulation')}
      </legend>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Accounting only. Real upstream cache fields, including zero, always take priority. Defaults: creation 20%, read 60%; samples are independent.'
        )}
      </p>
      {channel ? (
        <label className='block space-y-1 text-sm'>
          {t('Mode')}
          <NativeSelect
            value={value.mode ?? 'inherit'}
            onChange={(e) =>
              onChange({
                ...value,
                mode: e.target.value as CachePolicy['mode'],
              })
            }
          >
            <NativeSelectOption value='inherit'>
              {t('Inherit global settings')}
            </NativeSelectOption>
            <NativeSelectOption value='off'>{t('Disabled')}</NativeSelectOption>
            <NativeSelectOption value='custom'>
              {t('Custom')}
            </NativeSelectOption>
          </NativeSelect>
        </label>
      ) : (
        <div className='flex flex-wrap gap-4'>
          <label className='flex items-center gap-2 text-sm'>
            <Switch
              checked={value.enabled ?? false}
              onCheckedChange={(enabled) => onChange({ ...value, enabled })}
            />
            {t('Enabled')}
          </label>
          <label className='flex items-center gap-2 text-sm'>
            <Switch
              checked={value.force_disabled ?? false}
              onCheckedChange={(force_disabled) =>
                onChange({ ...value, force_disabled })
              }
            />
            {t('Emergency stop for all channels')}
          </label>
        </div>
      )}
      <div className='grid gap-3 sm:grid-cols-2'>
        {fields.map(([key, label]) => (
          <label key={key} className='space-y-1 text-sm'>
            {label}
            <Input
              disabled={channel && value.mode !== 'custom'}
              type='number'
              min={0}
              max={100}
              step={1}
              value={value[key] ?? DEFAULT_CACHE[key]}
              onChange={(e) =>
                onChange({
                  ...value,
                  [key]:
                    e.target.value === '' ? undefined : Number(e.target.value),
                })
              }
            />
          </label>
        ))}
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Token shares must total at most 100%. Both trigger probabilities cannot be 100%.'
        )}
      </p>
    </fieldset>
  )
}
