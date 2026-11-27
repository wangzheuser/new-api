/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'

import {
  REQUEST_MODES,
  TEXT_PROTOCOLS,
  modeLabels,
  profileFor,
  protocolOverrides,
  setModelOverrides,
  textProtocolLabel,
} from '../lib/protocol-policy'
import type { ChannelProtocolPolicy, TextEndpointType } from '../types'

export function ProtocolModelExceptions({
  model,
  models,
  onModel,
  policy,
  onChange,
  disabled,
}: {
  model: string
  models: string[]
  onModel: (name: string) => void
  policy: ChannelProtocolPolicy
  onChange: (p: ChannelProtocolPolicy) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const profile = profileFor(policy, model)
  const patches = protocolOverrides(profile)
  return (
    <div className='space-y-2'>
      <div className='flex flex-wrap items-center gap-2'>
        <NativeSelect
          aria-label={t('Model exception')}
          value={model}
          onChange={(e) => onModel(e.target.value)}
        >
          {models.map((m) => (
            <option key={m}>{m}</option>
          ))}
        </NativeSelect>
        <Button
          size='sm'
          variant='ghost'
          type='button'
          disabled={disabled || !profile}
          onClick={() => onChange(setModelOverrides(policy, model, {}))}
        >
          {t('Restore inheritance')}
        </Button>
      </div>
      <StaticDataTable
        data={TEXT_PROTOCOLS}
        getRowKey={(e) => e}
        columns={[
          {
            id: 'endpoint',
            header: t('Upstream protocol'),
            cell: (e) => textProtocolLabel(e, t),
          },
          ...REQUEST_MODES.map((key) => ({
            id: key,
            header: t(modeLabels[key]),
            cell: (endpoint: TextEndpointType) => {
              const flag = patches[endpoint]?.[key]
              return (
                <NativeSelect
                  size='sm'
                  aria-label={`${model} ${textProtocolLabel(endpoint, t)} ${t(
                    modeLabels[key]
                  )}`}
                  value={flag === undefined ? 'inherit' : String(flag)}
                  disabled={!model || disabled}
                  onChange={(e) => {
                    const next = { ...patches },
                      patch = { ...next[endpoint] }
                    if (e.target.value === 'inherit') delete patch[key]
                    else patch[key] = e.target.value === 'true'
                    if (Object.keys(patch).length) next[endpoint] = patch
                    else delete next[endpoint]
                    onChange(setModelOverrides(policy, model, next))
                  }}
                >
                  <option value='inherit'>
                    {t('Inherit')} ·{' '}
                    {policy.native[endpoint]?.[key]
                      ? t('Enabled')
                      : t('Disabled')}
                  </option>
                  <option value='true'>{t('Enabled')}</option>
                  <option value='false'>{t('Disabled')}</option>
                </NativeSelect>
              )
            },
          })),
        ]}
      />
    </div>
  )
}
