/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { NativeSelect } from '@/components/ui/native-select'

import { probeChannelProtocolDraft } from '../api'
import {
  applyDetectedProtocols,
  classificationLabels,
  modeLabels,
  probeCases,
  REQUEST_MODES,
  TEXT_PROTOCOLS,
  textProtocolLabel,
} from '../lib/protocol-policy'
import type {
  ChannelNativeProbeDraft,
  ChannelProtocolPolicy,
  ChannelProtocolProbeResponse,
  TextEndpointType,
} from '../types'

type Props = {
  models: string[]
  model: string
  policy: ChannelProtocolPolicy
  editable: boolean
  disabled?: boolean
  normalize: boolean
  getDraft: () => ChannelNativeProbeDraft
  onClose: () => void
  onApply: (policy: ChannelProtocolPolicy, model?: string) => void
}
export function ProtocolProbeDialog(props: Props) {
  const { t } = useTranslation()
  const caseNames = {
    basic: t('Basic'),
    assistant_history: t('Assistant history'),
    tool_cycle: t('Tool cycle'),
    reasoning_history: t('Reasoning history'),
    invalid_tool_id: t('Invalid tool ID'),
    tool_id_collision: t('Tool ID collision'),
  }
  const [model, setModel] = useState(props.model)
  const [scope, setScope] = useState<'channel' | 'model'>('channel')
  const [full, setFull] = useState(false)
  const [results, setResults] = useState<ChannelProtocolProbeResponse[]>([])
  const [running, setRunning] = useState(false)
  const controller = useRef<AbortController | null>(null)
  const snapshot = useRef('')
  useEffect(() => () => controller.current?.abort(), [])
  const cases = full ? probeCases : (['basic'] as const)
  const total = 8 * cases.length
  const done = results.length === total && !running
  const proposed =
    done && !full
      ? applyDetectedProtocols(
          props.policy,
          model,
          results,
          scope,
          props.normalize
        )
      : null
  async function run(retry: boolean) {
    const draft = props.getDraft()
    if (retry && snapshot.current !== JSON.stringify(draft)) {
      setResults([])
      toast.warning(t('Channel configuration changed. Run the probe again.'))
      return
    }
    const abort = new AbortController()
    controller.current = abort
    snapshot.current = JSON.stringify(draft)
    const retained = retry
      ? results.filter((r) => r.classification === 'confirmed')
      : []
    const tasks = TEXT_PROTOCOLS.flatMap((endpoint_type) =>
      REQUEST_MODES.flatMap((mode) =>
        cases.map((probe_case) => ({
          model,
          endpoint_type,
          stream: mode === 'stream',
          probe_case,
        }))
      )
    ).filter(
      (task) =>
        !retained.some(
          (r) =>
            r.endpoint_type === task.endpoint_type &&
            r.stream === task.stream &&
            r.probe_case === task.probe_case
        )
    )
    let cursor = 0
    setResults(retained)
    setRunning(true)
    async function worker() {
      while (cursor < tasks.length && !abort.signal.aborted) {
        const task = tasks[cursor++]
        let result: ChannelProtocolProbeResponse
        try {
          result = await probeChannelProtocolDraft(
            { ...draft, ...task },
            abort.signal
          )
          if (!result.classification) {
            throw new Error(result.message || t('Probe failed'))
          }
        } catch (error) {
          if (abort.signal.aborted) return
          result = {
            ...task,
            success: false,
            message: error instanceof Error ? error.message : t('Probe failed'),
            http_status: 0,
            classification: 'transport_error',
            capability_level: 'endpoint',
            effective_route_mode: 'native',
            recommended_mode: 'unsupported',
          }
        }
        if (!abort.signal.aborted) setResults((current) => [...current, result])
      }
    }
    try {
      await Promise.all([worker(), worker()])
    } finally {
      if (!abort.signal.aborted) setRunning(false)
    }
  }
  function confirm() {
    if (!done) {
      void run(false)
      return
    }
    if (!props.editable || full) {
      props.onClose()
      return
    }
    if (snapshot.current !== JSON.stringify(props.getDraft())) {
      setResults([])
      toast.warning(t('Channel configuration changed. Run the probe again.'))
      return
    }
    if (proposed) {
      props.onApply(proposed, scope === 'model' ? model : undefined)
      props.onClose()
    }
  }
  let actionLabel = t('Test Connection')
  if (done) {
    actionLabel =
      props.editable && !full ? t('Apply probe results') : t('Close')
  }
  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Protocol compatibility probe')}
      desc={t('Probe one model across four protocols in both request modes.')}
      confirmText={actionLabel}
      disabled={
        running ||
        !model ||
        props.disabled ||
        (done && props.editable && !full && !proposed)
      }
      handleConfirm={confirm}
    >
      <div className='max-h-[60vh] space-y-3 overflow-auto'>
        <NativeSelect
          aria-label={t('Model')}
          value={model}
          disabled={running}
          onChange={(e) => {
            setModel(e.target.value)
            setResults([])
          }}
        >
          {props.models.map((m) => (
            <option key={m}>{m}</option>
          ))}
        </NativeSelect>
        <label className='flex items-center gap-2'>
          <Checkbox
            checked={full}
            disabled={running}
            onCheckedChange={(v) => {
              setFull(v)
              setResults([])
            }}
          />
          {t('Run full compatibility probe')}
        </label>
        <div role='status'>
          {t('{{completed}} / {{total}} probes completed', {
            completed: results.length,
            total,
          })}
        </div>
        <StaticDataTable
          data={TEXT_PROTOCOLS}
          getRowKey={(e) => e}
          columns={[
            {
              id: 'protocol',
              header: t('Upstream protocol'),
              cell: (e) => textProtocolLabel(e, t),
            },
            ...REQUEST_MODES.map((key) => ({
              id: key,
              header: t(modeLabels[key]),
              cell: (e: TextEndpointType) => {
                const cells = results.filter(
                  (r) =>
                    r.endpoint_type === e && r.stream === (key === 'stream')
                )
                if (!cells.length) return '—'
                const failed = cells.filter(
                  (r) => r.classification !== 'confirmed'
                )
                if (failed.length) {
                  return (
                    <details>
                      <summary className='cursor-pointer text-amber-600'>
                        {t(classificationLabels[failed[0].classification])}
                      </summary>
                      {failed.map((r) => (
                        <p
                          key={r.probe_case}
                          className='max-w-48 text-xs break-words whitespace-normal'
                        >
                          {full ? `${caseNames[r.probe_case]} · ` : ''}
                          {r.message ||
                            t(classificationLabels[r.classification])}
                        </p>
                      ))}
                    </details>
                  )
                }
                return (
                  <span>
                    {t('Endpoint reachable')}
                    {full ? ` ${cells.length}/${cases.length}` : ''}
                  </span>
                )
              },
            })),
          ]}
        />
        {done && props.editable && !full && (
          <>
            <NativeSelect
              aria-label={t('Apply probe results')}
              value={scope}
              onChange={(e) => setScope(e.target.value as typeof scope)}
            >
              <option value='channel'>{t('Channel default')}</option>
              <option
                value='model'
                disabled={!Object.keys(props.policy.native).length}
              >
                {t('Model exception')} · {model}
              </option>
            </NativeSelect>
            <p className='text-muted-foreground text-xs'>
              {proposed
                ? t(
                    'Replace the selected scope with the reachable request modes.'
                  )
                : t('Check failures before applying this result.')}
            </p>
          </>
        )}
        {done && results.some((r) => r.classification !== 'confirmed') && (
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => void run(true)}
          >
            {t('Retry failed checks')}
          </Button>
        )}
      </div>
    </ConfirmDialog>
  )
}
