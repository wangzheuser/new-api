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
import { Gift, Plus, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DateTimePicker } from '@/components/datetime-picker'
import {
  SideDrawerSection,
  SideDrawerSectionHeader,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
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
import { formatTimestamp } from '@/features/subscriptions/lib'

import { getGroups, getUserGroupGrants, replaceUserGroupGrants } from '../api'
import type { UserGroupGrantData } from '../types'

type EditableGrant = {
  group: string
  expiresAt?: Date
}

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
  user: { id: number; username?: string } | null
  onSuccess?: () => void
}

/** Manages the administrator-owned group benefits for one user. */
export function UserGroupGrantsSheet(props: Props) {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [data, setData] = useState<UserGroupGrantData | null>(null)
  const [groupOptions, setGroupOptions] = useState<string[]>([])
  const [grants, setGrants] = useState<EditableGrant[]>([])

  const loadData = useCallback(async () => {
    if (!props.user?.id) return
    setLoading(true)
    try {
      const [grantResponse, groupResponse] = await Promise.all([
        getUserGroupGrants(props.user.id),
        getGroups(),
      ])
      if (!grantResponse.success || !grantResponse.data) {
        toast.error(grantResponse.message || t('Loading failed'))
        return
      }
      setData(grantResponse.data)
      setGroupOptions(groupResponse.success ? groupResponse.data || [] : [])
      setGrants(
        grantResponse.data.manual_grants.map((grant) => ({
          group: grant.group,
          expiresAt:
            grant.expires_at > 0
              ? new Date(grant.expires_at * 1000)
              : undefined,
        }))
      )
    } catch {
      toast.error(t('Loading failed'))
    } finally {
      setLoading(false)
    }
  }, [props.user?.id, t])

  useEffect(() => {
    if (props.open) void loadData()
  }, [props.open, loadData])

  const selectableGroups = useMemo(
    () => groupOptions.filter((group) => group !== data?.base_group),
    [data?.base_group, groupOptions]
  )

  /** Adds the first group not already present in the editable list. */
  const addGrant = () => {
    const used = new Set(grants.map((grant) => grant.group))
    const group = selectableGroups.find((candidate) => !used.has(candidate))
    if (!group) {
      toast.info(t('All available groups have already been added'))
      return
    }
    setGrants((current) => [...current, { group }])
  }

  /** Validates and atomically replaces the manual grants. */
  const saveGrants = async () => {
    if (!props.user?.id) return
    const groups = grants.map((grant) => grant.group.trim())
    if (groups.some((group) => !group)) {
      toast.error(t('Please select a benefit group'))
      return
    }
    if (new Set(groups).size !== groups.length) {
      toast.error(t('Benefit groups cannot be duplicated'))
      return
    }
    if (
      grants.some(
        (grant) => grant.expiresAt && grant.expiresAt.getTime() <= Date.now()
      )
    ) {
      toast.error(t('Expiration time must be in the future'))
      return
    }

    setSaving(true)
    try {
      const response = await replaceUserGroupGrants(props.user.id, {
        grants: grants.map((grant) => ({
          group: grant.group,
          expires_at: grant.expiresAt
            ? Math.floor(grant.expiresAt.getTime() / 1000)
            : 0,
        })),
      })
      if (response.success && response.data) {
        toast.success(t('Benefit groups updated'))
        setData(response.data)
        setGrants(
          response.data.manual_grants.map((grant) => ({
            group: grant.group,
            expiresAt:
              grant.expires_at > 0
                ? new Date(grant.expires_at * 1000)
                : undefined,
          }))
        )
        props.onSuccess?.()
      } else {
        toast.error(response.message || t('Update failed'))
      }
    } catch {
      toast.error(t('Update failed'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-3xl')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('User Benefit Groups')}</SheetTitle>
          <SheetDescription>
            {props.user?.username || '-'} (ID: {props.user?.id || '-'})
          </SheetDescription>
        </SheetHeader>

        <div className={sideDrawerFormClassName()}>
          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Current access')}
              description={t(
                'The base group, system-inherited groups, and active benefits can all be selected when creating an API key.'
              )}
              icon={<Gift className='size-4' />}
              iconTone='info'
            />
            {loading ? (
              <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
            ) : (
              <div className='grid gap-3 sm:grid-cols-2'>
                <div className='border-border/60 rounded-lg border p-3'>
                  <div className='text-muted-foreground mb-2 text-xs'>
                    {t('Base Group')}
                  </div>
                  <GroupBadge group={data?.base_group} />
                </div>
                <div className='border-border/60 rounded-lg border p-3'>
                  <div className='text-muted-foreground mb-2 text-xs'>
                    {t('Effective Groups')}
                  </div>
                  <div className='flex flex-wrap gap-1.5'>
                    {(data?.effective_groups || []).map((group) => (
                      <GroupBadge key={group} group={group} />
                    ))}
                  </div>
                </div>
              </div>
            )}
          </SideDrawerSection>

          <SideDrawerSection>
            <div className='flex items-start justify-between gap-3'>
              <SideDrawerSectionHeader
                title={t('Manual Benefit Groups')}
                description={t(
                  'Manual benefits take effect immediately. Use no expiration for permanent access.'
                )}
              />
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={addGrant}
                disabled={loading}
              >
                <Plus className='mr-1 size-4' />
                {t('Add group')}
              </Button>
            </div>

            {grants.length === 0 ? (
              <p className='text-muted-foreground rounded-lg border border-dashed py-8 text-center text-sm'>
                {t('No manually granted benefit groups')}
              </p>
            ) : (
              <div className='space-y-3'>
                {grants.map((grant, index) => {
                  const active =
                    !grant.expiresAt || grant.expiresAt.getTime() > Date.now()
                  const options = selectableGroups.filter(
                    (group) =>
                      group === grant.group ||
                      !grants.some(
                        (candidate, candidateIndex) =>
                          candidateIndex !== index && candidate.group === group
                      )
                  )
                  if (grant.group && !options.includes(grant.group)) {
                    options.unshift(grant.group)
                  }

                  return (
                    <div
                      key={grant.group}
                      className='border-border/60 grid gap-3 rounded-lg border p-3 sm:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)_auto] sm:items-end'
                    >
                      <div className='space-y-1.5'>
                        <div className='flex items-center justify-between gap-2 text-xs font-medium'>
                          <span>{t('Benefit Group')}</span>
                          <StatusBadge
                            label={active ? t('Active') : t('Expired')}
                            variant={active ? 'success' : 'neutral'}
                            copyable={false}
                          />
                        </div>
                        <Select
                          items={options.map((group) => ({
                            value: group,
                            label: group,
                          }))}
                          value={grant.group}
                          onValueChange={(value) => {
                            if (value === null) return
                            setGrants((current) =>
                              current.map((item, itemIndex) =>
                                itemIndex === index
                                  ? { ...item, group: value }
                                  : item
                              )
                            )
                          }}
                        >
                          <SelectTrigger>
                            <SelectValue
                              placeholder={t('Select a benefit group')}
                            />
                          </SelectTrigger>
                          <SelectContent alignItemWithTrigger={false}>
                            <SelectGroup>
                              {options.map((group) => (
                                <SelectItem key={group} value={group}>
                                  {group}
                                </SelectItem>
                              ))}
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                      </div>
                      <div className='space-y-1.5'>
                        <div className='text-xs font-medium'>
                          {t('Expiration Time')}
                        </div>
                        <div className='flex gap-2'>
                          <DateTimePicker
                            value={grant.expiresAt}
                            onChange={(expiresAt) =>
                              setGrants((current) =>
                                current.map((item, itemIndex) =>
                                  itemIndex === index
                                    ? { ...item, expiresAt }
                                    : item
                                )
                              )
                            }
                            placeholder={t('Permanent')}
                            className='min-w-0 flex-1'
                          />
                          <Button
                            type='button'
                            variant='outline'
                            onClick={() =>
                              setGrants((current) =>
                                current.map((item, itemIndex) =>
                                  itemIndex === index
                                    ? { ...item, expiresAt: undefined }
                                    : item
                                )
                              )
                            }
                          >
                            {t('Permanent')}
                          </Button>
                        </div>
                      </div>
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        aria-label={t('Delete')}
                        onClick={() =>
                          setGrants((current) =>
                            current.filter(
                              (_, itemIndex) => itemIndex !== index
                            )
                          )
                        }
                      >
                        <Trash2 className='size-4' />
                      </Button>
                    </div>
                  )
                })}
              </div>
            )}
          </SideDrawerSection>

          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Subscription Benefit Groups')}
              description={t(
                'These read-only benefits follow each subscription snapshot and expire with that subscription.'
              )}
            />
            {(data?.subscription_grants || []).length === 0 ? (
              <p className='text-muted-foreground rounded-lg border border-dashed py-8 text-center text-sm'>
                {t('No active subscription benefit groups')}
              </p>
            ) : (
              <div className='space-y-2'>
                {(data?.subscription_grants || []).map((subscription) => (
                  <div
                    key={subscription.subscription_id}
                    className='border-border/60 rounded-lg border p-3'
                  >
                    <div className='mb-2 flex items-center justify-between gap-3 text-sm'>
                      <span className='font-medium'>
                        {t('Plan')} #{subscription.plan_id}
                      </span>
                      <span className='text-muted-foreground text-xs'>
                        {formatTimestamp(subscription.start_time)} –{' '}
                        {formatTimestamp(subscription.end_time)}
                      </span>
                    </div>
                    <div className='flex flex-wrap gap-1.5'>
                      {subscription.groups.map((group) => (
                        <GroupBadge key={group} group={group} />
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </SideDrawerSection>
        </div>

        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button onClick={saveGrants} disabled={loading || saving}>
            {saving ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
