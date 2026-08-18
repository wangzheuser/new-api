import { Link } from '@tanstack/react-router'
import { Clock3, Gauge, Settings2, ShieldCheck } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import type { UserRateLimitConfig } from '../types'

type OverviewCardProps = {
  config: UserRateLimitConfig
}

export function OverviewCard(props: OverviewCardProps) {
  const { t } = useTranslation()
  const base = props.config.base_limit

  return (
    <Card className='via-background to-background overflow-hidden border-sky-500/20 bg-linear-to-br from-sky-500/5'>
      <CardHeader className='flex-row items-start justify-between gap-4'>
        <div className='space-y-1'>
          <CardTitle className='flex items-center gap-2'>
            <ShieldCheck className='size-5 text-sky-600' />
            {t('Current model rate-limit state')}
          </CardTitle>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Independent rules share the existing switch and counting period.'
            )}
          </p>
        </div>
        <Badge variant={base.enabled ? 'default' : 'secondary'}>
          {base.enabled ? t('Enabled') : t('Paused')}
        </Badge>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='grid gap-3 sm:grid-cols-3'>
          <Metric
            icon={<Clock3 />}
            label={t('Shared period')}
            value={t('{{count}} minute(s)', { count: base.period_minutes })}
          />
          <Metric
            icon={<Gauge />}
            label={t('Global total requests')}
            value={base.total_count === 0 ? t('Unlimited') : base.total_count}
          />
          <Metric
            icon={<ShieldCheck />}
            label={t('Global successful requests')}
            value={base.success_count}
          />
        </div>

        {!base.enabled && (
          <Alert>
            <ShieldCheck />
            <AlertTitle>{t('Independent rules are paused')}</AlertTitle>
            <AlertDescription>
              {t(
                'The model request rate-limit switch is off. Saved rules remain available and will resume when it is enabled.'
              )}
            </AlertDescription>
          </Alert>
        )}

        <div className='bg-background/70 flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Base counts and group count limits continue to be managed in System Settings.'
            )}
          </p>
          <Button
            variant='outline'
            size='sm'
            render={
              <Link
                to='/system-settings/security/$section'
                params={{ section: 'rate-limit' }}
              />
            }
          >
            <Settings2 />
            {t('Open base settings')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

type MetricProps = {
  icon: ReactNode
  label: string
  value: ReactNode
}

function Metric(props: MetricProps) {
  return (
    <div className='bg-background/80 flex items-center gap-3 rounded-xl border p-3 shadow-xs'>
      <span className='flex size-9 items-center justify-center rounded-lg bg-sky-500/10 text-sky-700 dark:text-sky-300 [&_svg]:size-4'>
        {props.icon}
      </span>
      <div className='min-w-0'>
        <p className='text-muted-foreground truncate text-xs'>{props.label}</p>
        <p className='font-mono text-base font-semibold tabular-nums'>
          {props.value}
        </p>
      </div>
    </div>
  )
}
