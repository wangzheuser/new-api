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
import { useQueryClient } from '@tanstack/react-query'
import {
  CheckCircle2,
  Loader2,
  Play,
  RefreshCw,
  RotateCw,
  ShieldOff,
  Square,
  Trash2,
  Power,
  PowerOff,
} from 'lucide-react'
import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Progress,
  ProgressLabel,
  ProgressValue,
} from '@/components/ui/progress'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import {
  getMultiKeyStatus,
  enableMultiKey,
  disableMultiKey,
  deleteMultiKey,
  enableAllMultiKeys,
  disableAllMultiKeys,
  deleteDisabledMultiKeys,
} from '../../api'
import { MULTI_KEY_FILTER_OPTIONS } from '../../constants'
import { useMultiKeyTest } from '../../hooks/use-multi-key-test'
import {
  channelsQueryKeys,
  formatTimestamp,
  getMultiKeyStatusConfig,
  getMultiKeyConfirmMessage,
  getMultiKeyTestActionIndexes,
  isDestructiveAction,
} from '../../lib'
import type {
  KeyStatus,
  MultiKeyConfirmAction,
  MultiKeyTestResult,
} from '../../types'
import { useChannels } from '../channels-provider'
import { StatisticsCard } from './multi-key-statistics-card'
import { MultiKeyTableRowActions } from './multi-key-table-row-actions'
import { MultiKeyTestDetailsDialog } from './multi-key-test-details-dialog'
import { MultiKeyTestResultBadge } from './multi-key-test-result'

type MultiKeyManageDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function MultiKeyManageDialog({
  open,
  onOpenChange,
}: MultiKeyManageDialogProps) {
  const { t } = useTranslation()
  const { currentRow } = useChannels()
  const queryClient = useQueryClient()
  const currentUser = useAuthStore((s) => s.auth.user)
  const canEditSensitive = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.SENSITIVE_WRITE
  )

  // Data state
  const [isLoading, setIsLoading] = useState(false)
  const [keys, setKeys] = useState<KeyStatus[]>([])
  const [currentPage, setCurrentPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)
  const [total, setTotal] = useState(0)
  const [totalPages, setTotalPages] = useState(0)
  const [enabledCount, setEnabledCount] = useState(0)
  const [manualDisabledCount, setManualDisabledCount] = useState(0)
  const [autoDisabledCount, setAutoDisabledCount] = useState(0)
  // UI state
  const [statusFilter, setStatusFilter] = useState<number | null>(null)
  const [confirmAction, setConfirmAction] =
    useState<MultiKeyConfirmAction | null>(null)
  const [isPerformingAction, setIsPerformingAction] = useState(false)
  const [detailsResult, setDetailsResult] = useState<MultiKeyTestResult | null>(
    null
  )
  const multiKeyTest = useMultiKeyTest({
    channelId: currentRow?.id ?? 0,
    open,
  })

  // Reset and load data when dialog opens
  useEffect(() => {
    if (open && currentRow) {
      setCurrentPage(1)
      setStatusFilter(null)
      multiKeyTest.reset()
      loadKeyStatus(1, pageSize, null)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, currentRow?.id])

  const loadKeyStatus = async (
    page: number = currentPage,
    size: number = pageSize,
    status: number | null = statusFilter
  ) => {
    if (!currentRow) return

    setIsLoading(true)
    try {
      const response = await getMultiKeyStatus(
        currentRow.id,
        page,
        size,
        status === null ? undefined : status
      )

      if (response.success && response.data) {
        setKeys(response.data.keys || [])
        setTotal(response.data.total || 0)
        setCurrentPage(response.data.page || 1)
        setPageSize(response.data.page_size || 10)
        setTotalPages(response.data.total_pages || 0)
        setEnabledCount(response.data.enabled_count || 0)
        setManualDisabledCount(response.data.manual_disabled_count || 0)
        setAutoDisabledCount(response.data.auto_disabled_count || 0)
      } else {
        toast.error(response.message || t('Failed to load key status'))
      }
    } catch (error: unknown) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to load key status')
      )
    } finally {
      setIsLoading(false)
    }
  }

  const handleStatusFilterChange = (value: string) => {
    const newFilter = value === 'all' ? null : Number.parseInt(value)
    setStatusFilter(newFilter)
    setCurrentPage(1)
    loadKeyStatus(1, pageSize, newFilter)
  }

  const handlePageChange = (newPage: number) => {
    setCurrentPage(newPage)
    loadKeyStatus(newPage, pageSize)
  }

  const performAction = async () => {
    if (!confirmAction || !currentRow) return
    if (
      !canEditSensitive &&
      (confirmAction.type === 'delete' ||
        confirmAction.type === 'delete-disabled')
    ) {
      setConfirmAction(null)
      return
    }

    setIsPerformingAction(true)
    try {
      const { type, keyIndex } = confirmAction
      let response

      // Execute the appropriate action
      if (type === 'enable' && keyIndex !== undefined) {
        response = await enableMultiKey(currentRow.id, keyIndex)
      } else if (type === 'disable' && keyIndex !== undefined) {
        response = await disableMultiKey(currentRow.id, keyIndex)
      } else if (type === 'delete' && keyIndex !== undefined) {
        response = await deleteMultiKey(currentRow.id, keyIndex)
      } else if (type === 'enable-all') {
        response = await enableAllMultiKeys(currentRow.id)
      } else if (type === 'disable-all') {
        response = await disableAllMultiKeys(currentRow.id)
      } else if (type === 'delete-disabled') {
        response = await deleteDisabledMultiKeys(currentRow.id)
      } else if (type === 'disable-unavailable') {
        const responses = await Promise.all(
          (confirmAction.keyIndexes || []).map((index) =>
            disableMultiKey(currentRow.id, index)
          )
        )
        response = responses.find((item) => !item.success) || { success: true }
      } else if (type === 'enable-available') {
        const responses = await Promise.all(
          (confirmAction.keyIndexes || []).map((index) =>
            enableMultiKey(currentRow.id, index)
          )
        )
        response = responses.find((item) => !item.success) || { success: true }
      }

      if (response?.success) {
        toast.success(response.message || t('Operation successful'))
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
        // Reload data - reset to page 1 for bulk actions
        const isBulkAction =
          type.includes('all') ||
          type === 'delete-disabled' ||
          type === 'disable-unavailable' ||
          type === 'enable-available'
        if (isBulkAction) {
          setCurrentPage(1)
          loadKeyStatus(1, pageSize)
        } else {
          loadKeyStatus(currentPage, pageSize)
        }
        if (type === 'delete' || type === 'delete-disabled') {
          multiKeyTest.reset()
        }
      } else {
        toast.error(response?.message || t('Operation failed'))
      }
    } catch (error: unknown) {
      toast.error(
        error instanceof Error ? error.message : t('Operation failed')
      )
    } finally {
      setIsPerformingAction(false)
      setConfirmAction(null)
    }
  }

  const renderStatusBadge = (status: number) => {
    const config = getMultiKeyStatusConfig(status)
    return (
      <StatusBadge
        label={t(config.label)}
        variant={config.variant}
        showDot
        copyable={false}
      />
    )
  }

  const formatKeyTimestamp = (timestamp?: number) => {
    if (!timestamp) return '-'
    return formatTimestamp(timestamp)
  }

  const testActionIndexes = getMultiKeyTestActionIndexes(
    multiKeyTest.results.values()
  )
  const abnormalIndexes = [...multiKeyTest.results.values()]
    .filter((result) => !result.success)
    .map((result) => result.key_index)
  const showTestSummary =
    multiKeyTest.runState !== 'idle' || multiKeyTest.summary.completed > 0
  const progress =
    multiKeyTest.runTotal > 0
      ? (multiKeyTest.runCompleted / multiKeyTest.runTotal) * 100
      : 0
  const keyCount = currentRow?.channel_info?.multi_key_size || total
  let testProgressLabel = t('Testing completed')
  if (multiKeyTest.runState === 'running') {
    testProgressLabel = t('Testing keys')
  } else if (multiKeyTest.runState === 'stopped') {
    testProgressLabel = t('Testing stopped')
  }

  let keyTableFallback = (
    <div className='text-muted-foreground py-12 text-center'>
      {t('No keys found')}
    </div>
  )
  if (isLoading) {
    keyTableFallback = (
      <div className='flex items-center justify-center py-12'>
        <Loader2 className='text-muted-foreground h-8 w-8 animate-spin' />
      </div>
    )
  }

  const startAllKeyTests = async () => {
    if (!currentRow || keyCount <= 0) return
    await multiKeyTest.startBatch(
      Array.from({ length: keyCount }, (_, index) => index),
      true
    )
  }

  if (!currentRow) return null

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={onOpenChange}
        title={
          <>
            {t('Multi-Key Management')}
            <StatusBadge
              label={currentRow.name}
              variant='neutral'
              copyable={false}
            />
            {currentRow.channel_info?.multi_key_mode && (
              <StatusBadge
                label={
                  currentRow.channel_info.multi_key_mode === 'random'
                    ? t('Random')
                    : t('Polling')
                }
                variant='neutral'
                copyable={false}
              />
            )}
          </>
        }
        description={t(
          'Manage multi-key status and configuration for this channel'
        )}
        contentClassName='flex w-[calc(100vw-2rem)] max-w-[1400px] flex-col sm:max-w-[min(1400px,calc(100vw-2rem))]'
        titleClassName='flex items-center gap-2'
        contentHeight='min(72vh, 720px)'
        bodyClassName='space-y-4'
      >
        <div className='flex min-h-0 flex-1 flex-col space-y-4 overflow-hidden'>
          {/* Statistics */}
          <div className='grid shrink-0 grid-cols-3 gap-3'>
            <StatisticsCard
              label={t(showTestSummary ? 'Tested' : 'Enabled')}
              count={
                showTestSummary ? multiKeyTest.summary.completed : enabledCount
              }
              total={showTestSummary ? keyCount : total}
            />
            <StatisticsCard
              label={t(showTestSummary ? 'Available' : 'Manual Disabled')}
              count={
                showTestSummary
                  ? multiKeyTest.summary.available
                  : manualDisabledCount
              }
              total={showTestSummary ? keyCount : total}
            />
            <StatisticsCard
              label={t(showTestSummary ? 'Abnormal' : 'Auto Disabled')}
              count={
                showTestSummary
                  ? multiKeyTest.summary.abnormal
                  : autoDisabledCount
              }
              total={showTestSummary ? keyCount : total}
            />
          </div>

          {multiKeyTest.runState !== 'idle' && (
            <div className='bg-muted/35 shrink-0 rounded-md border p-3'>
              <Progress value={progress}>
                <ProgressLabel>{testProgressLabel}</ProgressLabel>
                <ProgressValue>
                  {() =>
                    `${multiKeyTest.runCompleted} / ${multiKeyTest.runTotal}`
                  }
                </ProgressValue>
              </Progress>
            </div>
          )}

          <Separator className='shrink-0' />

          {/* Toolbar */}
          <div className='flex shrink-0 flex-wrap items-center justify-between gap-2'>
            <Select
              items={MULTI_KEY_FILTER_OPTIONS.map((option) => ({
                value: option.value,
                label: t(option.label),
              }))}
              value={statusFilter === null ? 'all' : statusFilter.toString()}
              onValueChange={(v) => v !== null && handleStatusFilterChange(v)}
            >
              <SelectTrigger className='w-40'>
                <SelectValue placeholder={t('All Status')} />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {MULTI_KEY_FILTER_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {t(option.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>

            <div className='flex flex-wrap items-center justify-end gap-2'>
              {multiKeyTest.runState === 'running' ? (
                <Button
                  variant='outline'
                  size='sm'
                  onClick={multiKeyTest.stopBatch}
                >
                  <Square className='h-4 w-4' />
                  {t('Stop testing')}
                </Button>
              ) : (
                <Button
                  variant='default'
                  size='sm'
                  onClick={() => void startAllKeyTests()}
                  disabled={keyCount === 0}
                >
                  <Play className='h-4 w-4' />
                  {t('Test all keys')}
                </Button>
              )}

              {abnormalIndexes.length > 0 &&
                multiKeyTest.runState !== 'running' && (
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={() =>
                      void multiKeyTest.startBatch(abnormalIndexes)
                    }
                  >
                    <RotateCw className='h-4 w-4' />
                    {t('Retest abnormal')}
                  </Button>
                )}

              {testActionIndexes.unavailable.length > 0 && (
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() =>
                    setConfirmAction({
                      type: 'disable-unavailable',
                      keyIndexes: testActionIndexes.unavailable,
                    })
                  }
                >
                  <ShieldOff className='h-4 w-4' />
                  {t('Disable unavailable')}
                </Button>
              )}

              {testActionIndexes.available.length > 0 && (
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() =>
                    setConfirmAction({
                      type: 'enable-available',
                      keyIndexes: testActionIndexes.available,
                    })
                  }
                >
                  <CheckCircle2 className='h-4 w-4' />
                  {t('Enable available')}
                </Button>
              )}
              <Button
                variant='outline'
                size='sm'
                onClick={() => loadKeyStatus()}
                disabled={isLoading}
              >
                <RefreshCw className='h-4 w-4' />
              </Button>

              {manualDisabledCount + autoDisabledCount > 0 && (
                <Button
                  variant='default'
                  size='sm'
                  onClick={() => setConfirmAction({ type: 'enable-all' })}
                >
                  <Power className='mr-2 h-4 w-4' />
                  {t('Enable All')}
                </Button>
              )}

              {enabledCount > 0 && (
                <Button
                  variant='destructive'
                  size='sm'
                  onClick={() => setConfirmAction({ type: 'disable-all' })}
                >
                  <PowerOff className='mr-2 h-4 w-4' />
                  {t('Disable All')}
                </Button>
              )}

              {autoDisabledCount > 0 && (
                <Button
                  variant='destructive'
                  size='sm'
                  onClick={() => {
                    if (!canEditSensitive) return
                    setConfirmAction({ type: 'delete-disabled' })
                  }}
                  disabled={!canEditSensitive}
                  title={
                    canEditSensitive
                      ? undefined
                      : t('No permission to perform this action')
                  }
                >
                  <Trash2 className='mr-2 h-4 w-4' />
                  {t('Delete Auto-Disabled')}
                </Button>
              )}
            </div>
          </div>
          {!canEditSensitive && (
            <p className='text-muted-foreground text-xs'>
              {t('No permission to perform this action')}
            </p>
          )}

          {/* Table */}
          <div className='min-h-0 flex-1 overflow-auto rounded-md border'>
            {keys.length > 0 && !isLoading ? (
              <StaticDataTable
                className='rounded-none border-0'
                tableClassName='min-w-[1100px]'
                data={keys}
                getRowKey={(key) => key.index}
                columns={[
                  {
                    id: 'index',
                    header: t('Index'),
                    className: 'w-20',
                    cellClassName: 'font-mono text-sm',
                    cell: (key) => `#${key.index + 1}`,
                  },
                  {
                    id: 'status',
                    header: t('Status'),
                    className: 'w-32',
                    cell: (key) => renderStatusBadge(key.status),
                  },
                  {
                    id: 'reason',
                    header: t('Disabled Reason'),
                    className: 'min-w-[200px]',
                    cellClassName: 'max-w-xs truncate text-sm',
                    cell: (key) => key.reason || '-',
                  },
                  {
                    id: 'disabled-time',
                    header: t('Disabled Time'),
                    className: 'w-44',
                    cellClassName: 'text-muted-foreground text-sm',
                    cell: (key) => formatKeyTimestamp(key.disabled_time),
                  },
                  {
                    id: 'test-result',
                    header: t('Test result'),
                    className: 'w-44',
                    cell: (key) => (
                      <MultiKeyTestResultBadge
                        result={multiKeyTest.results.get(key.index)}
                        isTesting={multiKeyTest.testingKeys.has(key.index)}
                        onShowDetails={setDetailsResult}
                      />
                    ),
                  },
                  {
                    id: 'response-time',
                    header: t('Response time'),
                    className: 'w-32',
                    cellClassName: 'text-muted-foreground text-sm tabular-nums',
                    cell: (key) => {
                      const time = multiKeyTest.results.get(key.index)?.time
                      return time === undefined ? '-' : `${time.toFixed(2)} s`
                    },
                  },
                  {
                    id: 'tested-at',
                    header: t('Last tested'),
                    className: 'w-44',
                    cellClassName: 'text-muted-foreground text-sm',
                    cell: (key) => {
                      const testedAt = multiKeyTest.results.get(
                        key.index
                      )?.tested_at
                      return testedAt
                        ? formatKeyTimestamp(Math.floor(testedAt / 1000))
                        : '-'
                    },
                  },
                  {
                    id: 'actions',
                    header: t('Actions'),
                    className: 'text-right',
                    cell: (key) => (
                      <MultiKeyTableRowActions
                        keyIndex={key.index}
                        status={key.status}
                        canDelete={canEditSensitive}
                        hasTestResult={multiKeyTest.results.has(key.index)}
                        isTesting={multiKeyTest.testingKeys.has(key.index)}
                        onTest={(keyIndex) =>
                          void multiKeyTest.testKey(keyIndex)
                        }
                        onAction={setConfirmAction}
                      />
                    ),
                  },
                ]}
              />
            ) : (
              keyTableFallback
            )}
          </div>

          {/* Pagination */}
          {totalPages > 1 && (
            <div className='flex shrink-0 items-center justify-between'>
              <div className='text-muted-foreground text-sm'>
                {t('Page {{current}} of {{total}}', {
                  current: currentPage,
                  total: totalPages,
                })}
              </div>
              <div className='flex gap-2'>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => handlePageChange(currentPage - 1)}
                  disabled={currentPage === 1 || isLoading}
                >
                  {t('Previous')}
                </Button>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => handlePageChange(currentPage + 1)}
                  disabled={currentPage >= totalPages || isLoading}
                >
                  {t('Next')}
                </Button>
              </div>
            </div>
          )}
        </div>
      </Dialog>

      {/* Confirmation Dialog */}
      <ConfirmDialog
        open={confirmAction !== null}
        onOpenChange={(open) => !open && setConfirmAction(null)}
        title={t('Confirm Action')}
        desc={t(getMultiKeyConfirmMessage(confirmAction))}
        destructive={isDestructiveAction(confirmAction)}
        isLoading={isPerformingAction}
        handleConfirm={performAction}
      />
      <MultiKeyTestDetailsDialog
        result={detailsResult}
        onOpenChange={(detailsOpen) => !detailsOpen && setDetailsResult(null)}
      />
    </>
  )
}
