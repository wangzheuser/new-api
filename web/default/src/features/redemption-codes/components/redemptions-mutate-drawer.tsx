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
import { zodResolver } from '@hookform/resolvers/zod'
import { CircleDollarSign, Crown, Info } from 'lucide-react'
import { type FormEvent, useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DateTimePicker } from '@/components/datetime-picker'
import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { getAdminPlans } from '@/features/subscriptions/api'
import { formatDuration, formatResetPeriod } from '@/features/subscriptions/lib'
import type { PlanRecord } from '@/features/subscriptions/types'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { formatQuota, parseQuotaFromDollars } from '@/lib/format'
import { addTimeToDate } from '@/lib/time'

import { createRedemption, updateRedemption, getRedemption } from '../api'
import { SUCCESS_MESSAGES } from '../constants'
import {
  getRedemptionFormSchema,
  REDEMPTION_FORM_DEFAULT_VALUES,
  transformFormDataToPayload,
  transformRedemptionToFormDefaults,
  type RedemptionFormValues,
} from '../lib'
import type { Redemption } from '../types'
import { useRedemptions } from './redemptions-provider'

type RedemptionsMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: Redemption
}

export function RedemptionsMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: RedemptionsMutateDrawerProps) {
  const { t } = useTranslation()
  const isUpdate = !!currentRow
  const { triggerRefresh } = useRedemptions()
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [plans, setPlans] = useState<PlanRecord[]>([])
  const [plansLoading, setPlansLoading] = useState(false)

  const form = useForm<RedemptionFormValues>({
    resolver: zodResolver(getRedemptionFormSchema(t)),
    defaultValues: REDEMPTION_FORM_DEFAULT_VALUES,
  })

  // Load the latest plan configuration and existing code data when the drawer opens.
  useEffect(() => {
    if (!open) return

    setPlansLoading(true)
    void getAdminPlans()
      .then((result) => {
        setPlans(result.success ? result.data || [] : [])
      })
      .catch(() => setPlans([]))
      .finally(() => setPlansLoading(false))

    if (open && isUpdate && currentRow) {
      void getRedemption(currentRow.id)
        .then((result) => {
          if (result.success && result.data) {
            form.reset(transformRedemptionToFormDefaults(result.data))
          }
        })
        .catch(() => undefined)
    } else if (open && !isUpdate) {
      form.reset(REDEMPTION_FORM_DEFAULT_VALUES)
    }
  }, [open, isUpdate, currentRow, form])

  const onSubmit = async (data: RedemptionFormValues) => {
    setIsSubmitting(true)
    try {
      const basePayload = transformFormDataToPayload(data)

      if (isUpdate && currentRow) {
        const result = await updateRedemption({
          ...basePayload,
          id: currentRow.id,
        })
        if (result.success) {
          toast.success(t(SUCCESS_MESSAGES.REDEMPTION_UPDATED))
          onOpenChange(false)
          triggerRefresh()
        }
      } else {
        // Create mode
        const result = await createRedemption(basePayload)
        if (result.success) {
          const count = result.data?.length || 0
          toast.success(
            count > 1
              ? t('Successfully created {{count}} redemption codes', {
                  count,
                })
              : t(SUCCESS_MESSAGES.REDEMPTION_CREATED)
          )
          onOpenChange(false)
          triggerRefresh()
        }
      }
    } finally {
      setIsSubmitting(false)
    }
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    if (!isUpdate) {
      const name = form.getValues('name')
      if (!name?.trim()) {
        if (form.getValues('benefit_type') === 'subscription') {
          const plan = plans.find(
            (item) => item.plan.id === form.getValues('plan_id')
          )
          if (plan) {
            form.setValue('name', [...plan.plan.title].slice(0, 20).join(''), {
              shouldValidate: true,
            })
          }
        } else {
          const quota = parseQuotaFromDollars(form.getValues('quota_dollars'))
          form.setValue('name', formatQuota(quota), { shouldValidate: true })
        }
      }
    }

    void form.handleSubmit(onSubmit)(event)
  }

  const handleSetExpiry = (months: number, days: number, hours: number) => {
    const newDate = addTimeToDate(months, days, hours)
    form.setValue('expired_time', newDate)
  }

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const quotaLabel = t('Quota ({{currency}})', { currency: currencyLabel })
  const quotaPlaceholder = tokensOnly
    ? t('Enter quota in tokens')
    : t('Enter quota in {{currency}}', { currency: currencyLabel })
  const benefitType = form.watch('benefit_type')
  const selectedPlanId = form.watch('plan_id')
  const selectedPlan = plans.find((item) => item.plan.id === selectedPlanId)

  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v)
        if (!v) {
          form.reset()
        }
      }}
    >
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[600px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isUpdate
              ? t('Update Redemption Code')
              : t('Create Redemption Code')}
          </SheetTitle>
          <SheetDescription>
            {isUpdate
              ? t('Update the redemption code by providing necessary info.')
              : t(
                  'Add new redemption code(s) by providing necessary info.'
                )}{' '}
            {t('Click save when you&apos;re done.')}
          </SheetDescription>
        </SheetHeader>
        <Form {...form}>
          <form
            id='redemption-form'
            onSubmit={handleSubmit}
            className={sideDrawerFormClassName()}
          >
            <SideDrawerSection>
              <FormField
                control={form.control}
                name='name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Name')}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder={t('Enter a name')} />
                    </FormControl>
                    <FormDescription>
                      {t('Name for this redemption code (1-20 characters)')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='benefit_type'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Redemption Benefit')}</FormLabel>
                    <FormControl>
                      <RadioGroup
                        value={field.value}
                        onValueChange={(value) => {
                          const next = value as 'quota' | 'subscription'
                          field.onChange(next)
                          if (next === 'quota') {
                            form.setValue('plan_id', 0, {
                              shouldValidate: true,
                            })
                          }
                        }}
                        className='grid gap-3 sm:grid-cols-2'
                      >
                        <Label
                          htmlFor='benefit-quota'
                          className='hover:border-primary/40 has-data-[checked]:border-primary has-data-[checked]:ring-primary/15 flex cursor-pointer items-start gap-3 rounded-xl border p-3.5 font-normal transition-all has-data-[checked]:ring-2'
                        >
                          <RadioGroupItem
                            id='benefit-quota'
                            value='quota'
                            className='mt-0.5'
                          />
                          <div className='min-w-0'>
                            <div className='flex items-center gap-2 font-medium'>
                              <CircleDollarSign className='text-primary size-4' />
                              {t('Wallet Quota')}
                            </div>
                            <p className='text-muted-foreground mt-1 text-xs leading-5'>
                              {t('Add a fixed quota amount to the user wallet')}
                            </p>
                          </div>
                        </Label>
                        <Label
                          htmlFor='benefit-subscription'
                          className='hover:border-primary/40 has-data-[checked]:border-primary has-data-[checked]:ring-primary/15 flex cursor-pointer items-start gap-3 rounded-xl border p-3.5 font-normal transition-all has-data-[checked]:ring-2'
                        >
                          <RadioGroupItem
                            id='benefit-subscription'
                            value='subscription'
                            className='mt-0.5'
                          />
                          <div className='min-w-0'>
                            <div className='flex items-center gap-2 font-medium'>
                              <Crown className='text-primary size-4' />
                              {t('Subscription Plan')}
                            </div>
                            <p className='text-muted-foreground mt-1 text-xs leading-5'>
                              {t('Grant one subscription plan entitlement')}
                            </p>
                          </div>
                        </Label>
                      </RadioGroup>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {benefitType === 'quota' ? (
                <FormField
                  control={form.control}
                  name='quota_dollars'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{quotaLabel}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min={tokensOnly ? 1 : 0.01}
                          step={tokensOnly ? 1 : 0.01}
                          placeholder={quotaPlaceholder}
                          onChange={(e) =>
                            field.onChange(
                              Number.parseFloat(e.target.value) || 0
                            )
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {tokensOnly
                          ? t('Enter the quota amount in tokens')
                          : t('Enter the quota amount in {{currency}}', {
                              currency: currencyLabel,
                            })}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ) : (
                <FormField
                  control={form.control}
                  name='plan_id'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Subscription Plan')}</FormLabel>
                      <Select
                        value={field.value > 0 ? String(field.value) : null}
                        onValueChange={(value) => {
                          if (typeof value === 'string') {
                            field.onChange(Number(value))
                          }
                        }}
                        disabled={plansLoading}
                      >
                        <FormControl>
                          <SelectTrigger className='w-full'>
                            <SelectValue
                              placeholder={
                                plansLoading
                                  ? t('Loading plans...')
                                  : t('Select a subscription plan')
                              }
                            />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            {plans.map(({ plan }) => (
                              <SelectItem key={plan.id} value={String(plan.id)}>
                                <span className='flex items-center gap-2'>
                                  <span>{plan.title}</span>
                                  {!plan.enabled && (
                                    <span className='text-muted-foreground text-xs'>
                                      ({t('Disabled')})
                                    </span>
                                  )}
                                </span>
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      {selectedPlan && (
                        <div className='bg-muted/35 grid grid-cols-2 gap-x-4 gap-y-2 rounded-lg border px-3 py-2.5 text-xs'>
                          <div>
                            <span className='text-muted-foreground'>
                              {t('Duration')}
                            </span>
                            <div className='mt-0.5 font-medium'>
                              {formatDuration(selectedPlan.plan, t)}
                            </div>
                          </div>
                          <div>
                            <span className='text-muted-foreground'>
                              {t('Quota')}
                            </span>
                            <div className='mt-0.5 font-medium'>
                              {selectedPlan.plan.total_amount > 0
                                ? formatQuota(selectedPlan.plan.total_amount)
                                : t('Unlimited')}
                            </div>
                          </div>
                          <div className='col-span-2'>
                            <span className='text-muted-foreground'>
                              {t('Reset Period')}
                            </span>
                            <div className='mt-0.5 font-medium'>
                              {formatResetPeriod(selectedPlan.plan, t)}
                            </div>
                          </div>
                        </div>
                      )}
                      <FormDescription>
                        {t(
                          'The code grants the latest configuration of this plan when redeemed.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}

              {benefitType === 'subscription' && (
                <div className='bg-muted/30 text-muted-foreground flex gap-2 rounded-lg border px-3 py-2.5 text-xs leading-5'>
                  <Info className='mt-0.5 size-3.5 shrink-0' />
                  <span>
                    {t(
                      'Changing the plan later also changes the benefit of every unused code linked to it.'
                    )}
                  </span>
                </div>
              )}

              <FormField
                control={form.control}
                name='expired_time'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Expiration Time')}</FormLabel>
                    <div className='flex flex-col gap-2'>
                      <FormControl>
                        <DateTimePicker
                          value={field.value}
                          onChange={field.onChange}
                          placeholder={t('Never expires')}
                        />
                      </FormControl>
                      <div className='grid grid-cols-4 gap-1.5 sm:flex sm:gap-2'>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(0, 0, 0)}
                        >
                          {t('Never')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(1, 0, 0)}
                        >
                          {t('1M')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(0, 7, 0)}
                        >
                          {t('1W')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(0, 1, 0)}
                        >
                          {t('1 Day')}
                        </Button>
                      </div>
                    </div>
                    <FormDescription>
                      {t('Leave empty for never expires')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {!isUpdate && (
                <FormField
                  control={form.control}
                  name='count'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Quantity')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min='1'
                          max='100'
                          placeholder={t('Number of codes to create')}
                          onChange={(e) =>
                            field.onChange(
                              Number.parseInt(e.target.value, 10) || 1
                            )
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Create multiple redemption codes at once (1-100)')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}
            </SideDrawerSection>
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button form='redemption-form' type='submit' disabled={isSubmitting}>
            {isSubmitting ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
