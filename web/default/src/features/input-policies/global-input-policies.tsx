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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { updateSystemOption } from '@/features/system-settings/api'
import { useSystemOptions } from '@/features/system-settings/hooks/use-system-options'

import { CachePolicyEditor } from './cache-policy-editor'
import { ContextPolicyEditor } from './context-policy-editor'
import {
  CACHE_OPTION,
  CONTEXT_OPTION,
  DEFAULT_CACHE,
  validateInputPolicies,
  type CachePolicy,
  type ContextPolicy,
} from './policy'

/** Save each whole policy atomically, without re-saving unrelated system options. */
export function GlobalInputPolicies() {
  const { t } = useTranslation()
  const { data, isPending, isError } = useSystemOptions()
  const client = useQueryClient()
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const save = useMutation({
    mutationFn: async ({ key, value }: { key: string; value: string }) => {
      const response = await updateSystemOption({ key, value })
      if (!response.success) throw new Error(response.message)
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['system-options'] })
      toast.success(t('Saved successfully'))
    },
    onError: (error: Error) => toast.error(error.message),
  })
  if (isPending) return <p>{t('Loading...')}</p>
  if (isError || !data?.success) {
    return <p role='alert'>{t('Failed to load settings')}</p>
  }
  const contextRaw =
    drafts[CONTEXT_OPTION] ??
    data.data.find((o) => o.key === CONTEXT_OPTION)?.value ??
    '{"models":{}}'
  const cacheRaw =
    drafts[CACHE_OPTION] ??
    data.data.find((o) => o.key === CACHE_OPTION)?.value ??
    JSON.stringify(DEFAULT_CACHE)
  let context: ContextPolicy
  let cache: CachePolicy
  try {
    context = JSON.parse(contextRaw)
    cache = JSON.parse(cacheRaw)
    if (!context?.models || !cache || typeof cache !== 'object') {
      throw new Error()
    }
  } catch {
    return (
      <div className='space-y-3'>
        <p role='alert'>
          {t('Invalid input policy. Check model budgets and percentages.')}
        </p>
        <Button
          type='button'
          onClick={() =>
            setDrafts({
              [CONTEXT_OPTION]: '{"models":{}}',
              [CACHE_OPTION]: JSON.stringify(DEFAULT_CACHE),
            })
          }
        >
          {t('Reset to defaults')}
        </Button>
      </div>
    )
  }
  return (
    <div className='space-y-4'>
      <ContextPolicyEditor
        value={context}
        onChange={(p) =>
          setDrafts({ ...drafts, [CONTEXT_OPTION]: JSON.stringify(p) })
        }
      />
      {!validateInputPolicies(
        JSON.stringify({ context_truncation: context }),
        false
      ) && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Invalid input policy. Check model budgets and percentages.')}
        </p>
      )}
      <Button
        type='button'
        disabled={
          save.isPending ||
          !validateInputPolicies(
            JSON.stringify({ context_truncation: context }),
            false
          )
        }
        onClick={() => save.mutate({ key: CONTEXT_OPTION, value: contextRaw })}
      >
        {t('Save context truncation')}
      </Button>
      <CachePolicyEditor
        value={cache}
        onChange={(p) =>
          setDrafts({ ...drafts, [CACHE_OPTION]: JSON.stringify(p) })
        }
      />
      <Button
        type='button'
        disabled={
          save.isPending ||
          !validateInputPolicies(
            JSON.stringify({ cache_usage_simulation: cache }),
            false
          )
        }
        onClick={() => save.mutate({ key: CACHE_OPTION, value: cacheRaw })}
      >
        {t('Save cache simulation')}
      </Button>
    </div>
  )
}
