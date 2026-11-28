import { useQuery } from '@tanstack/react-query'
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
import { Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'

import { formatTimestamp } from '../lib'
import { useSubscriptions } from './subscriptions-provider'

type UnfulfilledOrder = {
  id: number
  user_id: number
  plan_id: number
  trade_no: string
  payment_provider: string
  money: number
  paid_at: number
  status: 'paid' | 'payment_review' | 'unverified_payment'
}

export function SubscriptionsPrimaryButtons() {
  const { t } = useTranslation()
  const { setOpen, complianceConfirmed } = useSubscriptions()
  const [ordersOpen, setOrdersOpen] = useState(false)
  const [page, setPage] = useState(1)
  const [retrying, setRetrying] = useState<number>()
  const orders = useQuery({
    queryKey: ['subscriptions', 'unfulfilled-orders', page],
    enabled: ordersOpen,
    queryFn: async () => {
      const res = await api.get('/api/subscription/admin/orders/unfulfilled', {
        params: { p: page, page_size: 20 },
      })
      if (!res.data.success) throw new Error(res.data.message)
      return res.data.data as { items: UnfulfilledOrder[]; total: number }
    },
  })

  const retry = async (order: UnfulfilledOrder) => {
    setRetrying(order.id)
    try {
      const res = await api.post(
        `/api/subscription/admin/orders/${encodeURIComponent(order.trade_no)}/retry`
      )
      if (res.data.success) await orders.refetch()
    } catch {
      toast.error(t('Request failed'))
    } finally {
      setRetrying(undefined)
    }
  }

  return (
    <div className='flex gap-2'>
      <Button size='sm' variant='outline' onClick={() => setOrdersOpen(true)}>
        {t('Unfulfilled subscription orders')}
      </Button>
      <Dialog open={ordersOpen} onOpenChange={setOrdersOpen}>
        <DialogContent className='sm:max-w-2xl'>
          <DialogHeader>
            <DialogTitle>{t('Unfulfilled subscription orders')}</DialogTitle>
          </DialogHeader>
          <div className='max-h-[55vh] space-y-2 overflow-y-auto'>
            {orders.isPending && <p>{t('Loading...')}</p>}
            {orders.isError && (
              <Button variant='outline' onClick={() => orders.refetch()}>
                {t('Retry')}
              </Button>
            )}
            {orders.data?.items.length === 0 && <p>{t('No data')}</p>}
            {orders.data?.items.map((order) => (
              <div
                key={order.id}
                className='grid gap-2 rounded-md border p-3 sm:grid-cols-[1fr_auto]'
              >
                <div className='min-w-0 space-y-1 text-sm'>
                  <div className='font-medium break-all'>{order.trade_no}</div>
                  <div>
                    {order.payment_provider} · {t('User')} #{order.user_id} ·{' '}
                    {t('Plan')} #{order.plan_id} · {formatNumber(order.money)}
                  </div>
                  <div>
                    {formatTimestamp(order.paid_at)} ·{' '}
                    {order.status === 'paid'
                      ? t('Confirmed payment')
                      : t('Payment review')}
                  </div>
                </div>
                {order.status === 'paid' && (
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={retrying !== undefined}
                    onClick={() => retry(order)}
                  >
                    {t('Retry fulfillment')}
                  </Button>
                )}
              </div>
            ))}
          </div>
          {orders.data && orders.data.total > 20 && (
            <div className='flex justify-end gap-2'>
              <Button
                variant='outline'
                disabled={page <= 1}
                onClick={() => setPage(page - 1)}
              >
                {t('Previous')}
              </Button>
              <Button
                variant='outline'
                disabled={page * 20 >= orders.data.total}
                onClick={() => setPage(page + 1)}
              >
                {t('Next')}
              </Button>
            </div>
          )}
        </DialogContent>
      </Dialog>
      <Button
        size='sm'
        onClick={() => setOpen('create')}
        disabled={!complianceConfirmed}
      >
        <Plus className='h-4 w-4' />
        {t('Create Plan')}
      </Button>
    </div>
  )
}
