import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery } from '@tanstack/react-query'
import { AlertTriangle, Search, ShieldAlert, UserRound } from 'lucide-react'
import { useDeferredValue, useEffect, useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import {
  createUserRateLimitRule,
  getUserGroups,
  searchUsers,
  updateUserRateLimitRule,
} from '../api'
import { userRateLimitRuleSchema, type UserRateLimitRuleForm } from '../schemas'
import type {
  RateLimitResponse,
  UserRateLimitConfig,
  UserRateLimitRule,
  UserSummary,
} from '../types'

type RuleDrawerProps = {
  open: boolean
  rule: UserRateLimitRule | null
  config: UserRateLimitConfig
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}

export function RuleDrawer(props: RuleDrawerProps) {
  const { t } = useTranslation()
  const [userSearch, setUserSearch] = useState('')
  const [selectedUser, setSelectedUser] = useState<UserSummary | null>(null)
  const deferredUserSearch = useDeferredValue(userSearch)
  const form = useForm<UserRateLimitRuleForm>({
    resolver: zodResolver(userRateLimitRuleSchema),
    defaultValues: createRuleFormValues(props.rule),
  })
  const watched = useWatch({ control: form.control })
  const userId = watched.userId ?? 0
  const usersQuery = useQuery({
    queryKey: ['user-rate-limit-user-search', deferredUserSearch],
    queryFn: () => searchUsers(deferredUserSearch.trim()),
    enabled:
      props.open &&
      props.rule === null &&
      selectedUser === null &&
      deferredUserSearch.trim().length > 0,
  })
  const groupsQuery = useQuery({
    queryKey: ['user-rate-limit-user-groups', userId],
    queryFn: () => getUserGroups(userId),
    enabled: props.open && userId > 0,
  })
  const mutation = useMutation({
    mutationFn: (values: UserRateLimitRuleForm) => {
      const payload = {
        user_id: values.userId,
        group: values.group.trim(),
        total_count: values.totalCount,
        success_count: values.successCount,
        response: values.hasResponseOverride
          ? {
              status_code: values.statusCode,
              error_message: values.errorMessage.trim(),
            }
          : null,
      }
      if (props.rule) {
        return updateUserRateLimitRule(props.rule.id, payload)
      }
      return createUserRateLimitRule(payload)
    },
    onSuccess: () => {
      toast.success(
        props.rule
          ? t('Independent rule updated')
          : t('Independent rule created')
      )
      props.onSaved()
      props.onOpenChange(false)
    },
    onError: (error) => toast.error(error.message),
  })

  useEffect(() => {
    const values = createRuleFormValues(props.rule)
    form.reset(values)
    setSelectedUser(props.rule?.user ?? null)
    setUserSearch('')
  }, [form, props.open, props.rule])

  const groupOptions = useMemo(() => {
    const values = new Set(['auto', ...Object.keys(groupsQuery.data ?? {})])
    if (props.rule?.group) values.add(props.rule.group)
    return [...values].sort()
  }, [groupsQuery.data, props.rule?.group])
  const currentGroup = watched.group ?? ''
  const currentGroupUnavailable =
    Boolean(props.rule && currentGroup === props.rule.group) &&
    groupsQuery.isSuccess &&
    currentGroup !== 'auto' &&
    !Object.hasOwn(groupsQuery.data, currentGroup)
  const effectiveResponse = resolvePreviewResponse(
    props.config,
    currentGroup,
    Boolean(watched.hasResponseOverride),
    watched.statusCode ?? 429,
    watched.errorMessage ?? ''
  )
  const userOverrideStatus = watched.statusCode ?? 429
  const hasRiskyUserOverride =
    Boolean(watched.hasResponseOverride) &&
    (userOverrideStatus === 401 ||
      userOverrideStatus === 403 ||
      userOverrideStatus >= 500)

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-2xl')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {props.rule
              ? t('Edit independent rule')
              : t('Create independent rule')}
          </SheetTitle>
          <SheetDescription>
            {t(
              'The rule replaces the existing count limits only for the selected user and runtime group bucket.'
            )}
          </SheetDescription>
        </SheetHeader>

        <Form {...form}>
          <form
            id='user-rate-limit-rule-form'
            className={sideDrawerFormClassName()}
            onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
          >
            <section className='space-y-4'>
              <div>
                <h3 className='font-semibold'>{t('Target user and group')}</h3>
                <p className='text-muted-foreground text-sm'>
                  {t('Choose the exact account and group bucket to isolate.')}
                </p>
              </div>

              {selectedUser ? (
                <div className='bg-muted/30 flex items-center justify-between gap-3 rounded-xl border p-3'>
                  <div className='flex min-w-0 items-center gap-3'>
                    <span className='flex size-9 shrink-0 items-center justify-center rounded-lg bg-sky-500/10 text-sky-700 dark:text-sky-300'>
                      <UserRound className='size-4' />
                    </span>
                    <div className='min-w-0'>
                      <p className='truncate font-medium'>
                        {selectedUser.display_name || selectedUser.username}
                      </p>
                      <p className='text-muted-foreground truncate text-xs'>
                        #{selectedUser.id} · {selectedUser.username} ·{' '}
                        {selectedUser.email || t('No email')}
                      </p>
                    </div>
                  </div>
                  {!props.rule && (
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      onClick={() => {
                        setSelectedUser(null)
                        form.setValue('userId', 0)
                        form.setValue('group', '')
                      }}
                    >
                      {t('Change')}
                    </Button>
                  )}
                </div>
              ) : (
                <div className='space-y-2'>
                  <FormLabel>{t('Search target user')}</FormLabel>
                  <div className='relative'>
                    <Search className='text-muted-foreground absolute top-2.5 left-3 size-4' />
                    <Input
                      className='pl-9'
                      value={userSearch}
                      onChange={(event) => setUserSearch(event.target.value)}
                      placeholder={t(
                        'Search by user ID, username, display name, or email'
                      )}
                    />
                  </div>
                  {usersQuery.data && (
                    <div className='max-h-52 space-y-1 overflow-y-auto rounded-lg border p-1'>
                      {usersQuery.data.items.map((user) => (
                        <button
                          key={user.id}
                          type='button'
                          className='hover:bg-muted focus-visible:ring-ring flex w-full items-center justify-between rounded-md px-3 py-2 text-left text-sm outline-none focus-visible:ring-2'
                          onClick={() => {
                            setSelectedUser(user)
                            form.setValue('userId', user.id, {
                              shouldValidate: true,
                            })
                            form.setValue('group', '')
                          }}
                        >
                          <span className='min-w-0 truncate'>
                            {user.display_name || user.username}
                            <span className='text-muted-foreground'>
                              {' '}
                              · {user.username}
                            </span>
                          </span>
                          <span className='text-muted-foreground ml-3 shrink-0 font-mono text-xs'>
                            #{user.id}
                          </span>
                        </button>
                      ))}
                      {usersQuery.data.items.length === 0 && (
                        <p className='text-muted-foreground p-3 text-center text-sm'>
                          {t('No users found')}
                        </p>
                      )}
                    </div>
                  )}
                </div>
              )}

              <FormField
                control={form.control}
                name='group'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Runtime group bucket')}</FormLabel>
                    <FormControl>
                      <NativeSelect
                        className='w-full'
                        disabled={!selectedUser || groupsQuery.isLoading}
                        value={field.value}
                        onChange={field.onChange}
                      >
                        <NativeSelectOption value=''>
                          {groupsQuery.isLoading
                            ? t('Loading groups...')
                            : t('Select a group')}
                        </NativeSelectOption>
                        {groupOptions.map((group) => (
                          <NativeSelectOption key={group} value={group}>
                            {group}
                          </NativeSelectOption>
                        ))}
                      </NativeSelect>
                    </FormControl>
                    <FormDescription>
                      {t(
                        'API keys using auto are counted in the auto bucket; Playground may use the selected group.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {currentGroupUnavailable && (
                <Alert>
                  <AlertTriangle />
                  <AlertTitle>
                    {t('Saved group is currently unavailable')}
                  </AlertTitle>
                  <AlertDescription>
                    {t(
                      'You may keep this group while editing other fields. Choose a currently available group to replace it.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
            </section>

            <section className='space-y-4 border-t pt-6'>
              <div>
                <h3 className='font-semibold'>
                  {t('Independent count limits')}
                </h3>
                <p className='text-muted-foreground text-sm'>
                  {t('Both limits use the shared period shown on the page.')}
                </p>
              </div>
              <div className='grid gap-4 sm:grid-cols-2'>
                <FormField
                  control={form.control}
                  name='totalCount'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Total requests')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={0}
                          max={100000000}
                          value={field.value}
                          onChange={(event) =>
                            field.onChange(Number(event.target.value))
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t('0 means unlimited total requests')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='successCount'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Successful requests')}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          max={100000000}
                          value={field.value}
                          onChange={(event) =>
                            field.onChange(Number(event.target.value))
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Counted after a response succeeds')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
              {watched.totalCount === 0 && (
                <Alert>
                  <ShieldAlert />
                  <AlertTitle>{t('No hard total-request cap')}</AlertTitle>
                  <AlertDescription>
                    {t(
                      'Failed requests remain unrestricted. Set a non-zero total limit for strict high-concurrency protection.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
            </section>

            <section className='space-y-4 border-t pt-6'>
              <div className='flex items-center justify-between gap-4 rounded-xl border p-3'>
                <div>
                  <FormLabel>{t('User response override')}</FormLabel>
                  <p className='text-muted-foreground text-sm'>
                    {t('Turn off to inherit the group or global response.')}
                  </p>
                </div>
                <FormField
                  control={form.control}
                  name='hasResponseOverride'
                  render={({ field }) => (
                    <FormItem>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />
              </div>

              {watched.hasResponseOverride && (
                <div className='grid gap-4 sm:grid-cols-[160px_minmax(0,1fr)]'>
                  <FormField
                    control={form.control}
                    name='statusCode'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('HTTP status')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={400}
                            max={599}
                            value={field.value}
                            onChange={(event) =>
                              field.onChange(Number(event.target.value))
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='errorMessage'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Error message')}</FormLabel>
                        <FormControl>
                          <Textarea rows={3} maxLength={512} {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>
              )}

              {hasRiskyUserOverride && (
                <Alert>
                  <AlertTriangle />
                  <AlertTitle>{t('Client behavior warning')}</AlertTitle>
                  <AlertDescription>
                    {(userOverrideStatus === 401 ||
                      userOverrideStatus === 403) &&
                      t(
                        'Some clients may treat this status as invalid credentials.'
                      )}{' '}
                    {userOverrideStatus >= 500 &&
                      t('Some clients may automatically retry this status.')}
                  </AlertDescription>
                </Alert>
              )}

              <div className='bg-muted/20 rounded-xl border p-3'>
                <div className='mb-2 flex items-center justify-between gap-3'>
                  <p className='text-sm font-medium'>
                    {t('Effective response preview')}
                  </p>
                  <Badge variant='outline'>
                    {t(responseSourceLabel(effectiveResponse.source))}
                  </Badge>
                </div>
                <p className='font-mono text-sm'>
                  {effectiveResponse.status_code}
                </p>
                <p className='text-muted-foreground mt-1 text-sm break-words'>
                  {effectiveResponse.error_message ||
                    t('Enter an error message')}
                </p>
                <p className='text-muted-foreground mt-2 text-xs'>
                  {t('The server appends the request ID to this message.')}
                </p>
              </div>
            </section>
          </form>
        </Form>

        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Cancel')}
          </SheetClose>
          <Button
            type='submit'
            form='user-rate-limit-rule-form'
            disabled={mutation.isPending}
          >
            {mutation.isPending ? t('Saving...') : t('Save rule')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function createRuleFormValues(
  rule: UserRateLimitRule | null
): UserRateLimitRuleForm {
  return {
    userId: rule?.user.id ?? 0,
    group: rule?.group ?? '',
    totalCount: rule?.total_count ?? 0,
    successCount: rule?.success_count ?? 100,
    hasResponseOverride:
      rule?.response !== null && rule?.response !== undefined,
    statusCode: rule?.response?.status_code ?? 429,
    errorMessage: rule?.response?.error_message ?? '',
  }
}

function resolvePreviewResponse(
  config: UserRateLimitConfig,
  group: string,
  hasUserOverride: boolean,
  statusCode: number,
  errorMessage: string
): RateLimitResponse & { source: 'global' | 'group' | 'user_group' } {
  if (hasUserOverride) {
    return {
      status_code: statusCode,
      error_message: errorMessage,
      source: 'user_group',
    }
  }
  const groupResponse = config.group_responses.find(
    (item) => item.group === group
  )
  if (groupResponse) return { ...groupResponse, source: 'group' }
  return { ...config.default_response, source: 'global' }
}

function responseSourceLabel(
  source: 'global' | 'group' | 'user_group'
): string {
  if (source === 'user_group') return 'User override'
  if (source === 'group') return 'Group override'
  return 'Global default'
}
