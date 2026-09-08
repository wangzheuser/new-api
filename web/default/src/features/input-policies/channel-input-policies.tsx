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
import { useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { useSystemOptions } from '@/features/system-settings/hooks/use-system-options'

import { CachePolicyEditor } from './cache-policy-editor'
import { ContextPolicyEditor } from './context-policy-editor'
import {
  CACHE_OPTION,
  CONTEXT_OPTION,
  mergeInputPolicy,
  validateInputPolicies,
  type CachePolicy,
  type ContextPolicy,
  type ContextRule,
} from './policy'

/** Change only the two owned keys inside the channel's existing settings JSON. */
export function ChannelInputPolicies({
  value,
  onChange,
}: {
  value: string
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const { data } = useSystemOptions()
  const cacheDraft = useRef<CachePolicy | undefined>(undefined)
  const contextDrafts = useRef<Record<string, ContextRule>>({})
  let globalContext: ContextPolicy = { models: {} }
  let globalCache: CachePolicy = {}
  try {
    globalContext = JSON.parse(
      data?.data?.find((o) => o.key === CONTEXT_OPTION)?.value ??
        '{"models":{}}'
    )
    globalCache = JSON.parse(
      data?.data?.find((o) => o.key === CACHE_OPTION)?.value ?? '{}'
    )
  } catch {
    /* Invalid persisted settings remain disabled on the server. */
  }
  let settings: {
    context_truncation?: ContextPolicy
    cache_usage_simulation?: CachePolicy
  }
  try {
    settings = JSON.parse(value || '{}')
    if (!settings || typeof settings !== 'object' || Array.isArray(settings)) {
      throw new Error()
    }
  } catch {
    return (
      <p role='alert' className='text-destructive text-sm'>
        {t('Invalid JSON format')}
      </p>
    )
  }
  let cacheValue = settings.cache_usage_simulation ?? {
    mode: 'inherit' as const,
  }
  if (!cacheValue.mode || cacheValue.mode === 'inherit') {
    const {
      enabled: _enabled,
      force_disabled: _stop,
      ...inherited
    } = globalCache ?? {}
    cacheValue = { ...inherited, mode: 'inherit' }
  }
  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Channel custom settings override global defaults. A global emergency stop overrides every channel.'
        )}
      </p>
      {(globalContext?.force_disabled || globalCache?.force_disabled) && (
        <p role='status' className='text-destructive text-sm'>
          {t('Emergency stop for all channels')}:{' '}
          {[
            globalContext?.force_disabled && t('Context truncation'),
            globalCache?.force_disabled && t('Cache usage simulation'),
          ]
            .filter(Boolean)
            .join(' / ')}
        </p>
      )}
      <ContextPolicyEditor
        channel
        inherited={globalContext}
        value={settings.context_truncation ?? { models: {} }}
        onChange={(p) => {
          for (const [id, rule] of Object.entries(p.models)) {
            const previous = settings.context_truncation?.models?.[id]
            if (previous?.mode === 'custom' && rule.mode !== 'custom') {
              contextDrafts.current[id] = previous
            }
            if (rule.mode !== 'custom') {
              p.models[id] = { mode: rule.mode }
            } else if (previous?.mode !== 'custom') {
              const inherited = globalContext?.models?.[id]
              p.models[id] =
                contextDrafts.current[id] ??
                (inherited?.mode === 'custom' ? { ...inherited } : rule)
            }
          }
          onChange(mergeInputPolicy(value, 'context_truncation', p))
        }}
      />
      <CachePolicyEditor
        channel
        value={cacheValue}
        onChange={(p) => {
          const previous = settings.cache_usage_simulation
          if (previous?.mode === 'custom' && p.mode !== 'custom') {
            cacheDraft.current = previous
          }
          if (p.mode !== 'custom') {
            p = { mode: p.mode }
          } else if (previous?.mode !== 'custom') {
            const {
              enabled: _enabled,
              force_disabled: _stop,
              ...inherited
            } = globalCache ?? {}
            p = { ...(cacheDraft.current ?? inherited), mode: 'custom' }
          }
          onChange(mergeInputPolicy(value, 'cache_usage_simulation', p))
        }}
      />
      {!validateInputPolicies(value || '{}') && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Invalid input policy. Check model budgets and percentages.')}
        </p>
      )}
    </div>
  )
}
