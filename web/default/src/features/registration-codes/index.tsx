/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { api } from '@/lib/api'

import { RegistrationCodesTable } from './registration-codes-table'
import type { ApiResponse, RegistrationCode } from './types'

function toUnixTime(value: string) {
  return value ? Math.floor(new Date(value).getTime() / 1000) : 0
}

export function RegistrationCodes() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [name, setName] = useState('Registration')
  const [count, setCount] = useState(1)
  const [maxUses, setMaxUses] = useState(1)
  const [openTime, setOpenTime] = useState('')
  const [endTime, setEndTime] = useState('')

  const statusQuery = useQuery({
    queryKey: ['registration-code-status'],
    queryFn: async () => {
      const response =
        await api.get<ApiResponse<Record<string, unknown>>>('/api/status')
      return response.data.data
    },
  })
  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['registration-codes'] }),
      queryClient.invalidateQueries({ queryKey: ['registration-code-status'] }),
    ])
  }

  const createMutation = useMutation({
    mutationFn: async () => {
      const response = await api.post<ApiResponse<RegistrationCode[]>>(
        '/api/registration_code/',
        {
          name,
          count,
          max_uses: maxUses,
          open_time: toUnixTime(openTime),
          end_time: toUnixTime(endTime),
        }
      )
      if (!response.data.success) throw new Error(response.data.message)
      return response.data.data ?? []
    },
    onSuccess: async (codes) => {
      await refresh()
      toast.success(
        t('{{count}} registration code(s) created', { count: codes.length })
      )
    },
    onError: (error) => toast.error(error.message),
  })

  const required = statusQuery.data?.registration_code_required === true

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Registration Codes')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <Card>
            <CardHeader>
              <CardTitle>{t('Registration Restriction')}</CardTitle>
            </CardHeader>
            <CardContent className='flex items-center justify-between gap-4'>
              <div>
                <p className='font-medium'>{t('Registration Code Required')}</p>
                <p className='text-muted-foreground text-sm'>
                  {t('Require a valid registration code for all new users')}
                </p>
              </div>
              <Switch
                checked={required}
                onCheckedChange={async (checked) => {
                  const response = await api.put<ApiResponse<null>>(
                    '/api/registration_code/config',
                    { required: checked }
                  )
                  if (!response.data.success) {
                    toast.error(response.data.message)
                    return
                  }
                  await refresh()
                }}
              />
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('Generate Registration Codes')}</CardTitle>
            </CardHeader>
            <CardContent className='grid gap-4 md:grid-cols-5'>
              <div className='space-y-2'>
                <Label>{t('Name')}</Label>
                <Input
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
              </div>
              <div className='space-y-2'>
                <Label>{t('Quantity')}</Label>
                <Input
                  type='number'
                  min={1}
                  max={100}
                  value={count}
                  onChange={(event) => setCount(Number(event.target.value))}
                />
              </div>
              <div className='space-y-2'>
                <Label>{t('Maximum Uses')}</Label>
                <Input
                  type='number'
                  min={1}
                  value={maxUses}
                  onChange={(event) => setMaxUses(Number(event.target.value))}
                />
              </div>
              <div className='space-y-2'>
                <Label>{t('Valid From')}</Label>
                <Input
                  type='datetime-local'
                  value={openTime}
                  onChange={(event) => setOpenTime(event.target.value)}
                />
              </div>
              <div className='space-y-2'>
                <Label>{t('Expires At')}</Label>
                <Input
                  type='datetime-local'
                  value={endTime}
                  onChange={(event) => setEndTime(event.target.value)}
                />
              </div>
              <Button
                className='md:col-span-5 md:w-fit'
                onClick={() => createMutation.mutate()}
                disabled={createMutation.isPending || !name.trim()}
              >
                <Plus className='mr-2 size-4' />
                {t('Generate')}
              </Button>
            </CardContent>
          </Card>

          <Card>
            <CardContent className='pt-6'>
              <RegistrationCodesTable />
            </CardContent>
          </Card>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
