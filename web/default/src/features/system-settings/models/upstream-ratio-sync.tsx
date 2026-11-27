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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import { CheckSquare, RefreshCcw } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'

import {
  buildModelPricingSyncChanges,
  type PricingSyncSnapshot,
} from '../../../../../shared/model-pricing-sync'
import {
  fetchUpstreamRatios,
  getUpstreamChannels,
  getPricingSyncSnapshot,
  savePricingSyncChanges,
} from '../api'
import type {
  DifferencesMap,
  RatioType,
  UpstreamChannel,
  UpstreamConfig,
} from '../types'
import { ChannelSelectorDialog } from './channel-selector-dialog'
import {
  ConflictConfirmDialog,
  type ConflictItem,
} from './conflict-confirm-dialog'
import {
  DEFAULT_ENDPOINT,
  MODELS_DEV_PRESET_ENDPOINT,
  MODELS_DEV_PRESET_ID,
  OFFICIAL_CHANNEL_ENDPOINT,
  OFFICIAL_CHANNEL_ID,
  OPENROUTER_CHANNEL_TYPE,
  OPENROUTER_ENDPOINT,
} from './constants'
import {
  RATIO_SYNC_FIELDS,
  applyResolutionRemovalPlan,
  applyResolutionSelection,
  applyResolutionSelections,
  deleteResolutionField,
  type ResolutionRemovalPlan,
  type ResolutionSelection,
  type ResolutionsMap,
} from './upstream-ratio-sync-helpers'
import { UpstreamRatioSyncTable } from './upstream-ratio-sync-table'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type UpstreamRatioSyncProps = {
  modelRatios: {
    ModelPrice: string
    ModelRatio: string
    CompletionRatio: string
    CacheRatio: string
    CreateCacheRatio: string
    ImageRatio: string
    AudioRatio: string
    AudioCompletionRatio: string
    'billing_setting.billing_mode': string
    'billing_setting.billing_expr': string
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// The two synthesized presets always carry stable negative IDs assigned by
// `controller/ratio_sync.go`; matching by ID alone is sufficient and avoids
// fragile name/base_url comparisons.
function getDefaultEndpointForChannel(channel: UpstreamChannel): string {
  if (channel.id === MODELS_DEV_PRESET_ID) return MODELS_DEV_PRESET_ENDPOINT
  if (channel.id === OFFICIAL_CHANNEL_ID) return OFFICIAL_CHANNEL_ENDPOINT
  if (channel.type === OPENROUTER_CHANNEL_TYPE) return OPENROUTER_ENDPOINT
  return DEFAULT_ENDPOINT
}

function parseJsonRecord<T>(raw: string | undefined | null): Record<string, T> {
  try {
    return JSON.parse(raw || '{}') as Record<string, T>
  } catch {
    return {}
  }
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function UpstreamRatioSync({ modelRatios }: UpstreamRatioSyncProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [pricingBaseline, setPricingBaseline] =
    useState<PricingSyncSnapshot | null>(null)
  const [channelDialogOpen, setChannelDialogOpen] = useState(false)
  const [conflictDialogOpen, setConflictDialogOpen] = useState(false)
  const [selectedChannelIds, setSelectedChannelIds] = useState<number[]>([])
  const [channelEndpoints, setChannelEndpoints] = useState<
    Record<number, string>
  >({})
  const [differences, setDifferences] = useState<DifferencesMap>({})
  const [resolutions, setResolutions] = useState<ResolutionsMap>({})
  const [conflictItems, setConflictItems] = useState<ConflictItem[]>([])
  const [confirmLoading, setConfirmLoading] = useState(false)

  const { data: channelsData } = useQuery({
    queryKey: ['upstream-channels'],
    queryFn: getUpstreamChannels,
    enabled: channelDialogOpen,
  })

  // Memoize the channels list so the effect below only re-runs when the query
  // data actually changes, instead of on every render (the `|| []` fallback
  // would otherwise produce a new array reference each render).
  const channels = useMemo(() => channelsData?.data ?? [], [channelsData?.data])

  useEffect(() => {
    if (channels.length === 0) return
    setChannelEndpoints((prev) => {
      let mutated = false
      const next = { ...prev }
      for (const channel of channels) {
        if (!next[channel.id]) {
          next[channel.id] = getDefaultEndpointForChannel(channel)
          mutated = true
        }
      }
      return mutated ? next : prev
    })
  }, [channels])

  const fetchMutation = useMutation({
    mutationFn: async (request: Parameters<typeof fetchUpstreamRatios>[0]) => {
      setPricingBaseline(null)
      const baseline = await getPricingSyncSnapshot()
      if (!baseline.success) {
        throw new Error(
          baseline.message || t('Failed to fetch upstream prices')
        )
      }
      const response = await fetchUpstreamRatios(request)
      if (response.success) setPricingBaseline(baseline.data)
      return response
    },
    onSuccess: (data) => {
      if (!data.success) {
        toast.error(data.message || t('Failed to fetch upstream prices'))
        return
      }

      const { differences: diffs, test_results } = data.data

      const errorResults = test_results.filter((r) => r.status === 'error')
      if (errorResults.length > 0) {
        const errorMsg = errorResults
          .map((r) => `${r.name}: ${r.error}`)
          .join(', ')
        toast.warning(t('Some channels failed: {{errorMsg}}', { errorMsg }))
      }

      setDifferences(diffs)
      setResolutions({})

      if (Object.keys(diffs).length === 0) {
        toast.success(t('No price differences found'))
      } else {
        toast.success(t('Upstream prices fetched successfully'))
      }
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Failed to fetch upstream prices'))
    },
  })

  const { mutate: syncMutate, isPending: isSyncPending } = useMutation({
    mutationFn: async () => {
      if (!pricingBaseline) {
        throw new Error(t('Failed to fetch upstream prices'))
      }
      const changes = buildModelPricingSyncChanges(pricingBaseline, resolutions)
      await savePricingSyncChanges(changes)
    },
    onSuccess: () => {
      toast.success(t('Prices synced successfully'))
      queryClient.invalidateQueries({ queryKey: ['system-options'] })

      setDifferences((prevDiffs) => {
        const newDiffs = { ...prevDiffs }
        Object.entries(resolutions).forEach(([model, ratios]) => {
          Object.keys(ratios).forEach((ratioType) => {
            if (newDiffs[model]?.[ratioType as RatioType]) {
              delete newDiffs[model][ratioType as RatioType]
              if (Object.keys(newDiffs[model]).length === 0) {
                delete newDiffs[model]
              }
            }
          })
        })
        return newDiffs
      })

      setResolutions({})
    },
    onError: (error: Error) => {
      const message = isAxiosError<{ message?: string }>(error)
        ? error.response?.data?.message || error.message
        : error.message
      toast.error(message || t('Failed to sync prices'))
    },
  })

  const handleOpenChannelDialog = () => {
    setChannelDialogOpen(true)
  }

  const handleConfirmChannelSelection = (selectedIds: number[]) => {
    const selectedChannels = channels.filter((ch) =>
      selectedIds.includes(ch.id)
    )

    if (selectedChannels.length === 0) {
      toast.warning(t('Please select at least one channel'))
      return
    }

    const upstreams: UpstreamConfig[] = selectedChannels.map((ch) => ({
      id: ch.id,
      name: ch.name,
      base_url: ch.base_url,
      endpoint: channelEndpoints[ch.id] || DEFAULT_ENDPOINT,
    }))

    fetchMutation.mutate({ upstreams, timeout: 10 })
  }

  const handleSelectValue = useCallback(
    (
      model: string,
      ratioType: RatioType,
      value: number | string,
      sourceName: string
    ) => {
      setResolutions((prev) =>
        applyResolutionSelection(prev, differences, {
          model,
          ratioType,
          value,
          sourceName,
        })
      )
    },
    [differences]
  )

  const handleSelectValues = useCallback(
    (selections: ResolutionSelection[]) => {
      if (selections.length === 0) return
      setResolutions((prev) =>
        applyResolutionSelections(prev, differences, selections)
      )
    },
    [differences]
  )

  const handleUnselectValue = useCallback(
    (model: string, ratioType: RatioType) => {
      setResolutions((prev) => deleteResolutionField(prev, model, ratioType))
    },
    []
  )

  const handleUnselectValues = useCallback((plan: ResolutionRemovalPlan) => {
    if (plan.size === 0) return
    setResolutions((prev) => applyResolutionRemovalPlan(prev, plan))
  }, [])

  const parsedRatios = useMemo(() => {
    return {
      ModelRatio: parseJsonRecord<number>(modelRatios.ModelRatio),
      CompletionRatio: parseJsonRecord<number>(modelRatios.CompletionRatio),
      CacheRatio: parseJsonRecord<number>(modelRatios.CacheRatio),
      CreateCacheRatio: parseJsonRecord<number>(modelRatios.CreateCacheRatio),
      ImageRatio: parseJsonRecord<number>(modelRatios.ImageRatio),
      AudioRatio: parseJsonRecord<number>(modelRatios.AudioRatio),
      AudioCompletionRatio: parseJsonRecord<number>(
        modelRatios.AudioCompletionRatio
      ),
      ModelPrice: parseJsonRecord<number>(modelRatios.ModelPrice),
      'billing_setting.billing_mode': parseJsonRecord<string>(
        modelRatios['billing_setting.billing_mode']
      ),
      'billing_setting.billing_expr': parseJsonRecord<string>(
        modelRatios['billing_setting.billing_expr']
      ),
    }
  }, [modelRatios])

  type ParsedRatios = typeof parsedRatios

  const getLocalBillingCategory = (
    model: string,
    currentRatios: ParsedRatios
  ): 'price' | 'ratio' | null => {
    if (currentRatios.ModelPrice[model] !== undefined) return 'price'
    if (
      currentRatios.ModelRatio[model] !== undefined ||
      currentRatios.CompletionRatio[model] !== undefined ||
      currentRatios.CacheRatio[model] !== undefined ||
      currentRatios.CreateCacheRatio[model] !== undefined ||
      currentRatios.ImageRatio[model] !== undefined ||
      currentRatios.AudioRatio[model] !== undefined ||
      currentRatios.AudioCompletionRatio[model] !== undefined
    ) {
      return 'ratio'
    }
    return null
  }

  const performSync = useCallback(async (): Promise<boolean> => {
    return new Promise<boolean>((resolve) => {
      syncMutate(undefined, {
        onSuccess: () => resolve(true),
        onError: () => resolve(false),
      })
    })
  }, [syncMutate])

  const findSourceChannel = (
    model: string,
    ratioType: RatioType,
    value: number | string
  ): string => {
    const upMap = differences[model]?.[ratioType]?.upstreams
    if (!upMap) return 'Unknown'
    const entry = Object.entries(upMap).find(([, v]) => v === value)
    return entry ? entry[0] : 'Unknown'
  }

  const handleApplySync = () => {
    const currentRatios = parsedRatios
    const conflicts: ConflictItem[] = []

    const fixedPriceLabel = t('Fixed price')
    const modelRatioLabel = t('Model ratio')
    const completionRatioLabel = t('Completion ratio')

    Object.entries(resolutions).forEach(([model, ratios]) => {
      const localCat = getLocalBillingCategory(model, currentRatios)
      const selectedTypes = Object.keys(ratios)
      let newCat: 'price' | 'ratio' | 'tiered'
      if ('model_price' in ratios) {
        newCat = 'price'
      } else if (RATIO_SYNC_FIELDS.some((rt) => selectedTypes.includes(rt))) {
        newCat = 'ratio'
      } else {
        newCat = 'tiered'
      }

      if (localCat && newCat !== 'tiered' && localCat !== newCat) {
        const currentDesc =
          localCat === 'price'
            ? `${fixedPriceLabel}: ${currentRatios.ModelPrice[model]}`
            : `${modelRatioLabel}: ${currentRatios.ModelRatio[model] ?? '-'}\n${completionRatioLabel}: ${currentRatios.CompletionRatio[model] ?? '-'}`

        const newDesc =
          newCat === 'price'
            ? `${fixedPriceLabel}: ${ratios.model_price}`
            : `${modelRatioLabel}: ${ratios.model_ratio ?? '-'}\n${completionRatioLabel}: ${ratios.completion_ratio ?? '-'}`

        const channelNames = selectedTypes
          .map((rt) => findSourceChannel(model, rt as RatioType, ratios[rt]))
          .filter((v, idx, arr) => arr.indexOf(v) === idx)
          .join(', ')

        conflicts.push({
          channel: channelNames,
          model,
          current: currentDesc,
          newVal: newDesc,
        })
      }
    })

    if (conflicts.length > 0) {
      setConflictItems(conflicts)
      setConflictDialogOpen(true)
      return
    }

    toast.info(t('Syncing prices, please wait...'))
    performSync()
  }

  const handleConfirmConflict = async () => {
    setConfirmLoading(true)
    try {
      const success = await performSync()
      if (success) {
        setConflictDialogOpen(false)
      }
    } finally {
      setConfirmLoading(false)
    }
  }

  const hasSelections = Object.keys(resolutions).length > 0
  const isLoading = fetchMutation.isPending || isSyncPending || confirmLoading

  return (
    <div className='flex h-full min-h-0 flex-col gap-4'>
      <div className='flex shrink-0 flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
        <div className='flex flex-col gap-2 sm:flex-row'>
          <Button onClick={handleOpenChannelDialog} disabled={isLoading}>
            <RefreshCcw className='mr-2 h-4 w-4' />
            {t('Select Sync Channels')}
          </Button>
          <Button
            variant='secondary'
            onClick={handleApplySync}
            disabled={!hasSelections || !pricingBaseline || isLoading}
          >
            {(isSyncPending || confirmLoading) && (
              <span className='mr-2 h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent' />
            )}
            <CheckSquare className='mr-2 h-4 w-4' />
            {t('Apply Sync')}
          </Button>
        </div>
      </div>

      <div className='min-h-0 flex-1'>
        <UpstreamRatioSyncTable
          differences={differences}
          resolutions={resolutions}
          isDisabled={isLoading}
          isSyncing={fetchMutation.isPending}
          onSelectValue={handleSelectValue}
          onSelectValues={handleSelectValues}
          onUnselectValue={handleUnselectValue}
          onUnselectValues={handleUnselectValues}
        />
      </div>

      <ChannelSelectorDialog
        open={channelDialogOpen}
        onOpenChange={setChannelDialogOpen}
        channels={channels}
        selectedChannelIds={selectedChannelIds}
        onSelectedChannelIdsChange={setSelectedChannelIds}
        channelEndpoints={channelEndpoints}
        onChannelEndpointsChange={setChannelEndpoints}
        onConfirm={handleConfirmChannelSelection}
      />

      <ConflictConfirmDialog
        open={conflictDialogOpen}
        onOpenChange={setConflictDialogOpen}
        conflicts={conflictItems}
        onConfirm={handleConfirmConflict}
        isLoading={confirmLoading}
      />
    </div>
  )
}
