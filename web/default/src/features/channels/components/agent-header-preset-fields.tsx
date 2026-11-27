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
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'

import { AGENT_HEADER_DEFAULTS } from '../lib/agent-header-presets'
import type { ChannelFormValues } from '../lib/channel-form'

export function AgentHeaderPresetFields(props: { disabled: boolean }) {
  const { t } = useTranslation()
  const form = useFormContext<ChannelFormValues>()
  const preset = useWatch({
    control: form.control,
    name: 'agent_header_preset',
  })
  return (
    <div className='space-y-3 border-t pt-4'>
      <FormField
        control={form.control}
        name='agent_header_preset'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Client header preset')}</FormLabel>
            <Select
              value={field.value || 'none'}
              items={[
                { value: 'none', label: t('No preset') },
                { value: 'codex', label: 'Codex CLI' },
                { value: 'claude', label: 'Claude Code' },
              ]}
              disabled={props.disabled}
              onValueChange={(value) => {
                if (value !== 'codex' && value !== 'claude') {
                  field.onChange('')
                  form.setValue('agent_header_defaults', '', {
                    shouldDirty: true,
                    shouldValidate: true,
                  })
                  return
                }
                field.onChange(value)
                form.setValue(
                  'agent_header_defaults',
                  JSON.stringify(AGENT_HEADER_DEFAULTS[value], null, 2),
                  { shouldDirty: true, shouldValidate: true }
                )
              }}
            >
              <FormControl>
                <SelectTrigger onBlur={field.onBlur} ref={field.ref}>
                  <SelectValue placeholder={t('Fill preset')} />
                </SelectTrigger>
              </FormControl>
              <SelectContent>
                <SelectItem value='none'>{t('No preset')}</SelectItem>
                <SelectItem value='codex'>Codex CLI</SelectItem>
                <SelectItem value='claude'>Claude Code</SelectItem>
              </SelectContent>
            </Select>
            <FormDescription>
              {t(
                'Matching clients keep their headers; other requests use these defaults. Explicit header overrides take priority.'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      {preset && (
        <FormField
          control={form.control}
          name='agent_header_defaults'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Default client headers')}</FormLabel>
              <FormControl>
                <Textarea
                  {...field}
                  value={field.value || ''}
                  disabled={props.disabled}
                  rows={6}
                  className='font-mono text-sm'
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
      )}
    </div>
  )
}
