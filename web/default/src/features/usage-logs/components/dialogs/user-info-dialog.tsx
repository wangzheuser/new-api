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
import type { TFunction } from 'i18next'
import { AlertCircle, RotateCcw } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { getBillingPreferenceLabel } from '@/features/subscriptions/lib'
import { USER_ROLES, USER_STATUSES } from '@/features/users/constants'
import { formatCompactNumber, formatQuota, formatTimestamp } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getAdminUserOverview } from '../../api'
import type { AdminUserOverview, UserSubscriptionOverview } from '../../types'
import { useUsageLogsContext } from '../usage-logs-provider'

const MASKED_VALUE = '••••'

/** Returns an empty placeholder or a privacy-safe text value. */
function getSensitiveText(value: string, sensitiveVisible: boolean): string {
  if (!value) return '-'
  return sensitiveVisible ? value : MASKED_VALUE
}

interface UserInfoDialogProps {
  userId: number | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface InfoItemProps {
  label: string
  value: ReactNode
  className?: string
}

/** Renders a compact labeled value in the account overview. */
function InfoItem(props: InfoItemProps) {
  return (
    <div className={cn('min-w-0 space-y-1.5', props.className)}>
      <div className='text-muted-foreground text-xs'>{props.label}</div>
      <div className='min-w-0 text-sm font-medium break-words'>
        {props.value}
      </div>
    </div>
  )
}

/** Renders one high-frequency metric without exposing masked values. */
function MetricCard(props: InfoItemProps) {
  return (
    <div className='border-border/60 bg-muted/20 min-w-0 rounded-lg border p-3'>
      <InfoItem {...props} />
    </div>
  )
}

/** Maps persisted subscription sources to administrator-facing labels. */
function getSubscriptionSourceLabel(source: string, t: TFunction): string {
  switch (source) {
    case 'order':
      return t('Order Purchase')
    case 'balance':
      return t('Balance Purchase')
    case 'redemption':
      return t('Redemption Code')
    case 'admin':
      return t('Admin Grant')
    default:
      return source || '-'
  }
}

/** Renders group badges while respecting the log page privacy switch. */
function BenefitGroups(props: { groups: string[]; sensitiveVisible: boolean }) {
  if (props.groups.length === 0) {
    return <span className='text-muted-foreground'>-</span>
  }
  if (!props.sensitiveVisible) {
    return (
      <StatusBadge label={MASKED_VALUE} variant='neutral' copyable={false} />
    )
  }
  return (
    <div className='flex flex-wrap gap-1.5'>
      {props.groups.map((group) => (
        <GroupBadge key={group} group={group} />
      ))}
    </div>
  )
}

/** Renders the operational snapshot for one active or scheduled subscription. */
function SubscriptionCard(props: {
  subscription: UserSubscriptionOverview
  scheduled: boolean
  sensitiveVisible: boolean
  t: TFunction
}) {
  const subscription = props.subscription
  const total = Number(subscription.amount_total || 0)
  const used = Number(subscription.amount_used || 0)
  const percentage =
    total > 0 ? Math.min(100, Math.max(0, (used / total) * 100)) : 0
  let quotaDisplay = MASKED_VALUE
  if (props.sensitiveVisible) {
    quotaDisplay =
      total > 0
        ? `${formatQuota(used)} / ${formatQuota(total)}`
        : props.t('Unlimited')
  }

  return (
    <div className='border-border/60 space-y-3 rounded-lg border p-3 sm:p-4'>
      <div className='flex flex-wrap items-start justify-between gap-2'>
        <div className='min-w-0'>
          <div className='font-semibold break-words'>
            {subscription.plan_title || `#${subscription.plan_id}`}
          </div>
          <div className='text-muted-foreground mt-1 text-xs'>
            {props.t('Source')}:{' '}
            {getSubscriptionSourceLabel(subscription.source, props.t)} ·{' '}
            {props.t('Allocation count')}:{' '}
            {Math.max(1, subscription.allocation_count || 1)}
          </div>
        </div>
        <div className='flex flex-wrap gap-1.5'>
          <StatusBadge
            label={props.scheduled ? props.t('Scheduled') : props.t('Active')}
            variant={props.scheduled ? 'info' : 'success'}
            copyable={false}
          />
          {!subscription.allow_wallet_overflow && (
            <StatusBadge
              label={props.t('Subscription Quota Only')}
              variant='warning'
              copyable={false}
            />
          )}
        </div>
      </div>

      <div className='grid gap-3 text-sm sm:grid-cols-2'>
        <InfoItem
          label={props.scheduled ? props.t('Expected Start') : props.t('Start')}
          value={formatTimestamp(subscription.start_time)}
        />
        <InfoItem
          label={props.scheduled ? props.t('Expected End') : props.t('End')}
          value={formatTimestamp(subscription.end_time)}
        />
      </div>

      <div className='space-y-1.5'>
        <div className='flex items-center justify-between gap-3 text-xs'>
          <span className='text-muted-foreground'>
            {props.t('Subscription Quota')}
          </span>
          <span className='font-medium tabular-nums'>{quotaDisplay}</span>
        </div>
        {props.sensitiveVisible && total > 0 && (
          <Progress value={percentage} className='h-1.5' />
        )}
      </div>

      <div className='grid gap-3 sm:grid-cols-2'>
        <InfoItem
          label={props.t('Benefit Groups')}
          value={
            <BenefitGroups
              groups={subscription.benefit_groups || []}
              sensitiveVisible={props.sensitiveVisible}
            />
          }
        />
        {subscription.next_reset_time > 0 && (
          <InfoItem
            label={props.t('Next Reset')}
            value={formatTimestamp(subscription.next_reset_time)}
          />
        )}
      </div>
    </div>
  )
}

/** Renders a loading layout that matches the final overview structure. */
function OverviewSkeleton(props: { label: string }) {
  return (
    <div className='space-y-5 py-2' aria-busy='true' aria-label={props.label}>
      <Skeleton className='h-20 w-full rounded-lg' />
      <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
        {Array.from({ length: 4 }, (_, index) => (
          <Skeleton key={index} className='h-20 rounded-lg' />
        ))}
      </div>
      <Skeleton className='h-40 w-full rounded-lg' />
    </div>
  )
}

/** Returns whether the invitation section contains meaningful information. */
function hasInvitationDetails(overview: AdminUserOverview): boolean {
  const invitation = overview.invitation
  return Boolean(
    invitation.code ||
    invitation.inviter_id ||
    invitation.invited_count ||
    invitation.available_quota ||
    invitation.history_quota
  )
}

/** Displays a read-only administrator overview for a user selected from usage logs. */
export function UserInfoDialog(props: UserInfoDialogProps) {
  const { t } = useTranslation()
  const { sensitiveVisible } = useUsageLogsContext()
  const queryEnabled =
    props.open && typeof props.userId === 'number' && props.userId > 0
  const userOverviewQuery = useQuery({
    queryKey: ['usage-logs', 'user-overview', props.userId],
    enabled: queryEnabled,
    staleTime: 0,
    queryFn: async () => {
      if (typeof props.userId !== 'number' || props.userId <= 0) {
        throw new Error(t('No user information available'))
      }
      const result = await getAdminUserOverview(props.userId)
      if (!result.success || !result.data) {
        throw new Error(result.message || t('Failed to fetch user information'))
      }
      return result.data
    },
  })
  const overview = userOverviewQuery.data
  const statusConfig = overview
    ? USER_STATUSES[overview.status as keyof typeof USER_STATUSES]
    : undefined
  const roleConfig = overview
    ? USER_ROLES[overview.role as keyof typeof USER_ROLES]
    : undefined
  const hasSubscriptions = Boolean(
    overview &&
    (overview.active_subscriptions.length > 0 ||
      overview.scheduled_subscriptions.length > 0)
  )

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('User Information')}
      description={t(
        'Review this user account, usage, access groups, and current subscription status.'
      )}
      contentClassName='sm:max-w-3xl'
      contentHeight='auto'
      bodyClassName='space-y-5'
    >
      {queryEnabled && userOverviewQuery.isPending && (
        <OverviewSkeleton label={t('Loading...')} />
      )}
      {queryEnabled && userOverviewQuery.isError && (
        <div
          className='flex flex-col items-center justify-center gap-3 py-10 text-center'
          role='alert'
        >
          <AlertCircle className='text-destructive size-7' />
          <div>
            <div className='font-medium'>
              {t('Failed to fetch user information')}
            </div>
            <div className='text-muted-foreground mt-1 text-sm'>
              {userOverviewQuery.error instanceof Error
                ? userOverviewQuery.error.message
                : t('Request failed')}
            </div>
          </div>
          <Button
            variant='outline'
            size='sm'
            onClick={() => userOverviewQuery.refetch()}
          >
            <RotateCcw className='mr-1 size-4' />
            {t('Retry')}
          </Button>
        </div>
      )}
      {queryEnabled &&
        !userOverviewQuery.isPending &&
        !userOverviewQuery.isError &&
        overview && (
          <div className='space-y-5 py-1'>
            <section className='border-border/60 bg-muted/20 rounded-lg border p-4'>
              <div className='flex flex-wrap items-start justify-between gap-3'>
                <div className='min-w-0'>
                  <div className='flex flex-wrap items-center gap-2'>
                    <span className='text-base font-semibold break-all'>
                      {sensitiveVisible ? overview.username : MASKED_VALUE}
                    </span>
                    <span className='text-muted-foreground text-xs'>
                      {t('ID')}:
                    </span>
                    <TableId value={overview.id} />
                  </div>
                  {overview.display_name &&
                    overview.display_name !== overview.username && (
                      <div className='text-muted-foreground mt-1 text-sm break-all'>
                        {sensitiveVisible
                          ? overview.display_name
                          : MASKED_VALUE}
                      </div>
                    )}
                </div>
                <div className='flex flex-wrap gap-1.5'>
                  <StatusBadge
                    label={
                      statusConfig
                        ? t(statusConfig.labelKey)
                        : String(overview.status)
                    }
                    variant={statusConfig?.variant || 'neutral'}
                    copyable={false}
                  />
                  <StatusBadge
                    label={
                      roleConfig
                        ? t(roleConfig.labelKey)
                        : String(overview.role)
                    }
                    variant='neutral'
                    copyable={false}
                  />
                </div>
              </div>
            </section>

            <section className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
              <MetricCard
                label={t('Balance')}
                value={
                  sensitiveVisible ? formatQuota(overview.quota) : MASKED_VALUE
                }
              />
              <MetricCard
                label={t('Used Quota')}
                value={
                  sensitiveVisible
                    ? formatQuota(overview.used_quota)
                    : MASKED_VALUE
                }
              />
              <MetricCard
                label={t('Request Count')}
                value={formatCompactNumber(overview.request_count)}
              />
              <MetricCard
                label={t('Billing Preference')}
                value={getBillingPreferenceLabel(
                  overview.billing_preference,
                  t
                )}
              />
            </section>

            {!hasSubscriptions && (
              <section className='space-y-3'>
                <h3 className='text-sm font-semibold'>{t('Subscriptions')}</h3>
                <div className='text-muted-foreground rounded-lg border border-dashed py-8 text-center text-sm'>
                  {t('No current or scheduled subscriptions')}
                </div>
              </section>
            )}

            {hasSubscriptions && (
              <>
                <section className='space-y-3'>
                  <div className='flex items-center justify-between gap-3'>
                    <h3 className='text-sm font-semibold'>
                      {t('Current Subscriptions')}
                    </h3>
                    <StatusBadge
                      label={String(overview.active_subscriptions.length)}
                      variant='success'
                      copyable={false}
                    />
                  </div>
                  {overview.active_subscriptions.length > 0 ? (
                    <div className='space-y-3'>
                      {overview.active_subscriptions.map((subscription) => (
                        <SubscriptionCard
                          key={subscription.id}
                          subscription={subscription}
                          scheduled={false}
                          sensitiveVisible={sensitiveVisible}
                          t={t}
                        />
                      ))}
                    </div>
                  ) : (
                    <div className='text-muted-foreground rounded-lg border border-dashed py-6 text-center text-sm'>
                      {t('No active subscriptions')}
                    </div>
                  )}
                </section>

                <section className='space-y-3'>
                  <div className='flex items-center justify-between gap-3'>
                    <h3 className='text-sm font-semibold'>
                      {t('Scheduled Subscriptions')}
                    </h3>
                    <StatusBadge
                      label={String(overview.scheduled_subscriptions.length)}
                      variant='info'
                      copyable={false}
                    />
                  </div>
                  {overview.scheduled_subscriptions.length > 0 ? (
                    <div className='space-y-3'>
                      {overview.scheduled_subscriptions.map((subscription) => (
                        <SubscriptionCard
                          key={subscription.id}
                          subscription={subscription}
                          scheduled
                          sensitiveVisible={sensitiveVisible}
                          t={t}
                        />
                      ))}
                    </div>
                  ) : (
                    <div className='text-muted-foreground rounded-lg border border-dashed py-6 text-center text-sm'>
                      {t('No scheduled subscriptions')}
                    </div>
                  )}
                </section>
              </>
            )}

            <section className='border-border/60 space-y-4 rounded-lg border p-4'>
              <h3 className='text-sm font-semibold'>{t('Account Details')}</h3>
              <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
                <InfoItem
                  label={t('Email')}
                  value={getSensitiveText(overview.email, sensitiveVisible)}
                />
                <InfoItem
                  label={t('Created At')}
                  value={formatTimestamp(overview.created_at)}
                />
                <InfoItem
                  label={t('Last Login')}
                  value={formatTimestamp(overview.last_login_at)}
                />
                <InfoItem
                  label={t('Base Group')}
                  value={
                    <BenefitGroups
                      groups={overview.base_group ? [overview.base_group] : []}
                      sensitiveVisible={sensitiveVisible}
                    />
                  }
                />
                <InfoItem
                  label={t('Effective Groups')}
                  className='sm:col-span-2'
                  value={
                    <BenefitGroups
                      groups={overview.effective_groups}
                      sensitiveVisible={sensitiveVisible}
                    />
                  }
                />
              </div>
              {overview.remark && (
                <InfoItem label={t('Remark')} value={overview.remark} />
              )}
            </section>

            {hasInvitationDetails(overview) && (
              <section className='border-border/60 space-y-4 rounded-lg border p-4'>
                <h3 className='text-sm font-semibold'>
                  {t('Invitation Summary')}
                </h3>
                <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
                  <InfoItem
                    label={t('Invitation Code')}
                    value={getSensitiveText(
                      overview.invitation.code,
                      sensitiveVisible
                    )}
                  />
                  <InfoItem
                    label={t('Inviter')}
                    value={overview.invitation.inviter_id || t('No Inviter')}
                  />
                  <InfoItem
                    label={t('Invited Users')}
                    value={formatCompactNumber(
                      overview.invitation.invited_count
                    )}
                  />
                  <InfoItem
                    label={t('Invitation Quota')}
                    value={
                      sensitiveVisible
                        ? formatQuota(overview.invitation.available_quota)
                        : MASKED_VALUE
                    }
                  />
                  <InfoItem
                    label={t('Total invitation revenue')}
                    value={
                      sensitiveVisible
                        ? formatQuota(overview.invitation.history_quota)
                        : MASKED_VALUE
                    }
                  />
                </div>
              </section>
            )}
          </div>
        )}
      {(!queryEnabled ||
        (!userOverviewQuery.isPending &&
          !userOverviewQuery.isError &&
          !overview)) && (
        <div className='text-muted-foreground py-8 text-center text-sm'>
          {t('No user information available')}
        </div>
      )}
    </Dialog>
  )
}
