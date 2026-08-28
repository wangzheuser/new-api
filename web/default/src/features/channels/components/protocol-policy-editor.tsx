/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import {
  ChevronDown,
  Loader2,
  OctagonX,
  Play,
  Square,
  Wand2,
} from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import { probeChannelProtocol } from '../api'
import {
  applyProtocolProbeResults,
  createProtocolProbeBatch,
  isProtocolProbeBatchComplete,
  parseModelOverridesDraft,
  promoteCommonModelProtocolCapabilities,
  protocolCapabilityState,
  protocolProbeKey,
  PROTOCOL_PROBE_CASES,
  summarizeModelProtocolOverrides,
  supportsNormalizedProtocol,
  TEXT_PROTOCOLS,
  updateProtocolCapabilityState,
  type ProtocolCapabilityState,
  type ProtocolProbeBatch,
  type ProtocolProbeCase,
  type ProtocolProbeResultMap,
  type ProtocolRequestMode,
} from '../lib/protocol-policy'
import type {
  ChannelProtocolProbeResponse,
  ChannelProtocolPolicy,
  ProtocolCapability,
  TextEndpointType,
} from '../types'

const PROBE_CONCURRENCY = 2
const MAX_PROBE_MODELS = 10

type ProtocolPolicyEditorProps = {
  channelId?: number
  channelType: number
  models: string[]
  value?: string
  disabled?: boolean
  onChange: (value: string) => void
}

function createDefaultPolicy(): ChannelProtocolPolicy {
  return {
    native: {
      openai: { non_stream: true, stream: true },
    },
    model_overrides: {},
    auto_convert: true,
    max_quality: 'fair',
  }
}

function parsePolicy(value?: string): ChannelProtocolPolicy | null {
  if (!value?.trim()) return null
  try {
    const parsed = JSON.parse(value) as ChannelProtocolPolicy
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null
    }
    return parsed
  } catch {
    return null
  }
}

function capabilityFor(
  policy: ChannelProtocolPolicy,
  endpointType: TextEndpointType
): ProtocolCapability {
  return policy.native[endpointType] ?? { non_stream: false, stream: false }
}

function classificationTone(
  classification: ChannelProtocolProbeResponse['classification']
) {
  switch (classification) {
    case 'confirmed':
      return 'border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
    case 'path_mismatch':
      return 'border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300'
    default:
      return 'border-destructive/40 bg-destructive/10 text-destructive'
  }
}

function capabilityStateTone(state: ProtocolCapabilityState) {
  switch (state) {
    case 'native':
      return 'border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-300'
    case 'normalized':
      return 'border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
    default:
      return 'border-border bg-muted/40 text-muted-foreground'
  }
}

export function ProtocolPolicyEditor({
  channelId,
  channelType,
  models,
  value,
  disabled,
  onChange,
}: ProtocolPolicyEditorProps) {
  const { t } = useTranslation()
  const protocolLabels: Record<TextEndpointType, string> = {
    openai: t('Chat'),
    'openai-response': t('OpenAI Responses'),
    anthropic: t('Claude Messages'),
    gemini: t('Gemini Generate Content'),
  }
  const probeCaseLabels: Record<ProtocolProbeCase, string> = {
    basic: t('Basic'),
    assistant_history: t('Assistant history'),
    tool_cycle: t('Tool cycle'),
    reasoning_history: t('Reasoning history'),
    invalid_tool_id: t('Invalid tool ID'),
    tool_id_collision: t('Tool ID collision'),
  }
  const policy = useMemo(() => parsePolicy(value), [value])
  const policyEditable = channelType === 1
  const probeSupported = channelType === 1 || channelType === 58
  const [selectedModels, setSelectedModels] = useState<string[]>([])
  const [probeResults, setProbeResults] = useState<ProtocolProbeResultMap>({})
  const [probeBatch, setProbeBatch] = useState<ProtocolProbeBatch | null>(null)
  const [probeProgress, setProbeProgress] = useState({ completed: 0, total: 0 })
  const [isProbing, setIsProbing] = useState(false)
  const [overridesDraft, setOverridesDraft] = useState('{}')
  const [defaultProtocolsOpen, setDefaultProtocolsOpen] = useState(false)
  const [fullProbeConfirmOpen, setFullProbeConfirmOpen] = useState(false)
  const stopRequestedRef = useRef(false)

  useEffect(() => {
    setOverridesDraft(JSON.stringify(policy?.model_overrides ?? {}, null, 2))
  }, [policy?.model_overrides])

  const writePolicy = (nextPolicy: ChannelProtocolPolicy | null) => {
    onChange(nextPolicy ? JSON.stringify(nextPolicy, null, 2) : '')
  }

  const updateCapability = (
    endpointType: TextEndpointType,
    requestMode: ProtocolRequestMode,
    state: ProtocolCapabilityState
  ) => {
    const nextPolicy = structuredClone(policy ?? createDefaultPolicy())
    const capability = capabilityFor(nextPolicy, endpointType)
    const nextCapability = updateProtocolCapabilityState(
      capability,
      endpointType,
      requestMode,
      state
    )
    if (!nextCapability) {
      delete nextPolicy.native[endpointType]
    } else {
      nextPolicy.native[endpointType] = nextCapability
    }
    writePolicy(nextPolicy)
  }

  const applyOverridesDraft = () => {
    const parsedDraft = parseModelOverridesDraft(overridesDraft)
    if (!parsedDraft.success) {
      toast.error(
        t(
          parsedDraft.error === 'not_object'
            ? 'Model overrides must be a JSON object'
            : 'Invalid model overrides'
        )
      )
      return
    }

    const nextPolicy = structuredClone(policy ?? createDefaultPolicy())
    nextPolicy.model_overrides = parsedDraft.value
    writePolicy(nextPolicy)
    toast.success(t('Model protocol overrides updated in the current form'))
  }

  const toggleSelectedModel = (model: string, checked: boolean) => {
    setSelectedModels((current) => {
      if (!checked) return current.filter((item) => item !== model)
      if (current.includes(model)) return current
      if (current.length >= MAX_PROBE_MODELS) {
        toast.error(t('Select at most 10 models'))
        return current
      }
      return [...current, model]
    })
  }

  const runProtocolProbe = async (capabilityLevel: 'endpoint' | 'semantic') => {
    if (!channelId || selectedModels.length === 0) return
    const batch = createProtocolProbeBatch(selectedModels, capabilityLevel)
    const tasks = batch.models.flatMap((model) =>
      TEXT_PROTOCOLS.flatMap((endpointType) =>
        [false, true].flatMap((stream) =>
          batch.probeCases.map((probeCase) => ({
            model,
            endpointType,
            stream,
            probeCase,
          }))
        )
      )
    )
    stopRequestedRef.current = false
    setProbeResults({})
    setProbeBatch(batch)
    setProbeProgress({ completed: 0, total: tasks.length })
    setIsProbing(true)
    let cursor = 0

    const worker = async () => {
      while (!stopRequestedRef.current) {
        const index = cursor
        cursor += 1
        const task = tasks[index]
        if (!task) return
        try {
          const result = await probeChannelProtocol(channelId, {
            model: task.model,
            endpoint_type: task.endpointType,
            stream: task.stream,
            probe_mode: 'native',
            probe_case: task.probeCase,
          })
          setProbeResults((current) => ({
            ...current,
            [protocolProbeKey(
              task.model,
              task.endpointType,
              task.stream,
              task.probeCase
            )]: result,
          }))
        } catch (error) {
          const message =
            error instanceof Error ? error.message : t('Probe failed')
          setProbeResults((current) => ({
            ...current,
            [protocolProbeKey(
              task.model,
              task.endpointType,
              task.stream,
              task.probeCase
            )]: {
              success: false,
              message,
              model: task.model,
              endpoint_type: task.endpointType,
              stream: task.stream,
              http_status: 0,
              classification: 'transport_error',
              probe_case: task.probeCase,
              capability_level:
                task.probeCase === 'basic' ? 'endpoint' : 'semantic',
              effective_route_mode: 'native',
              recommended_mode: 'unsupported',
            },
          }))
        } finally {
          setProbeProgress((current) => ({
            ...current,
            completed: current.completed + 1,
          }))
        }
      }
    }

    await Promise.all(Array.from({ length: PROBE_CONCURRENCY }, worker))
    setIsProbing(false)
    if (stopRequestedRef.current) {
      setProbeBatch((current) =>
        current === batch ? { ...batch, stopped: true } : current
      )
      toast.info(t('Protocol probe stopped'))
    } else {
      toast.success(t('Protocol probe completed'))
    }
  }

  const applyProbeResults = () => {
    if (!policyEditable || !probeBatch) return

    const parsedDraft = parseModelOverridesDraft(overridesDraft)
    if (!parsedDraft.success) {
      toast.error(
        t(
          parsedDraft.error === 'not_object'
            ? 'Model overrides must be a JSON object'
            : 'Invalid model overrides'
        )
      )
      return
    }

    const overrides = applyProtocolProbeResults(
      parsedDraft.value,
      probeBatch,
      probeResults
    )
    if (!overrides) return

    const nextPolicy = structuredClone(policy ?? createDefaultPolicy())
    nextPolicy.model_overrides = overrides
    writePolicy(nextPolicy)
    toast.success(t('Probe results applied to the current form'))
  }

  const completedResults = Object.keys(probeResults).length
  const probeBatchComplete = isProtocolProbeBatchComplete(
    probeBatch,
    probeResults
  )
  const parsedOverridesDraft = useMemo(
    () => parseModelOverridesDraft(overridesDraft),
    [overridesDraft]
  )
  const visibleOverrides = parsedOverridesDraft.success
    ? parsedOverridesDraft.value
    : {}
  const coverageSummary = summarizeModelProtocolOverrides(
    models,
    visibleOverrides
  )
  const commonCapabilitiesPromotion = promoteCommonModelProtocolCapabilities(
    models,
    visibleOverrides
  )
  const commonCapabilitiesPromotionAvailable = Boolean(
    commonCapabilitiesPromotion &&
    (TEXT_PROTOCOLS.some((endpointType) => {
      const currentCapability = policy?.native[endpointType]
      const promotedCapability =
        commonCapabilitiesPromotion.native[endpointType]
      return (
        protocolCapabilityState(currentCapability, 'non_stream') !==
          protocolCapabilityState(promotedCapability, 'non_stream') ||
        protocolCapabilityState(currentCapability, 'stream') !==
          protocolCapabilityState(promotedCapability, 'stream') ||
        currentCapability?.reasoning_history !==
          promotedCapability?.reasoning_history
      )
    }) ||
      Object.keys(commonCapabilitiesPromotion.modelOverrides).length !==
        Object.keys(visibleOverrides).length)
  )
  const defaultProtocolLabels = TEXT_PROTOCOLS.filter((endpointType) => {
    const capability = policy?.native[endpointType]
    return capability?.non_stream || capability?.stream
  }).map((endpointType) => {
    const capability = policy?.native[endpointType]
    const mode =
      capability?.mode === 'normalized'
        ? t('Compatible normalization')
        : t('Native passthrough')
    return `${protocolLabels[endpointType]} · ${mode}`
  })

  const promoteCommonCapabilities = () => {
    if (!policy || !commonCapabilitiesPromotion) return

    const nextPolicy = structuredClone(policy)
    nextPolicy.native = commonCapabilitiesPromotion.native
    nextPolicy.model_overrides = commonCapabilitiesPromotion.modelOverrides
    writePolicy(nextPolicy)
    setDefaultProtocolsOpen(true)
    toast.success(t('Common model capabilities set as channel default'))
  }

  return (
    <div className='space-y-5'>
      {policyEditable && (
        <div className='space-y-4'>
          <div className='flex items-center justify-between gap-4'>
            <div>
              <Label>{t('Enable protocol policy')}</Label>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t('Leave disabled to preserve legacy channel routing')}
              </p>
            </div>
            <Switch
              checked={policy !== null}
              disabled={disabled}
              onCheckedChange={(checked) =>
                writePolicy(checked ? createDefaultPolicy() : null)
              }
            />
          </div>

          {policy && (
            <>
              {coverageSummary.coveredModels > 0 && (
                <div className='space-y-2'>
                  <div>
                    <Label>{t('Model protocol capability summary')}</Label>
                    <p className='text-muted-foreground mt-1 text-xs'>
                      {t(
                        '{{covered}} of {{total}} channel models have protocol overrides',
                        {
                          covered: coverageSummary.coveredModels,
                          total: coverageSummary.totalModels,
                        }
                      )}
                    </p>
                    <p className='text-muted-foreground mt-1 text-xs'>
                      {t(
                        'This summary shows model overrides and does not change channel defaults'
                      )}
                    </p>
                    <p className='text-muted-foreground mt-1 text-xs'>
                      {t(
                        'Coverage means an explicit configuration exists; it does not prove semantic compatibility'
                      )}
                    </p>
                  </div>
                  <div className='overflow-hidden rounded-md border'>
                    <div className='bg-muted/40 grid grid-cols-[minmax(140px,1fr)_110px_110px] gap-2 px-3 py-2 text-xs font-medium'>
                      <span>{t('Protocol capability')}</span>
                      <span>{t('Normal')}</span>
                      <span>{t('Streaming')}</span>
                    </div>
                    {TEXT_PROTOCOLS.map((protocol) => {
                      const counts = coverageSummary.capabilities[protocol]
                      const total = coverageSummary.coveredModels
                      return (
                        <div
                          key={protocol}
                          className='grid grid-cols-[minmax(140px,1fr)_110px_110px] items-center gap-2 border-t px-3 py-2'
                        >
                          <span className='text-sm font-medium'>
                            {protocolLabels[protocol]}
                          </span>
                          <div className='flex flex-wrap items-center gap-1'>
                            <Badge variant='outline' className='text-[10px]'>
                              {t('Native')} {counts.nonStream.native}/{total}
                            </Badge>
                            <Badge variant='outline' className='text-[10px]'>
                              {t('Normalized')} {counts.nonStream.normalized}/
                              {total}
                            </Badge>
                          </div>
                          <div className='flex flex-wrap items-center gap-1'>
                            <Badge variant='outline' className='text-[10px]'>
                              {t('Native')} {counts.stream.native}/{total}
                            </Badge>
                            <Badge variant='outline' className='text-[10px]'>
                              {t('Normalized')} {counts.stream.normalized}/
                              {total}
                            </Badge>
                          </div>
                        </div>
                      )
                    })}
                  </div>
                  {commonCapabilitiesPromotionAvailable && (
                    <div className='bg-muted/30 flex flex-wrap items-center justify-between gap-3 rounded-md border px-3 py-2'>
                      <p className='text-muted-foreground text-xs'>
                        {t(
                          'All channel models are covered; their common capabilities can be used as the channel default'
                        )}
                      </p>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        disabled={disabled}
                        onClick={promoteCommonCapabilities}
                      >
                        {t('Set common capabilities as channel default')}
                      </Button>
                    </div>
                  )}
                </div>
              )}

              <Collapsible
                open={defaultProtocolsOpen}
                onOpenChange={setDefaultProtocolsOpen}
                className='overflow-hidden rounded-md border'
              >
                <CollapsibleTrigger className='hover:bg-muted/40 flex w-full items-center justify-between gap-3 px-3 py-2.5 text-left'>
                  <div>
                    <div className='text-sm font-medium'>
                      {t('Default protocols for uncovered models')}
                    </div>
                    <p className='text-muted-foreground mt-1 text-xs'>
                      {t(
                        'Used only by models without a model protocol override'
                      )}
                    </p>
                  </div>
                  <div className='flex shrink-0 items-center gap-2'>
                    <Badge variant='outline'>
                      {defaultProtocolLabels.join(', ')}
                    </Badge>
                    <ChevronDown
                      className={`text-muted-foreground h-4 w-4 transition-transform ${
                        defaultProtocolsOpen ? 'rotate-180' : ''
                      }`}
                    />
                  </div>
                </CollapsibleTrigger>
                <CollapsibleContent className='border-t'>
                  <div className='bg-muted/40 grid grid-cols-[minmax(140px,1fr)_150px_150px] gap-2 px-3 py-2 text-xs font-medium'>
                    <span>{t('Protocol capability')}</span>
                    <span>{t('Normal')}</span>
                    <span>{t('Streaming')}</span>
                  </div>
                  {TEXT_PROTOCOLS.map((protocol) => {
                    const capability = capabilityFor(policy, protocol)
                    return (
                      <div
                        key={protocol}
                        className='grid grid-cols-[minmax(140px,1fr)_150px_150px] items-center gap-2 border-t px-3 py-2'
                      >
                        <span className='text-sm font-medium'>
                          {protocolLabels[protocol]}
                        </span>
                        <Select
                          value={protocolCapabilityState(
                            capability,
                            'non_stream'
                          )}
                          disabled={disabled}
                          onValueChange={(state) =>
                            updateCapability(
                              protocol,
                              'non_stream',
                              state as ProtocolCapabilityState
                            )
                          }
                        >
                          <SelectTrigger className='h-8'>
                            <SelectValue />
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
                              disabled={!supportsNormalizedProtocol(protocol)}
                            >
                              {t('Compatible normalization')}
                            </SelectItem>
                          </SelectContent>
                        </Select>
                        <Select
                          value={protocolCapabilityState(capability, 'stream')}
                          disabled={disabled}
                          onValueChange={(state) =>
                            updateCapability(
                              protocol,
                              'stream',
                              state as ProtocolCapabilityState
                            )
                          }
                        >
                          <SelectTrigger className='h-8'>
                            <SelectValue />
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
                              disabled={!supportsNormalizedProtocol(protocol)}
                            >
                              {t('Compatible normalization')}
                            </SelectItem>
                          </SelectContent>
                        </Select>
                      </div>
                    )
                  })}
                  <div className='text-muted-foreground border-t px-3 py-2 text-xs'>
                    {t(
                      'Normal and streaming availability are independent; one protocol shares the selected handling mode'
                    )}
                  </div>
                </CollapsibleContent>
              </Collapsible>

              <div className='grid gap-4 md:grid-cols-2'>
                <div className='flex items-start justify-between gap-3 rounded-md border px-3 py-2'>
                  <div>
                    <Label>{t('Automatic protocol conversion')}</Label>
                    <p className='text-muted-foreground mt-1 text-xs'>
                      {t(
                        'Used only when the requested client protocol is unavailable'
                      )}
                    </p>
                  </div>
                  <Switch
                    checked={policy.auto_convert}
                    disabled={disabled}
                    onCheckedChange={(checked) =>
                      writePolicy({ ...policy, auto_convert: checked })
                    }
                  />
                </div>
                <div className='space-y-2'>
                  <Label>{t('Maximum conversion quality')}</Label>
                  <Select
                    value={policy.max_quality || 'fair'}
                    disabled={disabled}
                    onValueChange={(quality) => {
                      if (quality) {
                        writePolicy({ ...policy, max_quality: quality })
                      }
                    }}
                  >
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value='good'>{t('Good only')}</SelectItem>
                      <SelectItem value='fair'>{t('Good and fair')}</SelectItem>
                    </SelectContent>
                  </Select>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'This limit applies to cross-protocol conversion, not compatible normalization'
                    )}
                  </p>
                </div>
              </div>

              <div className='space-y-2'>
                <div className='flex items-center justify-between gap-2'>
                  <Label>{t('Model protocol overrides')}</Label>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={disabled}
                    onClick={applyOverridesDraft}
                  >
                    {t('Apply JSON')}
                  </Button>
                </div>
                <Textarea
                  value={overridesDraft}
                  disabled={disabled}
                  rows={6}
                  className='font-mono text-xs'
                  onChange={(event) => setOverridesDraft(event.target.value)}
                />
              </div>
            </>
          )}
        </div>
      )}

      {probeSupported && (
        <div className='space-y-4 border-t pt-4'>
          <div className='flex flex-wrap items-start justify-between gap-3'>
            <div>
              <Label>{t('Protocol compatibility probe')}</Label>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  'Endpoint reachability is separate from full semantic compatibility'
                )}
              </p>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  'Tests four text protocols in normal and streaming mode with concurrency 2'
                )}
              </p>
            </div>
            <div className='flex flex-wrap gap-2'>
              {isProbing ? (
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  onClick={() => {
                    stopRequestedRef.current = true
                  }}
                >
                  <Square className='mr-2 h-3.5 w-3.5' />
                  {t('Stop')}
                </Button>
              ) : (
                <>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={
                      disabled || !channelId || selectedModels.length === 0
                    }
                    onClick={() => void runProtocolProbe('endpoint')}
                  >
                    <Play className='mr-2 h-3.5 w-3.5' />
                    {t('Check endpoint reachability')}
                  </Button>
                  <Button
                    type='button'
                    size='sm'
                    disabled={
                      disabled || !channelId || selectedModels.length === 0
                    }
                    onClick={() => setFullProbeConfirmOpen(true)}
                  >
                    <Play className='mr-2 h-3.5 w-3.5' />
                    {t('Run full compatibility probe')}
                  </Button>
                </>
              )}
              {policyEditable && (
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={
                    disabled ||
                    isProbing ||
                    !probeBatchComplete ||
                    probeBatch?.capabilityLevel !== 'semantic'
                  }
                  onClick={applyProbeResults}
                >
                  <Wand2 className='mr-2 h-3.5 w-3.5' />
                  {t('Apply probe results')}
                </Button>
              )}
            </div>
          </div>

          {!channelId && (
            <div className='text-muted-foreground rounded-md border border-dashed px-3 py-2 text-xs'>
              {t('Save the channel before running a protocol probe')}
            </div>
          )}

          <div className='grid gap-2 text-xs md:grid-cols-3'>
            <div className='bg-muted/30 rounded-md border px-3 py-2'>
              <div className='font-medium'>{t('Endpoint reachable')}</div>
              <p className='text-muted-foreground mt-1'>
                {t('A basic request reached the selected upstream endpoint')}
              </p>
            </div>
            <div className='bg-muted/30 rounded-md border px-3 py-2'>
              <div className='font-medium'>{t('Native passthrough')}</div>
              <p className='text-muted-foreground mt-1'>
                {t(
                  'The request keeps its original protocol and message semantics'
                )}
              </p>
            </div>
            <div className='bg-muted/30 rounded-md border px-3 py-2'>
              <div className='font-medium'>{t('Compatible normalization')}</div>
              <p className='text-muted-foreground mt-1'>
                {t(
                  'The request keeps its protocol and path while known structural issues are corrected'
                )}
              </p>
            </div>
          </div>

          <div className='max-h-40 space-y-1 overflow-y-auto rounded-md border p-2'>
            {models.length === 0 ? (
              <p className='text-muted-foreground px-2 py-3 text-xs'>
                {t('Add channel models before probing')}
              </p>
            ) : (
              models.map((model) => (
                <label
                  key={model}
                  className='hover:bg-muted/60 flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm'
                >
                  <Checkbox
                    checked={selectedModels.includes(model)}
                    disabled={disabled || isProbing}
                    onCheckedChange={(checked) =>
                      toggleSelectedModel(model, checked === true)
                    }
                  />
                  <span className='truncate'>{model}</span>
                </label>
              ))
            )}
          </div>

          {isProbing && (
            <div className='text-muted-foreground flex items-center gap-2 text-xs'>
              <Loader2 className='h-3.5 w-3.5 animate-spin' />
              {t('{{completed}} / {{total}} probes completed', probeProgress)}
            </div>
          )}

          {completedResults > 0 && (
            <div className='space-y-3'>
              {probeBatch?.models.map((model) => (
                <div key={model} className='space-y-2 rounded-md border p-3'>
                  <div className='truncate text-sm font-medium'>{model}</div>
                  <div className='grid gap-2 sm:grid-cols-2'>
                    {TEXT_PROTOCOLS.map((protocol) => {
                      return (
                        <div
                          key={protocol}
                          className='bg-muted/30 rounded px-2.5 py-2 text-xs'
                        >
                          <div className='mb-1.5 font-medium'>
                            {protocolLabels[protocol]}
                          </div>
                          {([false, true] as const).map((stream) => {
                            const modeLabel = stream
                              ? t('Streaming')
                              : t('Normal')
                            const caseResults = (
                              probeBatch?.probeCases ?? PROTOCOL_PROBE_CASES
                            ).map((probeCase) => ({
                              probeCase,
                              result:
                                probeResults[
                                  protocolProbeKey(
                                    model,
                                    protocol,
                                    stream,
                                    probeCase
                                  )
                                ],
                            }))
                            const semanticResults = caseResults
                              .filter(({ probeCase }) => probeCase !== 'basic')
                              .map(({ result }) => result)
                            const recommendations = new Set(
                              semanticResults.map(
                                (result) => result?.recommended_mode
                              )
                            )
                            let recommendation:
                              | 'native'
                              | 'normalized'
                              | undefined
                            if (
                              semanticResults.length > 0 &&
                              semanticResults.every(
                                (result) =>
                                  result?.classification === 'confirmed' &&
                                  result.capability_level === 'semantic' &&
                                  result.recommended_mode !== 'unsupported'
                              ) &&
                              recommendations.size === 1
                            ) {
                              const onlyRecommendation =
                                semanticResults[0]?.recommended_mode
                              if (
                                onlyRecommendation === 'native' ||
                                onlyRecommendation === 'normalized'
                              ) {
                                recommendation = onlyRecommendation
                              }
                            }
                            const state: ProtocolCapabilityState =
                              recommendation === 'native' ||
                              recommendation === 'normalized'
                                ? recommendation
                                : 'unavailable'
                            let stateLabel = t('Unsupported')
                            if (state === 'normalized') {
                              stateLabel = t('Normalized')
                            } else if (state === 'native') {
                              stateLabel = t('Native')
                            }
                            return (
                              <div
                                key={String(stream)}
                                className='mt-2 border-t pt-2 first:mt-0 first:border-t-0 first:pt-0'
                              >
                                <div className='mb-1 flex items-center justify-between gap-2'>
                                  <span className='text-muted-foreground'>
                                    {modeLabel}
                                  </span>
                                  {probeBatch?.capabilityLevel ===
                                    'semantic' && (
                                    <Badge
                                      variant='outline'
                                      className={capabilityStateTone(state)}
                                    >
                                      {stateLabel}
                                    </Badge>
                                  )}
                                </div>
                                <div className='flex flex-wrap gap-1'>
                                  {caseResults.map(
                                    ({ probeCase, result: typedResult }) => (
                                      <Badge
                                        key={probeCase}
                                        variant='outline'
                                        title={typedResult?.message}
                                        className={
                                          typedResult
                                            ? classificationTone(
                                                typedResult.classification
                                              )
                                            : undefined
                                        }
                                      >
                                        {probeCaseLabels[probeCase]}:{' '}
                                        {typedResult?.classification ?? '—'}
                                      </Badge>
                                    )
                                  )}
                                </div>
                              </div>
                            )
                          })}
                        </div>
                      )
                    })}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      <ConfirmDialog
        open={fullProbeConfirmOpen}
        onOpenChange={setFullProbeConfirmOpen}
        title={t('Run full compatibility probe?')}
        desc={t(
          'This administrator-only probe sends real upstream requests for every selected model, protocol, mode, and semantic scenario. It may incur a small usage charge.'
        )}
        confirmText={t('Run full probe')}
        handleConfirm={() => {
          setFullProbeConfirmOpen(false)
          void runProtocolProbe('semantic')
        }}
      />

      {!policyEditable && !probeSupported && (
        <div className='text-muted-foreground flex items-center gap-2 text-sm'>
          <OctagonX className='h-4 w-4' />
          {t('Protocol policy is available for standard compatible channels')}
        </div>
      )}
    </div>
  )
}
