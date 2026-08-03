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
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { type ReactNode, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import { formatTimestampToDate } from '@/lib/format'

import type {
  ApiResponse,
  RegistrationCode,
  RegistrationCodeUsage,
  RegistrationCodeUsagePage,
} from './types'

const PAGE_SIZE = 20
const SKELETON_KEYS = [
  'registration-usage-skeleton-1',
  'registration-usage-skeleton-2',
  'registration-usage-skeleton-3',
  'registration-usage-skeleton-4',
  'registration-usage-skeleton-5',
]

type RegistrationCodeUsagesDialogProps = {
  code: RegistrationCode
  open: boolean
  onOpenChange: (open: boolean) => void
}

/** Displays the accounts that consumed one registration code. */
export function RegistrationCodeUsagesDialog(
  props: RegistrationCodeUsagesDialogProps
) {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)

  const { data, isLoading, isFetching, isError, refetch } = useQuery({
    queryKey: ['registration-code-usages', props.code.id, page],
    queryFn: async () => {
      const response = await api.get<ApiResponse<RegistrationCodeUsagePage>>(
        `/api/registration_code/${props.code.id}/usages?p=${page}&page_size=${PAGE_SIZE}`
      )
      if (!response.data.success) throw new Error(response.data.message)
      return response.data.data ?? { items: [], total: 0 }
    },
    placeholderData: (previousData) => previousData,
  })

  const columns = useMemo<StaticDataTableColumn<RegistrationCodeUsage>[]>(
    () => [
      {
        id: 'user_id',
        header: t('User ID'),
        cell: (usage) => usage.user_id,
      },
      {
        id: 'username',
        header: t('Username'),
        cell: (usage) => usage.username || '-',
      },
      {
        id: 'source',
        header: t('Source'),
        cell: (usage) => usage.source,
      },
      {
        id: 'used_time',
        header: t('Used At'),
        cell: (usage) => formatTimestampToDate(usage.used_time),
      },
    ],
    [t]
  )

  const items = data?.items ?? []
  const total = data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  let content: ReactNode
  if (isLoading) {
    content = (
      <div className='space-y-2'>
        {SKELETON_KEYS.map((key) => (
          <Skeleton key={key} className='h-10 w-full' />
        ))}
      </div>
    )
  } else if (isError) {
    content = (
      <div className='flex min-h-32 flex-col items-center justify-center gap-3'>
        <p className='text-muted-foreground text-sm'>{t('Request failed')}</p>
        <Button variant='outline' size='sm' onClick={() => refetch()}>
          {t('Retry')}
        </Button>
      </div>
    )
  } else {
    content = (
      <StaticDataTable
        columns={columns}
        data={items}
        getRowKey={(usage) => usage.id}
        emptyContent={t('No usage records found')}
      />
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={`${t('Usage Records')} · ${props.code.name}`}
      contentHeight='auto'
      contentClassName='max-sm:w-[calc(100vw-1rem)] sm:max-w-3xl'
      bodyClassName='space-y-4'
    >
      {content}

      {!isLoading && !isError && total > 0 && (
        <div className='flex flex-col items-center gap-3 border-t pt-4 sm:flex-row sm:justify-between'>
          <div className='text-muted-foreground text-sm'>
            {t('Showing')} {(page - 1) * PAGE_SIZE + 1}-
            {Math.min(page * PAGE_SIZE, total)} {t('of')} {total}
          </div>
          <div className='flex items-center gap-2'>
            <Button
              variant='outline'
              size='sm'
              disabled={page === 1 || isFetching}
              onClick={() => setPage((current) => current - 1)}
            >
              <ChevronLeft />
              {t('Previous')}
            </Button>
            <span className='text-muted-foreground text-sm tabular-nums'>
              {page} / {totalPages}
            </span>
            <Button
              variant='outline'
              size='sm'
              disabled={page === totalPages || isFetching}
              onClick={() => setPage((current) => current + 1)}
            >
              {t('Next')}
              <ChevronRight />
            </Button>
          </div>
        </div>
      )}
    </Dialog>
  )
}
