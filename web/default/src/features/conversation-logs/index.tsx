/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { api } from '@/lib/api'

type ConversationLog = {
  id: number
  created_at: number
  request_id: string
  username: string
  user_id: number
  channel_id: number
  model_name: string
  upstream_model_name: string
  relay_format: string
  request_path: string
  is_stream: boolean
  status_code: number
  storage_bytes: number
  client_request_body?: string
  upstream_request_body?: string
  upstream_response_body?: string
  client_response_body?: string
  metadata?: string
}

type ApiResponse<T> = {
  success: boolean
  message?: string
  data?: T
}

type ConversationSummary = {
  summary: { storage_bytes: number; record_count: number }
  settings: { enabled: boolean; retention_days: number; max_storage_gb: number }
}

function BodyBlock({ title, body }: { title: string; body?: string }) {
  return (
    <div className='space-y-2'>
      <h3 className='font-medium'>{title}</h3>
      <pre className='bg-muted max-h-72 overflow-auto rounded-md p-3 text-xs break-all whitespace-pre-wrap'>
        {body || '—'}
      </pre>
    </div>
  )
}

export function ConversationLogs() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [retentionDays, setRetentionDays] = useState(7)
  const [maxStorageGB, setMaxStorageGB] = useState(5)

  const summaryQuery = useQuery({
    queryKey: ['conversation-log-summary'],
    queryFn: async () => {
      const response = await api.get<ApiResponse<ConversationSummary>>(
        '/api/conversation_logs/summary'
      )
      if (!response.data.success || !response.data.data) {
        throw new Error(response.data.message)
      }
      return response.data.data
    },
  })
  const logsQuery = useQuery({
    queryKey: ['conversation-logs'],
    queryFn: async () => {
      const response = await api.get<
        ApiResponse<{ items: ConversationLog[]; total: number }>
      >('/api/conversation_logs/?p=1&page_size=100')
      if (!response.data.success) throw new Error(response.data.message)
      return response.data.data
    },
  })
  const detailQuery = useQuery({
    queryKey: ['conversation-log', selectedId],
    enabled: selectedId !== null,
    queryFn: async () => {
      const response = await api.get<ApiResponse<ConversationLog>>(
        `/api/conversation_logs/${selectedId}`
      )
      if (!response.data.success || !response.data.data) {
        throw new Error(response.data.message)
      }
      return response.data.data
    },
  })

  useEffect(() => {
    if (!summaryQuery.data) return
    setEnabled(summaryQuery.data.settings.enabled)
    setRetentionDays(summaryQuery.data.settings.retention_days)
    setMaxStorageGB(summaryQuery.data.settings.max_storage_gb)
  }, [summaryQuery.data])

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['conversation-log-summary'] }),
      queryClient.invalidateQueries({ queryKey: ['conversation-logs'] }),
    ])
  }

  const settingsMutation = useMutation({
    mutationFn: async () => {
      const response = await api.put<ApiResponse<null>>(
        '/api/conversation_logs/settings',
        {
          enabled,
          retention_days: retentionDays,
          max_storage_gb: maxStorageGB,
        }
      )
      if (!response.data.success) throw new Error(response.data.message)
    },
    onSuccess: async () => {
      await refresh()
      toast.success(t('Conversation capture settings updated'))
    },
    onError: (error) => toast.error(error.message),
  })

  const cleanupMutation = useMutation({
    mutationFn: async () => {
      const response = await api.post<ApiResponse<unknown>>(
        '/api/conversation_logs/cleanup'
      )
      if (!response.data.success) throw new Error(response.data.message)
    },
    onSuccess: async () => {
      await refresh()
      toast.success(t('Conversation logs cleaned up'))
    },
    onError: (error) => toast.error(error.message),
  })

  const summary = summaryQuery.data?.summary

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Conversation Capture')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          onClick={() => window.open('/api/conversation_logs/export', '_blank')}
        >
          <Download className='mr-2 size-4' />
          {t('Export JSONL')}
        </Button>
        <Button variant='outline' onClick={() => cleanupMutation.mutate()}>
          <RefreshCw className='mr-2 size-4' />
          {t('Clean Up Now')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <Card>
            <CardHeader>
              <CardTitle>{t('Capture and Retention')}</CardTitle>
            </CardHeader>
            <CardContent className='grid gap-4 md:grid-cols-4'>
              <div className='flex items-center justify-between gap-4 rounded-md border p-3 md:col-span-2'>
                <div>
                  <p className='font-medium'>
                    {t('Global Conversation Capture')}
                  </p>
                  <p className='text-muted-foreground text-sm'>
                    {t('A channel must also enable conversation capture')}
                  </p>
                </div>
                <Switch checked={enabled} onCheckedChange={setEnabled} />
              </div>
              <div className='space-y-2'>
                <Label>{t('Retention Days')}</Label>
                <Input
                  type='number'
                  min={0}
                  max={3650}
                  value={retentionDays}
                  onChange={(event) =>
                    setRetentionDays(Number(event.target.value))
                  }
                />
              </div>
              <div className='space-y-2'>
                <Label>{t('Maximum Storage (GiB)')}</Label>
                <Input
                  type='number'
                  min={0}
                  max={10240}
                  value={maxStorageGB}
                  onChange={(event) =>
                    setMaxStorageGB(Number(event.target.value))
                  }
                />
              </div>
              <div className='text-muted-foreground text-sm md:col-span-3'>
                {t('{{count}} records, {{size}} MiB stored', {
                  count: summary?.record_count ?? 0,
                  size: ((summary?.storage_bytes ?? 0) / 1024 / 1024).toFixed(
                    2
                  ),
                })}
              </div>
              <Button
                onClick={() => settingsMutation.mutate()}
                disabled={settingsMutation.isPending}
              >
                {t('Save Settings')}
              </Button>
            </CardContent>
          </Card>

          <Card>
            <CardContent className='pt-6'>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('Time')}</TableHead>
                    <TableHead>{t('User')}</TableHead>
                    <TableHead>{t('Model')}</TableHead>
                    <TableHead>{t('Channel')}</TableHead>
                    <TableHead>{t('Status')}</TableHead>
                    <TableHead>{t('Size')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(logsQuery.data?.items ?? []).map((log) => (
                    <TableRow
                      key={log.id}
                      className='cursor-pointer'
                      onClick={() => setSelectedId(log.id)}
                    >
                      <TableCell>
                        {new Date(log.created_at * 1000).toLocaleString()}
                      </TableCell>
                      <TableCell>{log.username || `#${log.user_id}`}</TableCell>
                      <TableCell>{log.model_name}</TableCell>
                      <TableCell>#{log.channel_id}</TableCell>
                      <TableCell>
                        <Badge
                          variant={
                            log.status_code >= 400 ? 'destructive' : 'secondary'
                          }
                        >
                          {log.status_code}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        {(log.storage_bytes / 1024).toFixed(1)} KiB
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          {selectedId !== null && (
            <Card>
              <CardHeader className='flex-row items-center justify-between'>
                <CardTitle>{t('Conversation Detail')}</CardTitle>
                <Button variant='ghost' onClick={() => setSelectedId(null)}>
                  {t('Close')}
                </Button>
              </CardHeader>
              <CardContent className='grid gap-4 lg:grid-cols-2'>
                <BodyBlock
                  title={t('Client Request')}
                  body={detailQuery.data?.client_request_body}
                />
                <BodyBlock
                  title={t('Upstream Request')}
                  body={detailQuery.data?.upstream_request_body}
                />
                <BodyBlock
                  title={t('Upstream Response')}
                  body={detailQuery.data?.upstream_response_body}
                />
                <BodyBlock
                  title={t('Client Response')}
                  body={detailQuery.data?.client_response_body}
                />
              </CardContent>
            </Card>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
