/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import type { ColumnDef, RowSelectionState, Table } from '@tanstack/react-table'
import { Power, PowerOff, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import {
  DataTableBulkActions as BulkActionsToolbar,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { api } from '@/lib/api'

import type {
  ApiResponse,
  RegistrationCode,
  RegistrationCodePage,
} from './types'

const route = getRouteApi('/_authenticated/registration-codes/')

const STATUS_ENABLED = 1
const STATUS_DISABLED = 2

type BatchAction = 'enable' | 'disable' | 'delete'

function RegistrationCodeBulkActions({
  table,
}: {
  table: Table<RegistrationCode>
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [deleteOpen, setDeleteOpen] = useState(false)
  const selectedRows = table.getFilteredSelectedRowModel().rows
  const selectedIds = selectedRows.map((row) => row.original.id)
  const codesToCopy = selectedRows.map((row) => row.original.code).join('\n')

  const mutation = useMutation({
    mutationFn: async (action: BatchAction) => {
      const response =
        action === 'delete'
          ? await api.post<ApiResponse<number>>(
              '/api/registration_code/batch',
              {
                ids: selectedIds,
              }
            )
          : await api.post<ApiResponse<number>>(
              '/api/registration_code/status/batch',
              {
                ids: selectedIds,
                status: action === 'enable' ? STATUS_ENABLED : STATUS_DISABLED,
              }
            )
      if (!response.data.success) throw new Error(response.data.message)
      return { action, changed: response.data.data ?? 0 }
    },
    onSuccess: async ({ action, changed }) => {
      await queryClient.invalidateQueries({ queryKey: ['registration-codes'] })
      table.resetRowSelection()
      setDeleteOpen(false)
      toast.success(
        action === 'delete'
          ? t('{{count}} registration code(s) deleted', { count: changed })
          : t('{{count}} registration code(s) updated', { count: changed })
      )
    },
    onError: (error) => toast.error(error.message),
  })

  return (
    <>
      <BulkActionsToolbar table={table} entityName={t('registration code')}>
        <CopyButton
          value={codesToCopy}
          variant='outline'
          size='icon'
          className='size-8'
          tooltip={t('Copy selected registration codes')}
          successTooltip={t('Registration codes copied!')}
        />
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='outline'
                size='icon'
                className='size-8'
                disabled={mutation.isPending}
                onClick={() => mutation.mutate('enable')}
                aria-label={t('Enable selected registration codes')}
              />
            }
          >
            <Power />
          </TooltipTrigger>
          <TooltipContent>
            {t('Enable selected registration codes')}
          </TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='outline'
                size='icon'
                className='size-8'
                disabled={mutation.isPending}
                onClick={() => mutation.mutate('disable')}
                aria-label={t('Disable selected registration codes')}
              />
            }
          >
            <PowerOff />
          </TooltipTrigger>
          <TooltipContent>
            {t('Disable selected registration codes')}
          </TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='destructive'
                size='icon'
                className='size-8'
                disabled={mutation.isPending}
                onClick={() => setDeleteOpen(true)}
                aria-label={t('Delete selected registration codes')}
              />
            }
          >
            <Trash2 />
          </TooltipTrigger>
          <TooltipContent>
            {t('Delete selected registration codes')}
          </TooltipContent>
        </Tooltip>
      </BulkActionsToolbar>

      <Dialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={t('Delete selected registration codes?')}
        description={t(
          'This will delete {{count}} selected registration code(s). Usage records will be retained.',
          { count: selectedIds.length }
        )}
        contentHeight='auto'
        footer={
          <>
            <Button variant='outline' onClick={() => setDeleteOpen(false)}>
              {t('Cancel')}
            </Button>
            <Button
              variant='destructive'
              disabled={mutation.isPending}
              onClick={() => mutation.mutate('delete')}
            >
              {t('Delete')}
            </Button>
          </>
        }
      >
        <p className='text-muted-foreground text-sm'>
          {t('This action cannot be undone.')}
        </p>
      </Dialog>
    </>
  )
}

export function RegistrationCodesTable() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({})

  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    pagination: { defaultPage: 1, defaultPageSize: 20 },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      { columnId: 'status', searchKey: 'status', type: 'array' },
      { columnId: 'usage', searchKey: 'usage', type: 'array' },
      { columnId: 'validity', searchKey: 'validity', type: 'array' },
    ],
  })

  const filterValue = (id: string) =>
    ((columnFilters.find((filter) => filter.id === id)?.value as
      | string[]
      | undefined) ?? [])[0] ?? ''
  const status = filterValue('status')
  const usage = filterValue('usage')
  const validity = filterValue('validity')

  const { data, isLoading, isFetching } = useQuery({
    queryKey: [
      'registration-codes',
      pagination.pageIndex + 1,
      pagination.pageSize,
      globalFilter,
      status,
      usage,
      validity,
    ],
    queryFn: async () => {
      const keyword = globalFilter?.trim() ?? ''
      const params = new URLSearchParams({
        p: String(pagination.pageIndex + 1),
        page_size: String(pagination.pageSize),
      })
      if (keyword) params.set('keyword', keyword)
      if (status) params.set('status', status)
      if (usage) params.set('usage', usage)
      if (validity) params.set('validity', validity)
      const response = await api.get<ApiResponse<RegistrationCodePage>>(
        `/api/registration_code/?${params.toString()}`
      )
      if (!response.data.success) throw new Error(response.data.message)
      return response.data.data ?? { items: [], total: 0 }
    },
    placeholderData: (previousData) => previousData,
  })

  const updateMutation = useMutation({
    mutationFn: async (code: RegistrationCode) => {
      const response = await api.put<ApiResponse<RegistrationCode>>(
        '/api/registration_code/',
        code
      )
      if (!response.data.success) throw new Error(response.data.message)
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['registration-codes'] })
    },
    onError: (error) => toast.error(error.message),
  })

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      const response = await api.delete<ApiResponse>(
        `/api/registration_code/${id}`
      )
      if (!response.data.success) throw new Error(response.data.message)
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['registration-codes'] })
    },
    onError: (error) => toast.error(error.message),
  })

  const columns = useMemo<ColumnDef<RegistrationCode>[]>(
    () => [
      {
        id: 'select',
        header: ({ table }) => (
          <Checkbox
            checked={table.getIsAllPageRowsSelected()}
            indeterminate={table.getIsSomePageRowsSelected()}
            onCheckedChange={(value) =>
              table.toggleAllPageRowsSelected(!!value)
            }
            aria-label={t('Select all')}
          />
        ),
        cell: ({ row }) => (
          <Checkbox
            checked={row.getIsSelected()}
            onCheckedChange={(value) => row.toggleSelected(!!value)}
            aria-label={t('Select row')}
          />
        ),
        enableSorting: false,
        enableHiding: false,
        size: 40,
      },
      {
        accessorKey: 'name',
        header: t('Name'),
        cell: ({ row }) => (
          <span className='font-medium'>{row.original.name}</span>
        ),
      },
      {
        accessorKey: 'code',
        header: t('Registration Code'),
        cell: ({ row }) => (
          <span className='font-mono text-xs'>{row.original.code}</span>
        ),
      },
      {
        accessorKey: 'used_count',
        header: t('Usage'),
        cell: ({ row }) => (
          <span>
            {row.original.used_count} / {row.original.max_uses}
          </span>
        ),
      },
      {
        accessorKey: 'status',
        header: t('Status'),
        cell: ({ row }) => (
          <Badge variant='secondary'>
            {row.original.status === STATUS_ENABLED
              ? t('Enabled')
              : t('Disabled')}
          </Badge>
        ),
      },
      {
        id: 'usage',
        accessorFn: (code) => {
          if (code.used_count === 0) return 'unused'
          return code.used_count >= code.max_uses ? 'exhausted' : 'partial'
        },
        enableHiding: false,
      },
      {
        id: 'validity',
        accessorFn: (code) => {
          const now = Math.floor(Date.now() / 1000)
          if (code.open_time > now) return 'pending'
          if (code.end_time > 0 && code.end_time < now) return 'expired'
          return 'active'
        },
        enableHiding: false,
      },
      {
        id: 'actions',
        header: () => <div className='text-right'>{t('Actions')}</div>,
        cell: ({ row }) => (
          <div className='flex justify-end gap-2'>
            <CopyButton value={row.original.code} />
            <Button
              variant='outline'
              size='sm'
              disabled={updateMutation.isPending}
              aria-label={
                row.original.status === STATUS_ENABLED
                  ? t('Disable')
                  : t('Enable')
              }
              onClick={() =>
                updateMutation.mutate({
                  ...row.original,
                  status:
                    row.original.status === STATUS_ENABLED
                      ? STATUS_DISABLED
                      : STATUS_ENABLED,
                })
              }
            >
              {row.original.status === STATUS_ENABLED
                ? t('Disable')
                : t('Enable')}
            </Button>
            <Button
              variant='ghost'
              size='icon'
              disabled={deleteMutation.isPending}
              onClick={() => deleteMutation.mutate(row.original.id)}
              aria-label={t('Delete')}
            >
              <Trash2 className='text-destructive size-4' />
            </Button>
          </div>
        ),
        enableSorting: false,
        enableHiding: false,
      },
    ],
    [deleteMutation, t, updateMutation]
  )

  const { table } = useDataTable({
    data: data?.items ?? [],
    columns,
    enableRowSelection: true,
    getRowId: (row) => String(row.id),
    rowSelection,
    onRowSelectionChange: setRowSelection,
    columnFilters,
    globalFilter,
    pagination,
    onPaginationChange: (updater) => {
      setRowSelection({})
      onPaginationChange(updater)
    },
    onGlobalFilterChange: (updater) => {
      setRowSelection({})
      onGlobalFilterChange?.(updater)
    },
    onColumnFiltersChange: (updater) => {
      setRowSelection({})
      onColumnFiltersChange(updater)
    },
    manualPagination: true,
    manualFiltering: true,
    totalCount: data?.total ?? 0,
    ensurePageInRange,
    initialColumnVisibility: { usage: false, validity: false },
  })

  const statusOptions = useMemo(
    () => [
      { value: String(STATUS_ENABLED), label: t('Enabled') },
      { value: String(STATUS_DISABLED), label: t('Disabled') },
    ],
    [t]
  )
  const usageOptions = useMemo(
    () => [
      { value: 'unused', label: t('Unused') },
      { value: 'partial', label: t('Partially used') },
      { value: 'exhausted', label: t('Exhausted') },
    ],
    [t]
  )
  const validityOptions = useMemo(
    () => [
      { value: 'pending', label: t('Not started') },
      { value: 'active', label: t('Active') },
      { value: 'expired', label: t('Expired') },
    ],
    [t]
  )

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      isFetching={isFetching}
      fixedHeight={false}
      paginationInFooter={false}
      applyHeaderSize
      emptyTitle={t('No registration codes found')}
      emptyDescription={t(
        'Generate a registration code or adjust the filters.'
      )}
      toolbarProps={{
        searchPlaceholder: t('Filter by name or registration code...'),
        searchDebounceMs: 250,
        filters: [
          {
            columnId: 'status',
            title: t('Status'),
            options: statusOptions,
            singleSelect: true,
          },
          {
            columnId: 'usage',
            title: t('Usage'),
            options: usageOptions,
            singleSelect: true,
          },
          {
            columnId: 'validity',
            title: t('Validity'),
            options: validityOptions,
            singleSelect: true,
          },
        ],
      }}
      bulkActions={<RegistrationCodeBulkActions table={table} />}
    />
  )
}
