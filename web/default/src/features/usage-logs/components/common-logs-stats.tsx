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
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatNumber, formatTokens } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getLogStats, getUserLogStats } from '../api'
import { DEFAULT_LOG_STATS } from '../constants'
import { buildApiParams } from '../lib/utils'
import { useLogsViewScope, useUsageLogsContext } from './usage-logs-provider'

const route = getRouteApi('/_authenticated/usage-logs/$section')

function StatBadge(props: {
  label: string
  value: string | number
  accent: string
}) {
  return (
    <span className='border-border/60 bg-muted/25 inline-flex h-7 items-center gap-2 rounded-md border px-2.5 text-xs shadow-xs'>
      <span className={cn('h-3.5 w-0.5 rounded-full', props.accent)} />
      <span className='text-muted-foreground'>{props.label}</span>
      <span className='text-foreground/85 font-mono font-semibold tabular-nums'>
        {props.value}
      </span>
    </span>
  )
}

/** Format a zero token count explicitly while keeping large values compact. */
function formatStatTokens(value: number): string {
  return value === 0 ? '0' : formatTokens(value)
}

export function CommonLogsStats() {
  const { t } = useTranslation()
  const { isAdminView: isAdmin } = useLogsViewScope()
  const searchParams = route.useSearch()
  const { sensitiveVisible } = useUsageLogsContext()

  const { data: stats, isLoading } = useQuery({
    queryKey: ['usage-logs-stats', isAdmin, searchParams],
    queryFn: async () => {
      const params = buildApiParams({
        page: 1,
        pageSize: 1,
        searchParams,
        columnFilters: [],
        isAdmin,
      })

      const result = isAdmin
        ? await getLogStats(params)
        : await getUserLogStats(params)

      return result.success
        ? result.data || DEFAULT_LOG_STATS
        : DEFAULT_LOG_STATS
    },
    placeholderData: (previousData) => previousData,
  })

  if (isLoading) {
    return (
      <div className='flex flex-col items-start gap-1.5'>
        <div className='flex items-center gap-2'>
          <Skeleton className='h-7 w-[150px] rounded-md' />
          {isAdmin && <Skeleton className='h-7 w-[110px] rounded-md' />}
          <Skeleton className='h-7 w-[100px] rounded-md' />
          <Skeleton className='h-7 w-[120px] rounded-md' />
        </div>
        {isAdmin && (
          <div className='flex items-center gap-2'>
            <Skeleton className='h-7 w-[120px] rounded-md' />
            <Skeleton className='h-7 w-[120px] rounded-md' />
            <Skeleton className='h-7 w-[150px] rounded-md' />
            <Skeleton className='h-7 w-[150px] rounded-md' />
          </div>
        )}
      </div>
    )
  }

  return (
    <div className='flex flex-col items-start gap-1.5'>
      <div className='flex flex-wrap items-center gap-2'>
        <StatBadge
          label={t('Usage')}
          value={
            sensitiveVisible
              ? formatQuotaWithCurrency(stats?.quota || 0, {
                  digitsLarge: 2,
                  digitsSmall: 4,
                  abbreviate: false,
                })
              : '••••'
          }
          accent='bg-sky-500/70'
        />
        {isAdmin && (
          <StatBadge
            label={t('Request')}
            value={formatNumber(stats?.request_count || 0)}
            accent='bg-indigo-500/70'
          />
        )}
        <StatBadge
          label={t('RPM')}
          value={stats?.rpm || 0}
          accent='bg-rose-500/65'
        />
        <StatBadge
          label={t('TPM')}
          value={formatStatTokens(stats?.tpm || 0)}
          accent='bg-slate-400/70'
        />
      </div>
      {isAdmin && (
        <div className='flex flex-wrap items-center gap-2'>
          <StatBadge
            label={t('Input')}
            value={formatStatTokens(stats?.input_tokens || 0)}
            accent='bg-cyan-500/70'
          />
          <StatBadge
            label={t('Output')}
            value={formatStatTokens(stats?.output_tokens || 0)}
            accent='bg-violet-500/70'
          />
          <StatBadge
            label={t('Cache Creation')}
            value={formatStatTokens(stats?.cache_creation_tokens || 0)}
            accent='bg-amber-500/75'
          />
          <StatBadge
            label={t('Cache Read')}
            value={formatStatTokens(stats?.cache_read_tokens || 0)}
            accent='bg-emerald-500/70'
          />
        </div>
      )}
    </div>
  )
}
