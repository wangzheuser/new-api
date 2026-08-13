/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Label } from '@/components/ui/label'

import type { MultiKeyTestResult } from '../../types'
import { MultiKeyTestResultBadge } from './multi-key-test-result'

type MultiKeyTestDetailsDialogProps = {
  result: MultiKeyTestResult | null
  onOpenChange: (open: boolean) => void
}

export function MultiKeyTestDetailsDialog(
  props: MultiKeyTestDetailsDialogProps
) {
  const { t } = useTranslation()
  const result = props.result

  return (
    <Dialog
      open={result !== null}
      onOpenChange={props.onOpenChange}
      title={t('Key test details')}
      description={
        result
          ? t('Test result for key #{{index}}', { index: result.key_index + 1 })
          : ''
      }
      contentClassName='sm:max-w-lg'
      contentHeight='auto'
      bodyClassName='space-y-4'
    >
      {result && (
        <div className='space-y-4 py-2'>
          <div className='grid grid-cols-2 gap-4 text-sm'>
            <div className='space-y-1'>
              <Label>{t('Test result')}</Label>
              <MultiKeyTestResultBadge
                result={result}
                isTesting={false}
                onShowDetails={() => undefined}
              />
            </div>
            <div className='space-y-1'>
              <Label>{t('HTTP status')}</Label>
              <p className='font-mono'>{result.http_status || '-'}</p>
            </div>
            <div className='space-y-1'>
              <Label>{t('Response time')}</Label>
              <p>
                {result.time !== undefined
                  ? `${result.time.toFixed(2)} s`
                  : '-'}
              </p>
            </div>
            <div className='space-y-1'>
              <Label>{t('Error code')}</Label>
              <p className='font-mono break-all'>{result.error_code || '-'}</p>
            </div>
          </div>
          <div className='space-y-2'>
            <Label>{t('Error message')}</Label>
            <div className='bg-muted/50 min-h-20 rounded-md border p-3 text-sm break-words whitespace-pre-wrap'>
              {result.message || t('No error message')}
            </div>
          </div>
        </div>
      )}
    </Dialog>
  )
}
