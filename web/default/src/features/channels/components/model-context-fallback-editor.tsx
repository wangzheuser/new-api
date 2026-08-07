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
import { useQuery } from '@tanstack/react-query'
import {
  AlertTriangle,
  ArrowDown,
  ArrowRight,
  ArrowUp,
  ChevronDown,
  Code,
  Plus,
  Route,
  Search,
  Settings2,
  Trash2,
} from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Slider } from '@/components/ui/slider'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useDebounce } from '@/hooks/use-debounce'
import { cn } from '@/lib/utils'

import { getChannel, searchChannels } from '../api'
import {
  calculateContextFallbackTriggerTokens,
  createEmptyContextFallbackRule,
  MAX_CONTEXT_FALLBACK_RULES,
  parseContextFallbackValue,
  serializeContextFallbackDrafts,
  type ContextFallbackRuleDraft,
  type ContextFallbackRuleField,
  type ContextFallbackValidationError,
  validateContextFallbackDrafts,
} from '../lib/model-context-fallback'
import type { Channel } from '../types'

type ModelContextFallbackEditorProps = {
  value: string
  onChange: (value: string) => void
  sourceModels: string[]
  fallbackModels: string[]
  currentChannelId?: number
  currentGroups: string[]
  disabled?: boolean
  onValidityChange: (error: string | null) => void
}

type RuleCardProps = {
  rule: ContextFallbackRuleDraft
  errors: ContextFallbackValidationError[]
  expanded: boolean
  disabled: boolean
  sourceModels: string[]
  fallbackModels: string[]
  currentChannelId?: number
  currentGroups: string[]
  onExpandedChange: () => void
  onChange: (rule: ContextFallbackRuleDraft) => void
  onDelete: () => void
}

type TargetChannel = {
  id: number
  name: string
  group: string
  models: string
  status: number
  missing?: boolean
}

function splitCommaList(value: string): string[] {
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

function groupsAreCompatible(sourceGroups: string[], targetGroup: string) {
  const targets = splitCommaList(targetGroup)
  return (
    sourceGroups.includes('all') ||
    targets.includes('all') ||
    targets.some((group) => sourceGroups.includes(group))
  )
}

function channelSupportsModel(channel: TargetChannel, model: string) {
  return splitCommaList(channel.models).includes(model)
}

function toTargetChannel(channel: Channel): TargetChannel {
  return {
    id: channel.id,
    name: channel.name,
    group: channel.group,
    models: channel.models,
    status: channel.status,
  }
}

function getRuleFieldError(
  errors: ContextFallbackValidationError[],
  field: ContextFallbackRuleField
) {
  return errors.find((error) => error.field === field)
}

function RuleCard(props: RuleCardProps) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const debouncedSearch = useDebounce(search, 300)
  const triggerTokens = calculateContextFallbackTriggerTokens(
    props.rule.sourceContextWindowTokens,
    props.rule.thresholdPercent
  )
  const sourceModelSet = useMemo(
    () => new Set(props.sourceModels),
    [props.sourceModels]
  )
  const fallbackModelSet = useMemo(
    () => new Set(props.fallbackModels),
    [props.fallbackModels]
  )
  const sourceOptions = useMemo(
    () =>
      [...new Set([...props.sourceModels, props.rule.sourceModel])]
        .filter(Boolean)
        .map((model) => ({ value: model, label: model })),
    [props.rule.sourceModel, props.sourceModels]
  )
  const fallbackOptions = useMemo(
    () =>
      [...new Set([...props.fallbackModels, props.rule.fallbackModel])]
        .filter(Boolean)
        .map((model) => ({ value: model, label: model })),
    [props.fallbackModels, props.rule.fallbackModel]
  )
  const targetEditorVisible =
    props.expanded &&
    props.rule.routeMode === 'cross_channel' &&
    props.rule.targetMode === 'limited'

  const targetSearchQuery = useQuery({
    queryKey: [
      'context-fallback-targets',
      debouncedSearch,
      props.rule.fallbackModel,
    ],
    queryFn: async () => {
      const response = await searchChannels({
        keyword: debouncedSearch || undefined,
        model: props.rule.fallbackModel,
        status: 'enabled',
        p: 1,
        page_size: 50,
      })
      return (response.data?.items || [])
        .map(toTargetChannel)
        .filter(
          (channel) =>
            channel.id !== props.currentChannelId &&
            channelSupportsModel(channel, props.rule.fallbackModel)
        )
    },
    enabled: targetEditorVisible && Boolean(props.rule.fallbackModel.trim()),
  })

  const selectedTargetsQuery = useQuery({
    queryKey: [
      'context-fallback-selected-targets',
      props.rule.targetChannelIds,
    ],
    queryFn: async () =>
      Promise.all(
        props.rule.targetChannelIds.map(async (id): Promise<TargetChannel> => {
          try {
            const response = await getChannel(id)
            if (response.data) return toTargetChannel(response.data)
          } catch {
            // Keep stale IDs visible so an administrator can remove them.
          }
          return {
            id,
            name: t('Unavailable channel'),
            group: '',
            models: '',
            status: 0,
            missing: true,
          }
        })
      ),
    enabled: targetEditorVisible && props.rule.targetChannelIds.length > 0,
  })

  const selectedTargets = useMemo(() => {
    const resolved = new Map(
      (selectedTargetsQuery.data || []).map((channel) => [channel.id, channel])
    )
    return props.rule.targetChannelIds.map(
      (id) =>
        resolved.get(id) ?? {
          id,
          name: t('Loading channel'),
          group: '',
          models: '',
          status: 1,
        }
    )
  }, [props.rule.targetChannelIds, selectedTargetsQuery.data, t])

  const updateRule = (
    values: Partial<ContextFallbackRuleDraft>,
    clearTargets = false
  ) => {
    props.onChange({
      ...props.rule,
      ...values,
      targetChannelIds: clearTargets ? [] : props.rule.targetChannelIds,
    })
  }

  const fieldError = (field: ContextFallbackRuleField) =>
    getRuleFieldError(props.errors, field)
  const fieldDescriptionId = (field: ContextFallbackRuleField) =>
    `context-fallback-${props.rule.id}-${field}-description`
  const fieldInputId = (field: ContextFallbackRuleField) =>
    `context-fallback-${props.rule.id}-${field}`
  const thresholdNumber = Number(props.rule.thresholdPercent)
  const sliderValue =
    Number.isInteger(thresholdNumber) &&
    thresholdNumber >= 1 &&
    thresholdNumber <= 100
      ? thresholdNumber
      : 90
  let sourceModelDescription = t(
    'Requests using this model are evaluated by the rule.'
  )
  if (fieldError('sourceModel')) {
    sourceModelDescription = t(fieldError('sourceModel')?.message || '')
  } else if (
    !sourceModelSet.has(props.rule.sourceModel) &&
    props.rule.sourceModel
  ) {
    sourceModelDescription = t('Custom or unpublished model')
  }
  let thresholdDescription = t(
    'Enter a valid window and threshold to calculate tokens.'
  )
  if (fieldError('thresholdPercent')) {
    thresholdDescription = t(fieldError('thresholdPercent')?.message || '')
  } else if (triggerTokens != null) {
    thresholdDescription = t('Fallback starts at {{tokens}} tokens.', {
      tokens: triggerTokens.toLocaleString(),
    })
  }
  let fallbackModelDescription = t(
    'The request keeps its original model price.'
  )
  if (fieldError('fallbackModel')) {
    fallbackModelDescription = t(fieldError('fallbackModel')?.message || '')
  } else if (
    !fallbackModelSet.has(props.rule.fallbackModel) &&
    props.rule.fallbackModel
  ) {
    fallbackModelDescription = t('Custom or unpublished model')
  }
  let summaryStrategy = t('the selected channel order')
  if (props.rule.routeMode === 'same_channel') {
    summaryStrategy = t('the same channel')
  } else if (props.rule.targetMode === 'auto') {
    summaryStrategy = t('automatic group routing')
  }

  const reorderTarget = (index: number, direction: -1 | 1) => {
    const targetIndex = index + direction
    if (targetIndex < 0 || targetIndex >= props.rule.targetChannelIds.length) {
      return
    }
    const ids = [...props.rule.targetChannelIds]
    ;[ids[index], ids[targetIndex]] = [ids[targetIndex], ids[index]]
    props.onChange({ ...props.rule, targetChannelIds: ids })
  }

  const removeTarget = (id: number) => {
    props.onChange({
      ...props.rule,
      targetChannelIds: props.rule.targetChannelIds.filter(
        (targetId) => targetId !== id
      ),
    })
  }

  return (
    <Collapsible open={props.expanded} onOpenChange={props.onExpandedChange}>
      <div
        className={cn(
          'bg-card overflow-hidden rounded-lg border',
          props.errors.length > 0 ? 'border-destructive/60' : 'border-border/70'
        )}
      >
        <div className='flex items-center gap-2 px-3 py-2.5'>
          <CollapsibleTrigger
            className='focus-visible:ring-ring flex min-w-0 flex-1 items-center gap-3 rounded-md text-left outline-none focus-visible:ring-2'
            disabled={props.disabled}
          >
            <ChevronDown
              className={cn(
                'text-muted-foreground size-4 shrink-0 transition-transform',
                props.expanded && 'rotate-180'
              )}
              aria-hidden='true'
            />
            <span className='min-w-0 flex-1'>
              <span className='flex min-w-0 items-center gap-2 text-sm font-medium'>
                <span className='truncate'>
                  {props.rule.sourceModel || t('Source model')}
                </span>
                <ArrowRight className='size-3.5 shrink-0' aria-hidden='true' />
                <span className='truncate'>
                  {props.rule.fallbackModel || t('Fallback model')}
                </span>
              </span>
              <span className='text-muted-foreground mt-1 flex flex-wrap items-center gap-1.5 text-xs'>
                <span>
                  {t('{{percent}}% threshold', {
                    percent: props.rule.thresholdPercent || '—',
                  })}
                </span>
                <Badge variant='outline' className='h-5 px-1.5 text-[10px]'>
                  {props.rule.routeMode === 'same_channel'
                    ? t('Same channel')
                    : t('Cross channel')}
                </Badge>
                {props.rule.routeMode === 'cross_channel' && (
                  <span>
                    {props.rule.targetMode === 'auto'
                      ? t('Automatic group routing')
                      : t('{{count}} selected channel(s)', {
                          count: props.rule.targetChannelIds.length,
                        })}
                  </span>
                )}
              </span>
            </span>
            {Object.keys(props.rule.extra).length > 0 && (
              <Badge variant='secondary' className='shrink-0'>
                {t('Advanced fields')}
              </Badge>
            )}
            {props.errors.length > 0 && (
              <AlertTriangle
                className='text-destructive size-4 shrink-0'
                aria-label={t('Rule has errors')}
              />
            )}
          </CollapsibleTrigger>
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  disabled={props.disabled}
                  onClick={props.onDelete}
                  aria-label={t('Delete fallback rule')}
                />
              }
            >
              <Trash2 className='size-4' aria-hidden='true' />
            </TooltipTrigger>
            <TooltipContent>{t('Delete fallback rule')}</TooltipContent>
          </Tooltip>
        </div>

        <CollapsibleContent>
          <div className='border-border/70 grid gap-5 border-t p-4 lg:grid-cols-2'>
            <section
              className='space-y-4'
              aria-labelledby={`${props.rule.id}-trigger-title`}
            >
              <div>
                <h4
                  id={`${props.rule.id}-trigger-title`}
                  className='flex items-center gap-2 text-sm font-semibold'
                >
                  <Settings2
                    className='text-primary size-4'
                    aria-hidden='true'
                  />
                  {t('Trigger conditions')}
                </h4>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t('Define when this source model should use its fallback.')}
                </p>
              </div>

              <div className='space-y-2'>
                <Label htmlFor={fieldInputId('sourceModel')}>
                  {t('Source Model')}
                </Label>
                <ComboboxInput
                  id={fieldInputId('sourceModel')}
                  options={sourceOptions}
                  value={props.rule.sourceModel}
                  onValueChange={(sourceModel) => updateRule({ sourceModel })}
                  allowCustomValue
                  disabled={props.disabled}
                  aria-invalid={Boolean(fieldError('sourceModel'))}
                  aria-describedby={fieldDescriptionId('sourceModel')}
                  placeholder={t('Select or enter a source model')}
                  emptyText='No matching models. Enter a custom model name.'
                />
                <p
                  id={fieldDescriptionId('sourceModel')}
                  className={cn(
                    'text-xs',
                    fieldError('sourceModel')
                      ? 'text-destructive'
                      : 'text-muted-foreground'
                  )}
                >
                  {sourceModelDescription}
                </p>
              </div>

              <div className='space-y-2'>
                <Label htmlFor={fieldInputId('sourceContextWindowTokens')}>
                  {t('Source context window')}
                </Label>
                <Input
                  id={fieldInputId('sourceContextWindowTokens')}
                  type='number'
                  min={1}
                  step={1}
                  value={props.rule.sourceContextWindowTokens}
                  disabled={props.disabled}
                  aria-invalid={Boolean(
                    fieldError('sourceContextWindowTokens')
                  )}
                  aria-describedby={fieldDescriptionId(
                    'sourceContextWindowTokens'
                  )}
                  onChange={(event) =>
                    updateRule({
                      sourceContextWindowTokens: event.target.value,
                    })
                  }
                  placeholder='262144'
                />
                <p
                  id={fieldDescriptionId('sourceContextWindowTokens')}
                  className={cn(
                    'text-xs',
                    fieldError('sourceContextWindowTokens')
                      ? 'text-destructive'
                      : 'text-muted-foreground'
                  )}
                >
                  {t(
                    fieldError('sourceContextWindowTokens')?.message ||
                      'Maximum context tokens supported by the source model.'
                  )}
                </p>
              </div>

              <div className='space-y-3'>
                <div className='flex items-center justify-between gap-3'>
                  <Label htmlFor={fieldInputId('thresholdPercent')}>
                    {t('Trigger threshold')}
                  </Label>
                  <div className='flex items-center gap-1'>
                    <Input
                      id={fieldInputId('thresholdPercent')}
                      type='number'
                      min={1}
                      max={100}
                      step={1}
                      value={props.rule.thresholdPercent}
                      disabled={props.disabled}
                      aria-invalid={Boolean(fieldError('thresholdPercent'))}
                      aria-describedby={fieldDescriptionId('thresholdPercent')}
                      onChange={(event) =>
                        updateRule({ thresholdPercent: event.target.value })
                      }
                      className='h-8 w-20 text-right'
                    />
                    <span className='text-muted-foreground text-sm'>%</span>
                  </div>
                </div>
                <Slider
                  min={1}
                  max={100}
                  step={1}
                  value={[sliderValue]}
                  disabled={props.disabled}
                  aria-label={t('Trigger threshold percentage')}
                  onValueChange={(values) => {
                    const value = Array.isArray(values) ? values[0] : values
                    updateRule({ thresholdPercent: String(value) })
                  }}
                />
                <p
                  id={fieldDescriptionId('thresholdPercent')}
                  className={cn(
                    'text-xs',
                    fieldError('thresholdPercent')
                      ? 'text-destructive'
                      : 'text-muted-foreground'
                  )}
                >
                  {thresholdDescription}
                </p>
              </div>
            </section>

            <section
              className='space-y-4'
              aria-labelledby={`${props.rule.id}-target-title`}
            >
              <div>
                <h4
                  id={`${props.rule.id}-target-title`}
                  className='flex items-center gap-2 text-sm font-semibold'
                >
                  <Route className='text-primary size-4' aria-hidden='true' />
                  {t('Fallback target')}
                </h4>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t(
                    'Choose the model and channel routing strategy used once.'
                  )}
                </p>
              </div>

              <div className='space-y-2'>
                <Label htmlFor={fieldInputId('fallbackModel')}>
                  {t('Fallback Model')}
                </Label>
                <ComboboxInput
                  id={fieldInputId('fallbackModel')}
                  options={fallbackOptions}
                  value={props.rule.fallbackModel}
                  onValueChange={(fallbackModel) =>
                    updateRule({ fallbackModel })
                  }
                  allowCustomValue
                  disabled={props.disabled}
                  aria-invalid={Boolean(fieldError('fallbackModel'))}
                  aria-describedby={fieldDescriptionId('fallbackModel')}
                  placeholder={t('Select or enter a fallback model')}
                  emptyText='No matching models. Enter a custom model name.'
                />
                <p
                  id={fieldDescriptionId('fallbackModel')}
                  className={cn(
                    'text-xs',
                    fieldError('fallbackModel')
                      ? 'text-destructive'
                      : 'text-muted-foreground'
                  )}
                >
                  {fallbackModelDescription}
                </p>
              </div>

              <div className='space-y-2'>
                <Label htmlFor={fieldInputId('fallbackContextWindowTokens')}>
                  {t('Fallback context window')}
                </Label>
                <Input
                  id={fieldInputId('fallbackContextWindowTokens')}
                  type='number'
                  min={1}
                  step={1}
                  value={props.rule.fallbackContextWindowTokens}
                  disabled={props.disabled}
                  aria-invalid={Boolean(
                    fieldError('fallbackContextWindowTokens')
                  )}
                  aria-describedby={fieldDescriptionId(
                    'fallbackContextWindowTokens'
                  )}
                  onChange={(event) =>
                    updateRule({
                      fallbackContextWindowTokens: event.target.value,
                    })
                  }
                  placeholder='1048576'
                />
                <p
                  id={fieldDescriptionId('fallbackContextWindowTokens')}
                  className={cn(
                    'text-xs',
                    fieldError('fallbackContextWindowTokens')
                      ? 'text-destructive'
                      : 'text-muted-foreground'
                  )}
                >
                  {t(
                    fieldError('fallbackContextWindowTokens')?.message ||
                      'Maximum context tokens supported by the fallback model.'
                  )}
                </p>
              </div>

              {triggerTokens != null &&
                Number(props.rule.fallbackContextWindowTokens) <
                  triggerTokens && (
                  <Alert className='border-amber-500/40 bg-amber-500/10 text-amber-900 dark:text-amber-100'>
                    <AlertTriangle className='size-4' aria-hidden='true' />
                    <AlertDescription>
                      {t(
                        'The fallback context window is smaller than the calculated trigger tokens.'
                      )}
                    </AlertDescription>
                  </Alert>
                )}

              <div className='space-y-2'>
                <Label>{t('Route mode')}</Label>
                <RadioGroup
                  value={props.rule.routeMode}
                  onValueChange={(routeMode) =>
                    updateRule(
                      {
                        routeMode: routeMode as
                          | 'same_channel'
                          | 'cross_channel',
                        targetMode:
                          routeMode === 'same_channel'
                            ? 'auto'
                            : props.rule.targetMode,
                      },
                      routeMode === 'same_channel'
                    )
                  }
                  className='grid-cols-2'
                  aria-label={t('Route mode')}
                  disabled={props.disabled}
                >
                  <Label className='border-border/70 has-data-checked:border-primary has-data-checked:bg-primary/5 flex cursor-pointer items-start gap-3 rounded-lg border p-3'>
                    <RadioGroupItem value='same_channel' />
                    <span>
                      <span className='block'>{t('Same channel')}</span>
                      <span className='text-muted-foreground mt-1 block text-xs font-normal'>
                        {t('Use another model on this channel')}
                      </span>
                    </span>
                  </Label>
                  <Label className='border-border/70 has-data-checked:border-primary has-data-checked:bg-primary/5 flex cursor-pointer items-start gap-3 rounded-lg border p-3'>
                    <RadioGroupItem value='cross_channel' />
                    <span>
                      <span className='block'>{t('Cross channel')}</span>
                      <span className='text-muted-foreground mt-1 block text-xs font-normal'>
                        {t('Route within compatible groups')}
                      </span>
                    </span>
                  </Label>
                </RadioGroup>
              </div>

              {props.rule.routeMode === 'cross_channel' && (
                <div className='space-y-3'>
                  <Label>{t('Target channel strategy')}</Label>
                  <RadioGroup
                    value={props.rule.targetMode}
                    onValueChange={(targetMode) =>
                      updateRule(
                        { targetMode: targetMode as 'auto' | 'limited' },
                        targetMode === 'auto'
                      )
                    }
                    aria-label={t('Target channel strategy')}
                    disabled={props.disabled}
                  >
                    <Label className='border-border/70 has-data-checked:border-primary has-data-checked:bg-primary/5 flex cursor-pointer items-start gap-3 rounded-lg border p-3'>
                      <RadioGroupItem value='auto' />
                      <span>
                        <span className='block'>
                          {t('Automatic group routing')}
                        </span>
                        <span className='text-muted-foreground mt-1 block text-xs font-normal'>
                          {t(
                            'Select any eligible channel in a compatible group.'
                          )}
                        </span>
                      </span>
                    </Label>
                    <Label className='border-border/70 has-data-checked:border-primary has-data-checked:bg-primary/5 flex cursor-pointer items-start gap-3 rounded-lg border p-3'>
                      <RadioGroupItem value='limited' />
                      <span>
                        <span className='block'>
                          {t('Limit candidate channels')}
                        </span>
                        <span className='text-muted-foreground mt-1 block text-xs font-normal'>
                          {t(
                            'Try only the selected channels in the listed order.'
                          )}
                        </span>
                      </span>
                    </Label>
                  </RadioGroup>
                </div>
              )}
            </section>
          </div>

          {targetEditorVisible && (
            <div className='border-border/70 space-y-3 border-t p-4'>
              <div>
                <Label htmlFor={`${props.rule.id}-target-search`}>
                  {t('Candidate channels')}
                </Label>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t(
                    'Final channel eligibility, including advanced custom paths, is checked at request time.'
                  )}
                </p>
              </div>

              {selectedTargets.length > 0 && (
                <div className='space-y-2'>
                  <p className='text-xs font-medium'>{t('Attempt order')}</p>
                  {selectedTargets.map((channel, index) => {
                    const isSelf = channel.id === props.currentChannelId
                    const isDisabled = channel.status !== 1
                    const missingModel =
                      !channel.missing &&
                      !channelSupportsModel(channel, props.rule.fallbackModel)
                    const incompatible =
                      !channel.missing &&
                      !groupsAreCompatible(props.currentGroups, channel.group)
                    const warning =
                      channel.missing ||
                      isSelf ||
                      isDisabled ||
                      missingModel ||
                      incompatible
                    let warningLabel = t('Group incompatible')
                    if (channel.missing) {
                      warningLabel = t('Unavailable')
                    } else if (isSelf) {
                      warningLabel = t('Ignored at runtime')
                    } else if (isDisabled) {
                      warningLabel = t('Disabled')
                    } else if (missingModel) {
                      warningLabel = t('Model unavailable')
                    }

                    return (
                      <div
                        key={channel.id}
                        className={cn(
                          'bg-muted/20 flex items-center gap-2 rounded-md border px-3 py-2',
                          warning ? 'border-amber-500/40' : 'border-border/70'
                        )}
                      >
                        <span className='text-muted-foreground w-5 text-center text-xs tabular-nums'>
                          {index + 1}
                        </span>
                        <span className='min-w-0 flex-1'>
                          <span className='block truncate text-sm font-medium'>
                            {channel.name}
                            <span className='text-muted-foreground ml-1 font-normal'>
                              #{channel.id}
                            </span>
                          </span>
                          <span className='text-muted-foreground block truncate text-xs'>
                            {channel.group || t('Unknown group')}
                          </span>
                        </span>
                        {warning && (
                          <Badge
                            variant='outline'
                            className='border-amber-500/50 text-amber-700 dark:text-amber-300'
                          >
                            {warningLabel}
                          </Badge>
                        )}
                        <Tooltip>
                          <TooltipTrigger
                            render={
                              <Button
                                type='button'
                                variant='ghost'
                                size='icon-sm'
                                disabled={props.disabled || index === 0}
                                onClick={() => reorderTarget(index, -1)}
                                aria-label={t('Move channel up')}
                              />
                            }
                          >
                            <ArrowUp className='size-4' aria-hidden='true' />
                          </TooltipTrigger>
                          <TooltipContent>
                            {t('Move channel up')}
                          </TooltipContent>
                        </Tooltip>
                        <Tooltip>
                          <TooltipTrigger
                            render={
                              <Button
                                type='button'
                                variant='ghost'
                                size='icon-sm'
                                disabled={
                                  props.disabled ||
                                  index === selectedTargets.length - 1
                                }
                                onClick={() => reorderTarget(index, 1)}
                                aria-label={t('Move channel down')}
                              />
                            }
                          >
                            <ArrowDown className='size-4' aria-hidden='true' />
                          </TooltipTrigger>
                          <TooltipContent>
                            {t('Move channel down')}
                          </TooltipContent>
                        </Tooltip>
                        <Button
                          type='button'
                          variant='ghost'
                          size='icon-sm'
                          disabled={props.disabled}
                          onClick={() => removeTarget(channel.id)}
                          aria-label={t('Remove target channel')}
                        >
                          <Trash2 className='size-4' aria-hidden='true' />
                        </Button>
                      </div>
                    )
                  })}
                </div>
              )}

              <div className='relative'>
                <Search
                  className='text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2'
                  aria-hidden='true'
                />
                <Input
                  id={`${props.rule.id}-target-search`}
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  disabled={props.disabled || !props.rule.fallbackModel.trim()}
                  placeholder={t('Search channels by name or ID')}
                  className='pl-9'
                />
              </div>

              {fieldError('targetChannelIds') && (
                <p className='text-destructive text-xs'>
                  {t(fieldError('targetChannelIds')?.message || '')}
                </p>
              )}

              <div className='max-h-56 space-y-1 overflow-y-auto'>
                {targetSearchQuery.isLoading && (
                  <p className='text-muted-foreground py-4 text-center text-sm'>
                    {t('Searching channels...')}
                  </p>
                )}
                {!targetSearchQuery.isLoading &&
                  (targetSearchQuery.data || []).map((channel) => {
                    const selected = props.rule.targetChannelIds.includes(
                      channel.id
                    )
                    const compatible = groupsAreCompatible(
                      props.currentGroups,
                      channel.group
                    )
                    let statusLabel = t('Group incompatible')
                    if (compatible) {
                      statusLabel = selected ? t('Selected') : t('Compatible')
                    }
                    return (
                      <button
                        key={channel.id}
                        type='button'
                        className='hover:bg-muted/60 focus-visible:ring-ring flex w-full items-center gap-3 rounded-md px-3 py-2 text-left outline-none focus-visible:ring-2 disabled:cursor-not-allowed disabled:opacity-50'
                        disabled={props.disabled || selected || !compatible}
                        onClick={() =>
                          props.onChange({
                            ...props.rule,
                            targetChannelIds: [
                              ...props.rule.targetChannelIds,
                              channel.id,
                            ],
                          })
                        }
                      >
                        <span className='min-w-0 flex-1'>
                          <span className='block truncate text-sm font-medium'>
                            {channel.name}{' '}
                            <span className='text-muted-foreground font-normal'>
                              #{channel.id}
                            </span>
                          </span>
                          <span className='text-muted-foreground block truncate text-xs'>
                            {channel.group}
                          </span>
                        </span>
                        <Badge variant={compatible ? 'secondary' : 'outline'}>
                          {statusLabel}
                        </Badge>
                      </button>
                    )
                  })}
                {!targetSearchQuery.isLoading &&
                  targetSearchQuery.data?.length === 0 && (
                    <p className='text-muted-foreground py-4 text-center text-sm'>
                      {t('No eligible channels found')}
                    </p>
                  )}
                {!props.rule.fallbackModel.trim() && (
                  <p className='text-muted-foreground py-4 text-center text-sm'>
                    {t('Enter a fallback model to search for channels.')}
                  </p>
                )}
              </div>
            </div>
          )}

          <div className='border-border/70 bg-muted/20 border-t px-4 py-3 text-xs'>
            <span className='font-medium'>{t('Rule summary')}: </span>
            <span className='text-muted-foreground'>
              {t(
                'At {{percent}}% of {{sourceTokens}} tokens, route {{sourceModel}} once to {{fallbackModel}} using {{strategy}}.',
                {
                  percent: props.rule.thresholdPercent || '—',
                  sourceTokens:
                    props.rule.sourceContextWindowTokens ||
                    t('an unset window'),
                  sourceModel:
                    props.rule.sourceModel || t('an unset source model'),
                  fallbackModel:
                    props.rule.fallbackModel || t('an unset fallback model'),
                  strategy: summaryStrategy,
                }
              )}
            </span>
          </div>
        </CollapsibleContent>
      </div>
    </Collapsible>
  )
}

export function ModelContextFallbackEditor(
  props: ModelContextFallbackEditorProps
) {
  const { t } = useTranslation()
  const initialRef = useRef(parseContextFallbackValue(props.value))
  const initial = initialRef.current
  const [mode, setMode] = useState<'visual' | 'json'>(
    initial.error ? 'json' : 'visual'
  )
  const [rules, setRules] = useState(initial.rules)
  const [jsonValue, setJsonValue] = useState(props.value)
  const [jsonError, setJsonError] = useState(initial.error)
  const [expandedRuleId, setExpandedRuleId] = useState<string | null>(
    initial.rules[0]?.id ?? null
  )
  const nextRuleIdRef = useRef(initial.rules.length)
  const lastEmittedValueRef = useRef(props.value)
  const onValidityChangeRef = useRef(props.onValidityChange)
  const visualErrors = useMemo(
    () => validateContextFallbackDrafts(rules),
    [rules]
  )
  const activeError = mode === 'json' ? jsonError : (visualErrors[0] ?? null)

  useEffect(() => {
    onValidityChangeRef.current = props.onValidityChange
  }, [props.onValidityChange])

  useEffect(() => {
    onValidityChangeRef.current(activeError?.message ?? null)
  }, [activeError?.message])

  useEffect(() => {
    if (props.value === lastEmittedValueRef.current) return
    lastEmittedValueRef.current = props.value
    const parsed = parseContextFallbackValue(props.value)
    setJsonValue(props.value)
    setJsonError(parsed.error)
    if (parsed.error) {
      setMode('json')
      return
    }
    setRules(parsed.rules)
    nextRuleIdRef.current = parsed.rules.length
    setExpandedRuleId(parsed.rules[0]?.id ?? null)
  }, [props])

  const emitValue = (value: string) => {
    lastEmittedValueRef.current = value
    props.onChange(value)
  }

  const focusError = (error: ContextFallbackValidationError | null) => {
    if (!error?.ruleId) return
    setExpandedRuleId(error.ruleId)
    window.requestAnimationFrame(() => {
      const field = error.field
        ? document.querySelector<HTMLElement>(
            `[id="context-fallback-${error.ruleId}-${error.field}"]`
          )
        : null
      field?.focus()
      field?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    })
  }

  const syncVisualRules = (nextRules: ContextFallbackRuleDraft[]) => {
    setRules(nextRules)
    const errors = validateContextFallbackDrafts(nextRules)
    if (errors.length > 0) return

    const serialized = serializeContextFallbackDrafts(nextRules)
    setJsonValue(serialized)
    setJsonError(null)
    emitValue(serialized)
  }

  const updateRule = (rule: ContextFallbackRuleDraft) => {
    syncVisualRules(
      rules.map((currentRule) =>
        currentRule.id === rule.id ? rule : currentRule
      )
    )
  }

  const addRule = () => {
    if (rules.length >= MAX_CONTEXT_FALLBACK_RULES) return
    nextRuleIdRef.current += 1
    const rule = createEmptyContextFallbackRule(
      `context-fallback-new-${nextRuleIdRef.current}`
    )
    setExpandedRuleId(rule.id)
    syncVisualRules([...rules, rule])
    window.requestAnimationFrame(() => {
      document
        .querySelector<HTMLElement>(
          `[id="context-fallback-${rule.id}-sourceModel"]`
        )
        ?.focus()
    })
  }

  const deleteRule = (id: string) => {
    const nextRules = rules.filter((rule) => rule.id !== id)
    if (expandedRuleId === id) {
      setExpandedRuleId(nextRules[0]?.id ?? null)
    }
    syncVisualRules(nextRules)
  }

  const handleJsonChange = (value: string) => {
    setJsonValue(value)
    const parsed = parseContextFallbackValue(value)
    setJsonError(parsed.error)
    if (parsed.error) return

    setRules(parsed.rules)
    setExpandedRuleId(parsed.rules[0]?.id ?? null)
    emitValue(value)
  }

  const handleModeChange = (nextMode: string) => {
    if (nextMode !== 'visual' && nextMode !== 'json') return
    if (nextMode === 'json') {
      const error = visualErrors[0] ?? null
      if (error) {
        focusError(error)
        return
      }
      setMode('json')
      return
    }

    const parsed = parseContextFallbackValue(jsonValue)
    setJsonError(parsed.error)
    if (parsed.error) return
    setRules(parsed.rules)
    setExpandedRuleId(parsed.rules[0]?.id ?? null)
    setMode('visual')
  }

  const formatJson = () => {
    const parsed = parseContextFallbackValue(jsonValue)
    setJsonError(parsed.error)
    if (parsed.error) return
    const formatted = jsonValue.trim()
      ? JSON.stringify(JSON.parse(jsonValue), null, 2)
      : ''
    setJsonValue(formatted)
    emitValue(formatted)
  }

  return (
    <Tabs value={mode} onValueChange={handleModeChange} className='space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <TabsList>
          <TabsTrigger value='visual' disabled={props.disabled}>
            <Settings2 className='size-4' aria-hidden='true' />
            {t('Visual')}
          </TabsTrigger>
          <TabsTrigger value='json' disabled={props.disabled}>
            <Code className='size-4' aria-hidden='true' />
            {t('JSON')}
          </TabsTrigger>
        </TabsList>
        {mode === 'json' && (
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={formatJson}
            disabled={props.disabled || Boolean(jsonError)}
          >
            {t('Format JSON')}
          </Button>
        )}
      </div>

      {activeError && (
        <Alert variant='destructive'>
          <AlertTriangle className='size-4' aria-hidden='true' />
          <AlertDescription>{t(activeError.message)}</AlertDescription>
        </Alert>
      )}

      <TabsContent value='visual' className='space-y-3'>
        {rules.length === 0 ? (
          <div className='border-border/70 bg-muted/10 flex min-h-36 flex-col items-center justify-center gap-3 rounded-lg border border-dashed p-6 text-center'>
            <div className='bg-muted text-muted-foreground flex size-9 items-center justify-center rounded-full'>
              <Route className='size-4' aria-hidden='true' />
            </div>
            <div>
              <p className='text-sm font-medium'>
                {t('No model context fallback rules configured.')}
              </p>
              <p className='text-muted-foreground mt-1 max-w-lg text-xs'>
                {t(
                  'Add a rule to route oversized requests to a model with a larger context window.'
                )}
              </p>
            </div>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={addRule}
              disabled={props.disabled}
            >
              <Plus className='size-4' aria-hidden='true' />
              {t('Add fallback rule')}
            </Button>
          </div>
        ) : (
          <div className='space-y-2'>
            {rules.map((rule) => (
              <RuleCard
                key={rule.id}
                rule={rule}
                errors={visualErrors.filter(
                  (error) => error.ruleId === rule.id
                )}
                expanded={expandedRuleId === rule.id}
                disabled={Boolean(props.disabled)}
                sourceModels={props.sourceModels}
                fallbackModels={props.fallbackModels}
                currentChannelId={props.currentChannelId}
                currentGroups={props.currentGroups}
                onExpandedChange={() =>
                  setExpandedRuleId((current) =>
                    current === rule.id ? null : rule.id
                  )
                }
                onChange={updateRule}
                onDelete={() => deleteRule(rule.id)}
              />
            ))}
            <Button
              type='button'
              variant='outline'
              className='w-full border-dashed'
              onClick={addRule}
              disabled={
                props.disabled || rules.length >= MAX_CONTEXT_FALLBACK_RULES
              }
            >
              <Plus className='size-4' aria-hidden='true' />
              {t('Add fallback rule')}
            </Button>
          </div>
        )}
      </TabsContent>

      <TabsContent value='json' className='space-y-2'>
        <Label htmlFor='model-context-fallback-json'>
          {t('Context fallback JSON')}
        </Label>
        <Textarea
          id='model-context-fallback-json'
          value={jsonValue}
          onChange={(event) => handleJsonChange(event.target.value)}
          disabled={props.disabled}
          aria-invalid={Boolean(jsonError)}
          aria-describedby='model-context-fallback-json-description'
          placeholder='{}'
          className='min-h-72 font-mono text-xs'
          spellCheck={false}
        />
        <p
          id='model-context-fallback-json-description'
          className={cn(
            'text-xs',
            jsonError ? 'text-destructive' : 'text-muted-foreground'
          )}
        >
          {jsonError
            ? t(jsonError.message)
            : t(
                'Advanced fields are preserved when switching back to the visual editor.'
              )}
        </p>
      </TabsContent>
    </Tabs>
  )
}
