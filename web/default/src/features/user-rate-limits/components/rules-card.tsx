import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ChevronLeft,
  ChevronRight,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Trash2,
} from 'lucide-react'
import { useDeferredValue, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { deleteUserRateLimitRule, getUserRateLimitRules } from '../api'
import type { UserRateLimitConfig, UserRateLimitRule } from '../types'
import { RuleDrawer } from './rule-drawer'

type RulesCardProps = {
  config: UserRateLimitConfig
}

export function RulesCard(props: RulesCardProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [keyword, setKeyword] = useState('')
  const [group, setGroup] = useState('')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [editingRule, setEditingRule] = useState<UserRateLimitRule | null>(null)
  const [deletingRule, setDeletingRule] = useState<UserRateLimitRule | null>(
    null
  )
  const deferredKeyword = useDeferredValue(keyword)
  const rulesQuery = useQuery({
    queryKey: ['user-rate-limit-rules', page, deferredKeyword, group],
    queryFn: () =>
      getUserRateLimitRules({
        page,
        pageSize: 20,
        keyword: deferredKeyword.trim(),
        group: group.trim(),
      }),
  })
  const deleteMutation = useMutation({
    mutationFn: deleteUserRateLimitRule,
    onSuccess: async () => {
      setDeletingRule(null)
      await queryClient.invalidateQueries({
        queryKey: ['user-rate-limit-rules'],
      })
      toast.success(t('Independent rule deleted'))
    },
    onError: (error) => toast.error(error.message),
  })

  const totalPages = Math.max(
    1,
    Math.ceil(
      (rulesQuery.data?.total ?? 0) / (rulesQuery.data?.page_size ?? 20)
    )
  )

  const refreshRules = async () => {
    await queryClient.invalidateQueries({ queryKey: ['user-rate-limit-rules'] })
  }

  return (
    <>
      <Card>
        <CardHeader className='gap-4'>
          <div className='flex flex-col justify-between gap-3 sm:flex-row sm:items-start'>
            <div>
              <CardTitle>{t('User independent rules')}</CardTitle>
              <p className='text-muted-foreground mt-1 text-sm'>
                {t(
                  'Each rule replaces count limits for one user and one runtime group bucket.'
                )}
              </p>
            </div>
            <div className='flex gap-2'>
              <Button
                variant='outline'
                size='icon'
                aria-label={t('Refresh rules')}
                onClick={refreshRules}
                disabled={rulesQuery.isFetching}
              >
                <RefreshCw
                  className={rulesQuery.isFetching ? 'animate-spin' : ''}
                />
              </Button>
              <Button
                onClick={() => {
                  setEditingRule(null)
                  setDrawerOpen(true)
                }}
              >
                <Plus />
                {t('Create rule')}
              </Button>
            </div>
          </div>

          <div className='grid gap-3 sm:grid-cols-[minmax(0,1fr)_220px]'>
            <div className='relative'>
              <Search className='text-muted-foreground absolute top-2.5 left-3 size-4' />
              <Input
                className='pl-9'
                value={keyword}
                onChange={(event) => {
                  setKeyword(event.target.value)
                  setPage(1)
                }}
                placeholder={t(
                  'Search user ID, username, email, group, or custom message'
                )}
              />
            </div>
            <Input
              value={group}
              onChange={(event) => {
                setGroup(event.target.value)
                setPage(1)
              }}
              placeholder={t('Exact group filter')}
            />
          </div>
        </CardHeader>

        <CardContent className='space-y-4'>
          <div className='overflow-x-auto rounded-xl border'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('User')}</TableHead>
                  <TableHead>{t('Group')}</TableHead>
                  <TableHead className='text-right'>{t('Total')}</TableHead>
                  <TableHead className='text-right'>
                    {t('Successful')}
                  </TableHead>
                  <TableHead>{t('Response source')}</TableHead>
                  <TableHead>{t('Effective response')}</TableHead>
                  <TableHead>{t('Updated')}</TableHead>
                  <TableHead className='text-right'>{t('Actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rulesQuery.isLoading &&
                  ['loading-1', 'loading-2', 'loading-3', 'loading-4'].map(
                    (rowKey) => (
                      <TableRow key={rowKey}>
                        <TableCell colSpan={8}>
                          <Skeleton className='h-8 w-full' />
                        </TableCell>
                      </TableRow>
                    )
                  )}
                {rulesQuery.data?.items.map((rule) => (
                  <TableRow key={rule.id}>
                    <TableCell>
                      <div className='min-w-44'>
                        <p className='font-medium'>
                          {rule.user.display_name || rule.user.username}
                        </p>
                        <p className='text-muted-foreground text-xs'>
                          #{rule.user.id} · {rule.user.username}
                        </p>
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant='outline' className='font-mono'>
                        {rule.group}
                      </Badge>
                    </TableCell>
                    <TableCell className='text-right font-mono tabular-nums'>
                      {rule.total_count === 0
                        ? t('Unlimited')
                        : rule.total_count}
                    </TableCell>
                    <TableCell className='text-right font-mono tabular-nums'>
                      {rule.success_count}
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={
                          rule.effective_response.source === 'user_group'
                            ? 'default'
                            : 'secondary'
                        }
                      >
                        {t(responseSourceLabel(rule.effective_response.source))}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <div className='max-w-80'>
                        <p className='font-mono text-xs font-semibold'>
                          HTTP {rule.effective_response.status_code}
                        </p>
                        <p
                          className='text-muted-foreground truncate text-xs'
                          title={rule.effective_response.error_message}
                        >
                          {rule.effective_response.error_message}
                        </p>
                      </div>
                    </TableCell>
                    <TableCell className='text-muted-foreground whitespace-nowrap'>
                      {new Date(rule.updated_at * 1000).toLocaleString()}
                    </TableCell>
                    <TableCell>
                      <div className='flex justify-end gap-1'>
                        <Button
                          variant='ghost'
                          size='icon-sm'
                          aria-label={t('Edit rule')}
                          onClick={() => {
                            setEditingRule(rule)
                            setDrawerOpen(true)
                          }}
                        >
                          <Pencil />
                        </Button>
                        <Button
                          variant='ghost'
                          size='icon-sm'
                          className='text-destructive'
                          aria-label={t('Delete rule')}
                          onClick={() => setDeletingRule(rule)}
                        >
                          <Trash2 />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
                {rulesQuery.isSuccess && rulesQuery.data.items.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={8} className='h-32 text-center'>
                      <p className='font-medium'>
                        {t('No independent rules found')}
                      </p>
                      <p className='text-muted-foreground mt-1 text-sm'>
                        {t(
                          'Create a rule to isolate a specific user and group.'
                        )}
                      </p>
                    </TableCell>
                  </TableRow>
                )}
                {rulesQuery.isError && (
                  <TableRow>
                    <TableCell
                      colSpan={8}
                      className='text-destructive h-24 text-center'
                    >
                      {rulesQuery.error.message}
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </div>

          <div className='flex flex-col justify-between gap-3 text-sm sm:flex-row sm:items-center'>
            <p className='text-muted-foreground'>
              {t('{{count}} rule(s)', { count: rulesQuery.data?.total ?? 0 })}
            </p>
            <div className='flex items-center gap-2'>
              <Button
                variant='outline'
                size='sm'
                disabled={page <= 1}
                onClick={() => setPage((current) => Math.max(1, current - 1))}
              >
                <ChevronLeft />
                {t('Previous')}
              </Button>
              <span className='text-muted-foreground min-w-20 text-center'>
                {t('{{page}} / {{pages}}', { page, pages: totalPages })}
              </span>
              <Button
                variant='outline'
                size='sm'
                disabled={page >= totalPages}
                onClick={() =>
                  setPage((current) => Math.min(totalPages, current + 1))
                }
              >
                {t('Next')}
                <ChevronRight />
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      <RuleDrawer
        open={drawerOpen}
        rule={editingRule}
        config={props.config}
        onOpenChange={setDrawerOpen}
        onSaved={refreshRules}
      />

      <AlertDialog
        open={deletingRule !== null}
        onOpenChange={(open) => {
          if (!open) setDeletingRule(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Delete independent rule?')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'The next request will fall back to the existing group or global count limits. Current window counters are kept.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('Cancel')}</AlertDialogCancel>
            <AlertDialogAction
              variant='destructive'
              disabled={deleteMutation.isPending}
              onClick={() => {
                if (deletingRule) deleteMutation.mutate(deletingRule.id)
              }}
            >
              {deleteMutation.isPending ? t('Deleting...') : t('Delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

function responseSourceLabel(
  source: 'global' | 'group' | 'user_group'
): string {
  if (source === 'user_group') return 'User override'
  if (source === 'group') return 'Group override'
  return 'Global default'
}
