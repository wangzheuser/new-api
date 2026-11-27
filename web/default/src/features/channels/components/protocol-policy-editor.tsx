/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { NativeSelect } from '@/components/ui/native-select'
import { handleServerError } from '@/lib/handle-server-error'

import {
  clearChannelProtocolObservations,
  previewChannelProtocol,
  testChannelClientDraft,
} from '../api'
import {
  emptyPolicy,
  modeLabels,
  protocolPaths,
  REQUEST_MODES,
  TEXT_PROTOCOLS,
  textProtocolLabel,
  withAvailability,
  type RequestMode,
} from '../lib/protocol-policy'
import type {
  ChannelNativeProbeDraft,
  ChannelProtocolPolicy,
  TextEndpointType,
} from '../types'
import { ProtocolModelExceptions } from './protocol-model-exceptions'
import { ProtocolProbeDialog } from './protocol-probe-dialog'

type Props = {
  channelId?: number
  draftKey?: string
  getProbeDraft: () => ChannelNativeProbeDraft
  channelType: number
  models: string[]
  savedValue?: string
  value?: string
  disabled?: boolean
  modelMapping?: string
  passThrough: boolean
  onChange: (value: string) => void
}
export function ProtocolPolicyEditor({
  channelId,
  draftKey,
  getProbeDraft,
  channelType,
  models,
  value,
  disabled,
  savedValue,
  passThrough,
  modelMapping,
  onChange,
}: Props) {
  const { t } = useTranslation()
  const policy = useMemo<ChannelProtocolPolicy>(() => {
    if (value) {
      try {
        const parsed = JSON.parse(value)
        if (parsed?.native && typeof parsed.native === 'object') return parsed
      } catch {
        /* Invalid drafts can be corrected in the form. */
      }
    }
    return { ...emptyPolicy, auto_convert: !passThrough }
  }, [value, passThrough])
  const choices = [
    ...new Set([...models, ...Object.keys(policy.model_overrides ?? {})]),
  ]
  const [selected, select] = useState('')
  const model = choices.includes(selected) ? selected : (choices[0] ?? '')
  const [client, setClient] = useState<TextEndpointType>('openai-response')
  const [mode, setMode] = useState<RequestMode>('non_stream')
  const [probe, setProbe] = useState(false)
  const [exceptions, setExceptions] = useState(false)
  const [testing, setTesting] = useState(false)
  const [test, setTest] = useState<{
    snapshot: string
    message: string
  } | null>(null)
  const runner = useRef<AbortController | null>(null)
  useEffect(() => () => runner.current?.abort(), [])
  const editable = channelType === 1
  const ready = !!model && !!(channelId || draftKey?.trim()) && !disabled
  const hasNative = Object.keys(policy.native).length > 0
  const request = {
    model,
    endpoint_type: client,
    stream: mode === 'stream',
    policy: value ? policy : null,
    pass_through: passThrough,
    model_mapping: modelMapping,
  }
  const preview = useQuery({
    queryKey: ['channel-protocol-preview', request],
    queryFn: ({ signal }) => previewChannelProtocol(request, signal),
    enabled: editable && !!model && (hasNative || !value),
    retry: false,
    staleTime: 60000,
  })
  const plan = preview.data
  const write = (next: ChannelProtocolPolicy) => onChange(JSON.stringify(next))
  const probeButton = (
    <Button
      type='button'
      size='sm'
      variant='outline'
      disabled={!ready}
      onClick={() => setProbe(true)}
    >
      {t('Detect upstream interfaces')}
    </Button>
  )
  const dialog = probe && (
    <ProtocolProbeDialog
      model={model}
      models={choices}
      editable={editable}
      disabled={disabled}
      policy={policy}
      normalize={!passThrough}
      getDraft={getProbeDraft}
      onClose={() => setProbe(false)}
      onApply={(next, sample) => {
        write(next)
        if (sample) {
          select(sample)
          setExceptions(true)
        }
        toast.success(t('Protocol settings updated in this form'))
      }}
    />
  )
  if (!editable) {
    return (
      <>
        {probeButton}
        {dialog}
      </>
    )
  }
  let routeSummary
  if (!model) routeSummary = t('Add channel models before probing')
  else if (preview.isFetching) routeSummary = t('Loading...')
  else if (preview.isError) routeSummary = preview.error.message
  else if (!plan) routeSummary = t('No available route')
  else if (plan.route_mode === 'legacy') {
    routeSummary = t(
      'Using channel routing defaults until an interface is selected.'
    )
  } else {
    routeSummary = (
      <>
        {textProtocolLabel(client, t)} →{' '}
        <b>{textProtocolLabel(plan.upstream_endpoint_type, t)}</b>{' '}
        <code>{plan.upstream_path}</code> ·{' '}
        {plan.capability_source === 'model_override'
          ? t('Model exception')
          : t('Channel default')}
      </>
    )
  }
  const testKey = () => JSON.stringify([getProbeDraft(), model, client, mode])
  async function runTest() {
    const abort = new AbortController()
    runner.current = abort
    const snapshot = testKey()
    setTesting(true)
    setTest(null)
    try {
      const result = await testChannelClientDraft(
        {
          ...getProbeDraft(),
          model,
          endpoint_type: client,
          stream: mode === 'stream',
          probe_case: 'basic',
        },
        abort.signal
      )
      if (!abort.signal.aborted) {
        setTest({
          snapshot,
          message: result.success
            ? `${t('Connection successful')} · ${textProtocolLabel(result.upstream_endpoint_type ?? client, t)}`
            : result.message || t('Probe failed'),
        })
      }
    } catch (error) {
      if (!abort.signal.aborted) handleServerError(error)
    } finally {
      if (!abort.signal.aborted) setTesting(false)
    }
  }
  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <strong>{t('Upstream interfaces')}</strong>
        <div className='flex flex-wrap gap-2'>
          {(value ?? '') !== (savedValue ?? '') && (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              disabled={disabled}
              onClick={() => onChange(savedValue ?? '')}
            >
              {t('Undo protocol changes')}
            </Button>
          )}
          {probeButton}
        </div>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Select a protocol for both request modes or enable each mode separately.'
        )}
      </p>
      <StaticDataTable
        data={TEXT_PROTOCOLS}
        getRowKey={(e) => e}
        columns={[
          {
            id: 'protocol',
            header: t('Upstream protocol'),
            cell: (e) => {
              const cap = policy.native[e],
                both = !!cap?.non_stream && !!cap.stream
              return (
                <label className='flex cursor-pointer items-center gap-2'>
                  <Checkbox
                    disabled={disabled}
                    checked={both}
                    indeterminate={!!cap && !both}
                    onCheckedChange={() =>
                      write({
                        ...policy,
                        native: withAvailability(
                          policy.native,
                          e,
                          { non_stream: !both, stream: !both },
                          !passThrough
                        ),
                      })
                    }
                  />
                  <span>
                    {textProtocolLabel(e, t)}
                    <code className='text-muted-foreground block text-xs'>
                      {protocolPaths[e]}
                    </code>
                  </span>
                </label>
              )
            },
          },
          ...REQUEST_MODES.map((key) => ({
            id: key,
            header: t(modeLabels[key]),
            cell: (e: TextEndpointType) => (
              <Checkbox
                aria-label={`${textProtocolLabel(e, t)} ${t(modeLabels[key])}`}
                disabled={disabled}
                checked={!!policy.native[e]?.[key]}
                onCheckedChange={(v) =>
                  write({
                    ...policy,
                    native: withAvailability(
                      policy.native,
                      e,
                      { [key]: v },
                      !passThrough
                    ),
                  })
                }
              />
            ),
          })),
        ]}
      />
      {!hasNative && (
        <p className='text-muted-foreground text-xs'>
          {value
            ? t('At least one native protocol is required')
            : t(
                'Using channel routing defaults until an interface is selected.'
              )}
        </p>
      )}
      <div className='space-y-3 rounded-md border p-3'>
        <strong>{t('Client route preview')}</strong>
        <div className='flex flex-wrap gap-2'>
          <NativeSelect
            aria-label={t('Model')}
            value={model}
            onChange={(e) => select(e.target.value)}
          >
            {choices.map((m) => (
              <option key={m}>{m}</option>
            ))}
          </NativeSelect>
          <NativeSelect
            aria-label={t('Client protocol')}
            value={client}
            onChange={(e) => setClient(e.target.value as TextEndpointType)}
          >
            {TEXT_PROTOCOLS.map((e) => (
              <option key={e} value={e}>
                {textProtocolLabel(e, t)}
              </option>
            ))}
          </NativeSelect>
          <NativeSelect
            aria-label={t('Request mode')}
            value={mode}
            onChange={(e) => setMode(e.target.value as RequestMode)}
          >
            {REQUEST_MODES.map((k) => (
              <option key={k} value={k}>
                {t(modeLabels[k])}
              </option>
            ))}
          </NativeSelect>
        </div>
        <div aria-live='polite' className='text-sm'>
          {routeSummary}
        </div>
        <Button
          type='button'
          size='sm'
          variant='outline'
          disabled={!ready || !plan || testing}
          onClick={() => void runTest()}
        >
          {testing ? t('Testing...') : t('Test Connection')}
        </Button>
        {test && test.snapshot === testKey() && (
          <p role='status' className='text-sm break-words'>
            {test.message}
          </p>
        )}
      </div>
      <details
        open={exceptions}
        onToggle={(e) => setExceptions(e.currentTarget.open)}
      >
        <summary className='cursor-pointer'>
          {t('Model exceptions')} ·{' '}
          {Object.keys(policy.model_overrides ?? {}).length}
        </summary>
        {exceptions && (
          <ProtocolModelExceptions
            model={model}
            models={choices}
            onModel={select}
            disabled={disabled || !hasNative}
            policy={policy}
            onChange={write}
          />
        )}
      </details>
      <details>
        <summary className='cursor-pointer'>{t('Advanced Settings')}</summary>
        <fieldset disabled={disabled || !hasNative} className='mt-3 space-y-3'>
          <label className='flex items-center gap-2'>
            <Checkbox
              checked={policy.auto_convert}
              onCheckedChange={(v) => write({ ...policy, auto_convert: v })}
            />
            {t('Automatic protocol conversion')}
          </label>
          <label className='flex items-center gap-2'>
            <Checkbox
              disabled={!policy.auto_convert}
              checked={!!policy.runtime_fallback}
              onCheckedChange={(v) => write({ ...policy, runtime_fallback: v })}
            />
            {t('Automatic protocol fallback')}
          </label>
          <label className='flex flex-wrap items-center gap-2'>
            {t('Maximum conversion quality')}
            <NativeSelect
              value={policy.max_quality}
              onChange={(e) =>
                write({
                  ...policy,
                  max_quality: e.target
                    .value as ChannelProtocolPolicy['max_quality'],
                })
              }
            >
              <option value='fair'>{t('Good and fair')}</option>
              <option value='good'>{t('Good only')}</option>
            </NativeSelect>
          </label>
          {TEXT_PROTOCOLS.filter(
            (e) =>
              policy.native[e] && (e === 'anthropic' || e === 'openai-response')
          ).map((e) => {
            const cap = policy.native[e]
            if (!cap) return null
            return (
              <label key={e} className='flex items-center gap-2'>
                <Checkbox
                  checked={cap.mode === 'normalized'}
                  onCheckedChange={(v) => {
                    const next = { ...cap }
                    if (v) next.mode = 'normalized'
                    else {
                      delete next.mode
                      delete next.reasoning_history
                    }
                    write({
                      ...policy,
                      native: { ...policy.native, [e]: next },
                    })
                  }}
                />
                {textProtocolLabel(e, t)} · {t('Compatible normalization')}
              </label>
            )
          })}
          {policy.native.anthropic?.mode === 'normalized' && (
            <label className='flex flex-wrap items-center gap-2'>
              {t('Reasoning history')}
              <NativeSelect
                value={policy.native.anthropic.reasoning_history ?? 'preserve'}
                onChange={(e) => {
                  const cap = policy.native.anthropic
                  if (cap) {
                    write({
                      ...policy,
                      native: {
                        ...policy.native,
                        anthropic: {
                          ...cap,
                          reasoning_history:
                            e.target.value === 'strip' ? 'strip' : 'preserve',
                        },
                      },
                    })
                  }
                }}
              >
                <option value='preserve'>{t('Keep original value')}</option>
                <option value='strip'>{t('Remove')}</option>
              </NativeSelect>
            </label>
          )}
          {channelId && (
            <Button
              variant='outline'
              size='sm'
              type='button'
              onClick={async () => {
                try {
                  const res = await clearChannelProtocolObservations(channelId)
                  if (!res.success) throw new Error(res.message)
                  toast.success(t('Protocol observations cleared'))
                } catch (err) {
                  handleServerError(err)
                }
              }}
            >
              {t('Clear protocol observations')}
            </Button>
          )}
        </fieldset>
        {value && (
          <Button
            disabled={disabled}
            type='button'
            size='sm'
            variant='ghost'
            onClick={() => onChange('')}
          >
            {t('Use channel routing defaults')}
          </Button>
        )}
      </details>
      {dialog}
    </div>
  )
}
