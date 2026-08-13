/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge, type StatusBadgeProps } from '@/components/status-badge'

import type {
  MultiKeyTestClassification,
  MultiKeyTestResult,
} from '../../types'

const testResultConfig: Record<
  MultiKeyTestClassification,
  { label: string; variant: StatusBadgeProps['variant'] }
> = {
  available: { label: 'Available', variant: 'success' },
  auth_failed: { label: 'Authentication failed', variant: 'danger' },
  quota_exhausted: { label: 'Quota exhausted', variant: 'warning' },
  rate_limited: { label: 'Rate limited', variant: 'warning' },
  model_forbidden: { label: 'Model unavailable', variant: 'warning' },
  configuration_error: { label: 'Configuration error', variant: 'danger' },
  upstream_error: { label: 'Upstream error', variant: 'danger' },
  network_error: { label: 'Network error', variant: 'danger' },
  response_error: { label: 'Invalid response', variant: 'danger' },
}

type MultiKeyTestResultBadgeProps = {
  result?: MultiKeyTestResult
  isTesting: boolean
  onShowDetails: (result: MultiKeyTestResult) => void
}

export function MultiKeyTestResultBadge(props: MultiKeyTestResultBadgeProps) {
  const { t } = useTranslation()
  if (props.isTesting) {
    return (
      <StatusBadge variant='info' copyable={false} pulse>
        <Loader2 className='h-3.5 w-3.5 animate-spin' />
        {t('Testing')}
      </StatusBadge>
    )
  }
  if (!props.result) {
    return (
      <StatusBadge label={t('Not tested')} variant='neutral' copyable={false} />
    )
  }

  const result = props.result
  const config = testResultConfig[result.classification]
  return (
    <button
      type='button'
      className='max-w-full text-left'
      onClick={() => props.onShowDetails(result)}
      title={t('View test details')}
    >
      <StatusBadge
        label={t(config.label)}
        variant={config.variant}
        copyable={false}
      />
    </button>
  )
}
