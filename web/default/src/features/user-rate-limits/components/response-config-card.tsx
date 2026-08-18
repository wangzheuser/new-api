import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery } from '@tanstack/react-query'
import { AlertTriangle, Plus, Save, Search, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useFieldArray, useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
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
import { Separator } from '@/components/ui/separator'
import { Textarea } from '@/components/ui/textarea'

import { getSystemGroups, updateUserRateLimitConfig } from '../api'
import {
  userRateLimitConfigSchema,
  type UserRateLimitConfigForm,
} from '../schemas'
import type { UserRateLimitConfig } from '../types'

type ResponseConfigCardProps = {
  config: UserRateLimitConfig
  onSaved: (config: UserRateLimitConfig) => void
}

export function ResponseConfigCard(props: ResponseConfigCardProps) {
  const { t } = useTranslation()
  const [filter, setFilter] = useState('')
  const form = useForm<UserRateLimitConfigForm>({
    resolver: zodResolver(userRateLimitConfigSchema),
    defaultValues: toFormValues(props.config),
  })
  const fieldArray = useFieldArray({
    control: form.control,
    name: 'groupResponses',
  })
  const watched = useWatch({ control: form.control })
  const groupsQuery = useQuery({
    queryKey: ['user-rate-limit-system-groups'],
    queryFn: getSystemGroups,
  })
  const mutation = useMutation({
    mutationFn: (values: UserRateLimitConfigForm) =>
      updateUserRateLimitConfig({
        delay_seconds: values.delaySeconds,
        default_response: {
          status_code: values.defaultResponse.statusCode,
          error_message: values.defaultResponse.errorMessage.trim(),
        },
        group_responses: values.groupResponses.map((item) => ({
          group: item.group.trim(),
          status_code: item.statusCode,
          error_message: item.errorMessage.trim(),
        })),
      }),
    onSuccess: (config) => {
      form.reset(toFormValues(config))
      props.onSaved(config)
      toast.success(t('Rate-limit response configuration saved'))
    },
    onError: (error) => toast.error(error.message),
  })

  useEffect(() => {
    form.reset(toFormValues(props.config))
  }, [form, props.config])

  const availableGroups = useMemo(() => {
    return [...new Set(['auto', ...(groupsQuery.data ?? [])])].sort()
  }, [groupsQuery.data])
  const filterText = filter.trim().toLowerCase()
  const riskyStatus = watched.defaultResponse?.statusCode ?? 429
  const delaySeconds = watched.delaySeconds ?? 0
  const configuredStatuses = [
    riskyStatus,
    ...(watched.groupResponses ?? []).map((item) => item?.statusCode ?? 429),
  ]
  const hasCredentialStatus = configuredStatuses.some(
    (status) => status === 401 || status === 403
  )
  const hasRetryableStatus = configuredStatuses.some((status) => status >= 500)

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Response and delay policy')}</CardTitle>
        <p className='text-muted-foreground text-sm'>
          {t(
            'User overrides fall back to group overrides, then to the required global response.'
          )}
        </p>
      </CardHeader>
      <CardContent>
        <Form {...form}>
          <form
            className='space-y-6'
            onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
          >
            <div className='grid items-start gap-4 lg:grid-cols-[180px_180px_minmax(0,1fr)]'>
              <FormField
                control={form.control}
                name='delaySeconds'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Return delay (seconds)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        max={60}
                        value={field.value}
                        onChange={(event) =>
                          field.onChange(Number(event.target.value))
                        }
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Shared by all independent rules')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='defaultResponse.statusCode'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Default HTTP status')}</FormLabel>
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
                name='defaultResponse.errorMessage'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Default error message')}</FormLabel>
                    <FormControl>
                      <Textarea rows={3} maxLength={512} {...field} />
                    </FormControl>
                    <FormDescription>
                      {t('The request ID is appended by the server.')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            {(delaySeconds > 0 ||
              hasCredentialStatus ||
              hasRetryableStatus) && (
              <Alert>
                <AlertTriangle />
                <AlertTitle>{t('Client behavior warning')}</AlertTitle>
                <AlertDescription>
                  {delaySeconds > 0 &&
                    t(
                      'Delayed rejections keep connections open and may be cut short by proxy or client timeouts.'
                    )}{' '}
                  {hasCredentialStatus &&
                    t(
                      'Some clients may treat this status as invalid credentials.'
                    )}{' '}
                  {hasRetryableStatus &&
                    t('Some clients may automatically retry this status.')}
                </AlertDescription>
              </Alert>
            )}

            <Separator />

            <div className='space-y-4'>
              <div className='flex flex-col justify-between gap-3 sm:flex-row sm:items-end'>
                <div>
                  <h3 className='font-semibold'>
                    {t('Group response overrides')}
                  </h3>
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'These responses are used only when an independent user rule in the same group is rejected.'
                    )}
                  </p>
                </div>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() =>
                    fieldArray.append({
                      group: '',
                      statusCode: 429,
                      errorMessage: props.config.default_response.error_message,
                    })
                  }
                >
                  <Plus />
                  {t('Add group override')}
                </Button>
              </div>

              {fieldArray.fields.length > 0 && (
                <div className='relative max-w-md'>
                  <Search className='text-muted-foreground absolute top-2.5 left-3 size-4' />
                  <Input
                    className='pl-9'
                    value={filter}
                    onChange={(event) => setFilter(event.target.value)}
                    placeholder={t('Search group or error message')}
                  />
                </div>
              )}

              <datalist id='user-rate-limit-groups'>
                {availableGroups.map((group) => (
                  <option key={group} value={group} />
                ))}
              </datalist>

              <div className='space-y-3'>
                {fieldArray.fields.map((item, index) => {
                  const current = watched.groupResponses?.[index]
                  const searchable =
                    `${current?.group ?? ''} ${current?.errorMessage ?? ''}`.toLowerCase()
                  if (filterText && !searchable.includes(filterText)) {
                    return null
                  }
                  return (
                    <div
                      key={item.id}
                      className='bg-muted/20 grid gap-3 rounded-xl border p-3 lg:grid-cols-[minmax(140px,0.7fr)_150px_minmax(240px,1.5fr)_auto]'
                    >
                      <FormField
                        control={form.control}
                        name={`groupResponses.${index}.group`}
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Group')}</FormLabel>
                            <FormControl>
                              <Input list='user-rate-limit-groups' {...field} />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                      <FormField
                        control={form.control}
                        name={`groupResponses.${index}.statusCode`}
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
                        name={`groupResponses.${index}.errorMessage`}
                        render={({ field }) => (
                          <FormItem>
                            <FormLabel>{t('Error message')}</FormLabel>
                            <FormControl>
                              <Input maxLength={512} {...field} />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        className='text-destructive self-end'
                        aria-label={t('Delete group override')}
                        onClick={() => fieldArray.remove(index)}
                      >
                        <Trash2 />
                      </Button>
                    </div>
                  )
                })}
                {fieldArray.fields.length === 0 && (
                  <div className='text-muted-foreground rounded-xl border border-dashed p-6 text-center text-sm'>
                    {t(
                      'No group response overrides. All rules use the global response by default.'
                    )}
                  </div>
                )}
              </div>
            </div>

            <div className='flex justify-end'>
              <Button type='submit' disabled={mutation.isPending}>
                <Save />
                {mutation.isPending
                  ? t('Saving...')
                  : t('Save response policy')}
              </Button>
            </div>
          </form>
        </Form>
      </CardContent>
    </Card>
  )
}

function toFormValues(config: UserRateLimitConfig): UserRateLimitConfigForm {
  return {
    delaySeconds: config.delay_seconds,
    defaultResponse: {
      statusCode: config.default_response.status_code,
      errorMessage: config.default_response.error_message,
    },
    groupResponses: config.group_responses.map((item) => ({
      group: item.group,
      statusCode: item.status_code,
      errorMessage: item.error_message,
    })),
  }
}
