import { useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'

import { getUserRateLimitConfig } from './api'
import { OverviewCard } from './components/overview-card'
import { ResponseConfigCard } from './components/response-config-card'
import { RulesCard } from './components/rules-card'

export function UserRateLimits() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const configQuery = useQuery({
    queryKey: ['user-rate-limit-config'],
    queryFn: getUserRateLimitConfig,
  })

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Independent Rate Limits')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        {configQuery.isLoading && (
          <div className='space-y-4'>
            <Skeleton className='h-56 w-full rounded-xl' />
            <Skeleton className='h-80 w-full rounded-xl' />
            <Skeleton className='h-96 w-full rounded-xl' />
          </div>
        )}

        {configQuery.isError && (
          <Alert variant='destructive'>
            <AlertTriangle />
            <AlertTitle>
              {t('Failed to load independent rate limits')}
            </AlertTitle>
            <AlertDescription>{configQuery.error.message}</AlertDescription>
          </Alert>
        )}

        {configQuery.data && (
          <div className='space-y-5'>
            <OverviewCard config={configQuery.data} />
            <ResponseConfigCard
              config={configQuery.data}
              onSaved={(config) => {
                queryClient.setQueryData(['user-rate-limit-config'], config)
                queryClient.invalidateQueries({
                  queryKey: ['user-rate-limit-rules'],
                })
              }}
            />
            <Alert>
              <AlertTriangle />
              <AlertTitle>{t('Operational logging notice')}</AlertTitle>
              <AlertDescription>
                {t(
                  'When error logging is enabled, every independent rate-limit rejection writes one usage log. Long delays also increase active connections.'
                )}
              </AlertDescription>
            </Alert>
            <RulesCard config={configQuery.data} />
          </div>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
