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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import {
  formatTimestampForInput,
  parseQuotaFromDollars,
  parseTimestampFromInput,
  quotaUnitsToDollars,
} from '@/lib/format'

import { updateUserSubscription } from '../../api'
import type { UserSubscriptionRecord } from '../../types'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  record: UserSubscriptionRecord
  planTitle: string
  onSuccess: () => Promise<void> | void
}

export function EditUserSubscriptionDialog(props: Props) {
  const { t } = useTranslation()
  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const [endTime, setEndTime] = useState('')
  const [amountUsed, setAmountUsed] = useState('')
  const [amountTotal, setAmountTotal] = useState('')
  const [unlimited, setUnlimited] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!props.open) return
    const sub = props.record.subscription
    setEndTime(formatTimestampForInput(sub.end_time))
    setAmountUsed(String(quotaUnitsToDollars(Number(sub.amount_used || 0))))
    setAmountTotal(String(quotaUnitsToDollars(Number(sub.amount_total || 0))))
    setUnlimited(Number(sub.amount_total || 0) === 0)
  }, [props.open, props.record])

  const handleSave = async () => {
    const parsedEndTime = parseTimestampFromInput(endTime)
    const usedDisplayAmount = Number(amountUsed)
    const totalDisplayAmount = Number(amountTotal)
    if (!Number.isFinite(parsedEndTime) || parsedEndTime <= 0) {
      toast.error(t('Please enter a valid end time'))
      return
    }
    if (!Number.isFinite(usedDisplayAmount) || usedDisplayAmount < 0) {
      toast.error(t('Quota cannot be negative'))
      return
    }
    if (
      !unlimited &&
      (!Number.isFinite(totalDisplayAmount) || totalDisplayAmount <= 0)
    ) {
      toast.error(t('Please enter amount'))
      return
    }

    const usedQuota = parseQuotaFromDollars(usedDisplayAmount)
    const totalQuota = unlimited ? 0 : parseQuotaFromDollars(totalDisplayAmount)
    if (totalQuota > 0 && usedQuota > totalQuota) {
      toast.error(t('Used quota cannot exceed total quota'))
      return
    }

    setSaving(true)
    try {
      const result = await updateUserSubscription(
        props.record.subscription.id,
        {
          end_time: parsedEndTime,
          amount_used: usedQuota,
          amount_total: totalQuota,
        }
      )
      if (result.success) {
        toast.success(result.data?.message || t('Update succeeded'))
        props.onOpenChange(false)
        await props.onSuccess()
      } else {
        toast.error(result.message || t('Request failed'))
      }
    } catch {
      toast.error(t('Request failed'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Edit subscription')}
      description={`${props.planTitle} · #${props.record.subscription.id}`}
      contentHeight='auto'
      contentClassName='sm:max-w-md'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={saving}
          >
            {t('Cancel')}
          </Button>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? t('Processing...') : t('Save')}
          </Button>
        </>
      }
    >
      <div className='space-y-2'>
        <Label htmlFor='subscription-end-time'>{t('End')}</Label>
        <Input
          id='subscription-end-time'
          type='datetime-local'
          value={endTime}
          onChange={(event) => setEndTime(event.target.value)}
        />
      </div>

      <div className='grid grid-cols-1 gap-4 sm:grid-cols-2'>
        <div className='space-y-2'>
          <Label htmlFor='subscription-used-quota'>
            {t('Used Quota')} ({currencyLabel})
          </Label>
          <Input
            id='subscription-used-quota'
            type='number'
            min={0}
            step={tokensOnly ? 1 : 0.000001}
            value={amountUsed}
            onChange={(event) => setAmountUsed(event.target.value)}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor='subscription-total-quota'>
            {t('Total Quota')} ({currencyLabel})
          </Label>
          <Input
            id='subscription-total-quota'
            type='number'
            min={0}
            step={tokensOnly ? 1 : 0.000001}
            value={amountTotal}
            onChange={(event) => setAmountTotal(event.target.value)}
            disabled={unlimited}
          />
        </div>
      </div>

      <div className='flex items-center justify-between gap-4 border-t pt-4'>
        <Label htmlFor='subscription-unlimited'>{t('Unlimited')}</Label>
        <Switch
          id='subscription-unlimited'
          checked={unlimited}
          onCheckedChange={(checked) => setUnlimited(!!checked)}
        />
      </div>
    </Dialog>
  )
}
