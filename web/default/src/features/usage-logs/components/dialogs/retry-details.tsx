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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

import { getRequestAttemptLogs } from '../../api'
import type { UsageLog } from '../../data/schema'
import {
  canShowRetryDetails,
  normalizeRequestAttempts,
  type RequestAttempt,
} from '../../lib/request-attempts'

interface RetryDetailsProps {
  log: UsageLog
  isAdmin: boolean
  open: boolean
}

export function RetryAttemptList(props: {
  attempts: RequestAttempt[]
  incomplete: boolean
}) {
  const { t } = useTranslation()
  return (
    <div className='min-w-0 space-y-3'>
      {props.incomplete && (
        <p role='status' className='text-muted-foreground text-xs'>
          {t('Historical retry details are incomplete')}
        </p>
      )}
      {props.attempts.length === 0 && (
        <p className='text-muted-foreground text-xs'>
          {t('No attempt errors were recorded')}
        </p>
      )}
      {props.attempts.map((attempt, index) => (
        <div
          key={attempt.attempt ?? `unknown-${index}`}
          className='min-w-0 space-y-2 rounded-md border p-3 text-xs'
        >
          <div className='flex min-w-0 flex-wrap items-center gap-2'>
            <span className='font-medium'>
              {attempt.attempt === null
                ? t('Unknown attempt')
                : t('Attempt {{number}}', { number: attempt.attempt })}
            </span>
            <span className='[overflow-wrap:anywhere]'>
              {t('Channel')}: {attempt.channel}
              {attempt.channelName ? ` (${attempt.channelName})` : ''}
            </span>
            {attempt.statusCode !== null && (
              <span>
                {t('Error status code')}: {attempt.statusCode}
              </span>
            )}
            {attempt.errorCode !== undefined && (
              <span className='[overflow-wrap:anywhere]'>
                {t('Error code')}: {attempt.errorCode}
              </span>
            )}
            <CopyButton
              value={attempt.message}
              size='icon'
              className='ml-auto size-6'
            />
          </div>
          <p className='[overflow-wrap:anywhere] whitespace-pre-wrap'>
            {attempt.message || t('No error message was recorded')}
          </p>
        </div>
      ))}
    </div>
  )
}

export function RetryDetails(props: RetryDetailsProps) {
  const { t } = useTranslation()
  const [expanded, setExpanded] = useState(false)
  const visible = canShowRetryDetails(props.log, props.isAdmin)
  const query = useQuery({
    queryKey: ['usage-logs', 'retry-details', props.log.request_id],
    enabled: visible && props.open && expanded,
    queryFn: ({ signal }) =>
      getRequestAttemptLogs(props.log.request_id, signal),
    staleTime: 0,
    refetchOnWindowFocus: false,
    retry: false,
  })
  if (!visible) return null
  const details = query.data
    ? normalizeRequestAttempts(query.data, props.log)
    : null
  return (
    <Accordion
      className='min-w-0'
      value={expanded ? ['attempts'] : []}
      onValueChange={(value) => setExpanded(value.includes('attempts'))}
    >
      <AccordionItem value='attempts'>
        <AccordionTrigger className='text-xs'>
          {t('Retry details')}
        </AccordionTrigger>
        <AccordionContent>
          <p className='text-muted-foreground mb-3 text-xs'>
            {t('Original attempt errors; the final result is shown separately')}
          </p>
          {query.isFetching && (
            <div role='status' aria-label={t('Loading...')}>
              <Skeleton className='mb-2 h-4 w-2/3' />
              <Skeleton className='h-12 w-full' />
            </div>
          )}
          {query.isError && !query.isFetching && (
            <ErrorState
              title={t('Failed to load retry details')}
              onRetry={() => void query.refetch()}
              className='min-h-0 py-3'
            />
          )}
          {details && !query.isError && !query.isFetching && (
            <>
              <RetryAttemptList {...details} />
              <Button
                variant='ghost'
                size='sm'
                className='mt-2'
                onClick={() => void query.refetch()}
              >
                {t('Refresh')}
              </Button>
            </>
          )}
        </AccordionContent>
      </AccordionItem>
    </Accordion>
  )
}
