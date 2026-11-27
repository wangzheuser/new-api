/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from '@/components/ui/select'

import {
  protocolCapabilityState,
  PROTOCOL_CAPABILITY_LABEL_KEYS,
  supportsNormalizedProtocol,
  TEXT_PROTOCOLS,
  updateProtocolCapabilityState,
  type ProtocolCapabilityState,
} from '../lib/protocol-policy'
import type { ChannelProtocolPolicy, TextEndpointType } from '../types'

type Props = {
  policy: ChannelProtocolPolicy
  models: string[]
  labels: Record<TextEndpointType, string>
  disabled?: boolean
  verification: (model: string) => string
  onChange: (policy: ChannelProtocolPolicy) => void
}

export function ProtocolModelExceptions(props: Props) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState<string | null>(null)
  const models = [
    ...new Set([
      ...props.models,
      ...Object.keys(props.policy.model_overrides ?? {}),
    ]),
  ]
  const native = editing
    ? (props.policy.model_overrides?.[editing]?.native ?? props.policy.native)
    : {}
  return (
    <div className='space-y-3'>
      <StaticDataTable
        data={models}
        getRowKey={(model) => model}
        columns={[
          { id: 'model', header: t('Model'), cell: (model) => model },
          {
            id: 'source',
            header: t('Configuration source'),
            cell: (model) =>
              props.policy.model_overrides?.[model]
                ? t('Model exception')
                : t('Channel default'),
          },
          {
            id: 'verification',
            header: t('Verification'),
            cell: props.verification,
          },
          {
            id: 'actions',
            header: t('Actions'),
            cell: (model) => (
              <div className='flex gap-2'>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={props.disabled}
                  onClick={() => setEditing(model)}
                >
                  {t('Edit')}
                </Button>
                {props.policy.model_overrides?.[model] && (
                  <Button
                    type='button'
                    size='sm'
                    variant='ghost'
                    disabled={props.disabled}
                    onClick={() => {
                      const next = structuredClone(props.policy)
                      delete next.model_overrides?.[model]
                      props.onChange(next)
                    }}
                  >
                    {t('Restore inheritance')}
                  </Button>
                )}
              </div>
            ),
          },
        ]}
      />
      {editing && (
        <div className='space-y-2 rounded-md border p-3'>
          <div className='flex items-center justify-between'>
            <span>{editing}</span>
            <Button
              type='button'
              size='sm'
              variant='ghost'
              onClick={() => setEditing(null)}
            >
              {t('Close')}
            </Button>
          </div>
          {TEXT_PROTOCOLS.map((endpoint) => (
            <div key={endpoint} className='grid grid-cols-3 items-center gap-2'>
              <span className='text-sm'>{props.labels[endpoint]}</span>
              {(['non_stream', 'stream'] as const).map((mode) => (
                <Select
                  key={mode}
                  value={protocolCapabilityState(native[endpoint], mode)}
                  disabled={props.disabled}
                  onValueChange={(state) => {
                    const next = structuredClone(props.policy)
                    next.model_overrides ??= {}
                    next.model_overrides[editing] ??= {
                      native: structuredClone(props.policy.native),
                    }
                    const profile = next.model_overrides[editing].native
                    const capability = updateProtocolCapabilityState(
                      profile[endpoint],
                      endpoint,
                      mode,
                      state as ProtocolCapabilityState
                    )
                    if (capability) profile[endpoint] = capability
                    else delete profile[endpoint]
                    if (Object.keys(profile).length === 0) return
                    props.onChange(next)
                  }}
                >
                  <SelectTrigger
                    aria-label={`${editing} ${props.labels[endpoint]} ${mode === 'stream' ? t('Streaming') : t('Normal')}`}
                  >
                    <SelectValue>
                      {t(
                        PROTOCOL_CAPABILITY_LABEL_KEYS[
                          protocolCapabilityState(native[endpoint], mode)
                        ]
                      )}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value='unavailable'>
                      {t('Unavailable')}
                    </SelectItem>
                    <SelectItem value='native'>
                      {t('Native passthrough')}
                    </SelectItem>
                    <SelectItem
                      value='normalized'
                      disabled={!supportsNormalizedProtocol(endpoint)}
                    >
                      {t('Compatible normalization')}
                    </SelectItem>
                  </SelectContent>
                </Select>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
