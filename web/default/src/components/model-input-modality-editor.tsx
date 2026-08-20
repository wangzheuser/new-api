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
import { ArrowRight, Plus, Search, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  buildGlobalInputModalityModelOptions,
  enableChannelInputModalityOverride,
  filterChannelInputModalityModels,
  getAvailableInputModalityModelOptions,
  getModelInputModalityNameError,
  groupChannelInputModalityModels,
  MAX_MODEL_INPUT_MODALITY_ENTRIES,
  normalizeModelInputModalities,
  removeModelInputModalityDeclaration,
  resolveModelInputModalities,
  type ModelInputModalities,
  type ModelInputModalityNameError,
} from '@/lib/model-input-modalities'

type ModelInputModalityEditorProps = {
  value: ModelInputModalities
  onChange: (value: ModelInputModalities) => void
  modelOptions?: string[]
  mapping?: Record<string, string>
  globalValue?: ModelInputModalities
  scope: 'global' | 'channel'
  disabled?: boolean
}

type ScopedInputModalityEditorProps = Omit<
  ModelInputModalityEditorProps,
  'scope'
>

type ChannelInputModalityRowProps = ScopedInputModalityEditorProps & {
  model: string
  removed: boolean
}

/** Render the shared global or channel editor for model input modalities. */
export function ModelInputModalityEditor(props: ModelInputModalityEditorProps) {
  if (props.scope === 'channel') {
    return <ChannelInputModalityEditor {...props} />
  }
  return <GlobalInputModalityEditor {...props} />
}

/** Render searchable global declarations with stable model-name editing. */
function GlobalInputModalityEditor(props: ScopedInputModalityEditorProps) {
  const { t } = useTranslation()
  const [draftModel, setDraftModel] = useState('')
  const [draftError, setDraftError] = useState('')
  const [modelDrafts, setModelDrafts] = useState<Record<string, string>>({})
  const [modelErrors, setModelErrors] = useState<Record<string, string>>({})

  const configuredModels = Object.keys(props.value)
  const modelOptions = useMemo(
    () =>
      buildGlobalInputModalityModelOptions(
        props.modelOptions || [],
        props.value
      ),
    [props.modelOptions, props.value]
  )
  const addModelOptions = useMemo(
    () => getAvailableInputModalityModelOptions(modelOptions, props.value),
    [modelOptions, props.value]
  )

  const getNameErrorMessage = (error: ModelInputModalityNameError): string => {
    if (error === 'required') return t('Model name is required')
    if (error === 'too_long') {
      return t('Model name must not exceed 255 bytes')
    }
    if (error === 'duplicate') {
      return t('This model already has an input modality configuration')
    }
    return ''
  }

  const updateModel = (model: string, imageEnabled: boolean) => {
    props.onChange(
      normalizeModelInputModalities({
        ...props.value,
        [model]: imageEnabled ? ['text', 'image'] : ['text'],
      })
    )
  }

  const removeModel = (model: string) => {
    props.onChange(removeModelInputModalityDeclaration(model, props.value))
  }

  const addModel = () => {
    const model = draftModel.trim()
    const error = getNameErrorMessage(
      getModelInputModalityNameError(model, props.value)
    )
    if (error) {
      setDraftError(error)
      return
    }

    updateModel(model, false)
    setDraftModel('')
    setDraftError('')
  }

  const renameModel = (previousModel: string, nextModel: string) => {
    const model = nextModel.trim()
    const error = getNameErrorMessage(
      getModelInputModalityNameError(model, props.value, previousModel)
    )
    if (error) {
      setModelErrors((current) => ({
        ...current,
        [previousModel]: error,
      }))
      return
    }

    if (model === previousModel) {
      setModelDrafts((current) => ({
        ...current,
        [previousModel]: previousModel,
      }))
      setModelErrors((current) => {
        const next = { ...current }
        delete next[previousModel]
        return next
      })
      return
    }

    const nextValue = { ...props.value, [model]: props.value[previousModel] }
    delete nextValue[previousModel]
    props.onChange(normalizeModelInputModalities(nextValue))
    setModelDrafts((current) => {
      const next = { ...current }
      delete next[previousModel]
      return next
    })
    setModelErrors((current) => {
      const next = { ...current }
      delete next[previousModel]
      return next
    })
  }

  return (
    <div className='space-y-4'>
      <div className='flex flex-col gap-2 sm:flex-row'>
        <ComboboxInput
          id='new-input-modality-model'
          options={addModelOptions.map((model) => ({
            value: model,
            label: model,
          }))}
          value={draftModel}
          onValueChange={(value) => {
            setDraftModel(value)
            setDraftError('')
          }}
          placeholder={t('Enter an exact client-requested model name')}
          emptyText={t('No matching models. Enter a custom model name.')}
          allowCustomValue
          openOnFocus
          disabled={props.disabled}
          aria-invalid={Boolean(draftError)}
          aria-describedby={
            draftError ? 'new-input-modality-model-error' : undefined
          }
        />
        <Button
          type='button'
          variant='outline'
          disabled={
            props.disabled ||
            configuredModels.length >= MAX_MODEL_INPUT_MODALITY_ENTRIES
          }
          onClick={addModel}
        >
          <Plus className='mr-2 h-4 w-4' aria-hidden='true' />
          {t('Add model')}
        </Button>
      </div>
      {draftError && (
        <p
          id='new-input-modality-model-error'
          className='text-destructive text-sm'
        >
          {draftError}
        </p>
      )}

      {configuredModels.length === 0 ? (
        <div className='text-muted-foreground rounded-lg border border-dashed p-6 text-center text-sm'>
          {t(
            'No model input modalities are configured. Requests keep the existing compatibility behavior.'
          )}
        </div>
      ) : (
        <div className='space-y-2'>
          {configuredModels.map((model, index) => {
            const imageEnabled = props.value[model].includes('image')
            const modelError = modelErrors[model]
            const modelInputId = `input-modality-model-${index}`
            const modelErrorId = `${modelInputId}-error`
            const renameOptions = getAvailableInputModalityModelOptions(
              modelOptions,
              props.value,
              model
            )

            return (
              <div
                key={model}
                className='border-border/60 grid gap-3 rounded-lg border p-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start sm:gap-x-4'
              >
                <div className='min-w-0 space-y-2'>
                  <label className='sr-only' htmlFor={modelInputId}>
                    {t('Model name')}
                  </label>
                  <ComboboxInput
                    id={modelInputId}
                    options={renameOptions.map((option) => ({
                      value: option,
                      label: option,
                    }))}
                    value={modelDrafts[model] ?? model}
                    onValueChange={(value) => {
                      setModelDrafts((current) => ({
                        ...current,
                        [model]: value,
                      }))
                      setModelErrors((current) => {
                        const next = { ...current }
                        delete next[model]
                        return next
                      })
                    }}
                    onValueCommit={(value) => renameModel(model, value)}
                    onBlur={() =>
                      renameModel(model, modelDrafts[model] ?? model)
                    }
                    placeholder={t('Model name')}
                    emptyText={t(
                      'No matching models. Enter a custom model name.'
                    )}
                    allowCustomValue
                    openOnFocus
                    disabled={props.disabled}
                    aria-invalid={Boolean(modelError)}
                    aria-describedby={modelError ? modelErrorId : undefined}
                  />
                  {modelError && (
                    <p id={modelErrorId} className='text-destructive text-sm'>
                      {modelError}
                    </p>
                  )}
                  <div className='flex flex-wrap gap-2'>
                    <Badge variant='outline'>{t('Text')}</Badge>
                    <Badge variant={imageEnabled ? 'default' : 'outline'}>
                      {imageEnabled ? t('Image') : t('Image disabled')}
                    </Badge>
                  </div>
                </div>
                <div className='flex h-8 items-center justify-between gap-3 sm:justify-end'>
                  <div className='flex items-center gap-2'>
                    <span className='text-sm'>{t('Image input')}</span>
                    <Switch
                      checked={imageEnabled}
                      disabled={props.disabled}
                      aria-label={t('Image input for {{model}}', { model })}
                      onCheckedChange={(checked) => updateModel(model, checked)}
                    />
                  </div>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon-sm'
                    disabled={props.disabled}
                    aria-label={t('Delete input modality configuration')}
                    onClick={() => removeModel(model)}
                  >
                    <Trash2 className='h-4 w-4' aria-hidden='true' />
                  </Button>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

/** Render every live channel model and keep removed overrides visible for cleanup. */
function ChannelInputModalityEditor(props: ScopedInputModalityEditorProps) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const mapping = useMemo(() => props.mapping || {}, [props.mapping])
  const modelGroups = useMemo(
    () =>
      groupChannelInputModalityModels(props.modelOptions || [], props.value),
    [props.modelOptions, props.value]
  )
  const currentModels = useMemo(
    () =>
      filterChannelInputModalityModels(
        modelGroups.currentModels,
        mapping,
        search
      ),
    [mapping, modelGroups.currentModels, search]
  )
  const removedModels = useMemo(
    () =>
      filterChannelInputModalityModels(
        modelGroups.removedModels,
        mapping,
        search
      ),
    [mapping, modelGroups.removedModels, search]
  )
  const hasModels =
    modelGroups.currentModels.length > 0 || modelGroups.removedModels.length > 0
  const hasSearchResults = currentModels.length > 0 || removedModels.length > 0

  if (!hasModels) {
    return (
      <div className='text-muted-foreground rounded-lg border border-dashed p-4 text-sm'>
        {t('Add a client model before configuring input modalities.')}
      </div>
    )
  }

  return (
    <div className='space-y-3'>
      <div className='relative'>
        <Search
          className='text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 h-4 w-4 -translate-y-1/2'
          aria-hidden='true'
        />
        <Input
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder={t('Search models...')}
          aria-label={t('Search models')}
          className='pl-8'
        />
      </div>

      {!hasSearchResults ? (
        <div className='text-muted-foreground rounded-lg border border-dashed p-6 text-center text-sm'>
          {t('No models found.')}
        </div>
      ) : (
        <div className='max-h-[32rem] space-y-4 overflow-y-auto pr-1'>
          {currentModels.length > 0 && (
            <div className='space-y-2'>
              {currentModels.map((model) => (
                <ChannelInputModalityRow
                  key={model}
                  {...props}
                  model={model}
                  removed={false}
                />
              ))}
            </div>
          )}

          {removedModels.length > 0 && (
            <div className='space-y-2'>
              <div className='space-y-1'>
                <p className='text-sm font-medium'>
                  {t('Removed Models ({{count}})', {
                    count: modelGroups.removedModels.length,
                  })}
                </p>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'These overrides belong to models removed from the current channel model list. Restore inheritance to remove them.'
                  )}
                </p>
              </div>
              {removedModels.map((model) => (
                <ChannelInputModalityRow
                  key={model}
                  {...props}
                  model={model}
                  removed
                />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

/** Render one channel model with independent override and image controls. */
function ChannelInputModalityRow(props: ChannelInputModalityRowProps) {
  const { t } = useTranslation()
  const globalValue = props.globalValue || {}
  const effective = resolveModelInputModalities(
    props.model,
    props.value,
    globalValue
  )
  const overridden = effective.source === 'channel'
  const imageEnabled = effective.modalities.includes('image')
  const mappingTarget = props.mapping?.[props.model]

  let sourceLabel = t('Unconfigured compatibility behavior')
  if (effective.source === 'channel') {
    sourceLabel = t('Channel override')
  } else if (effective.source === 'global') {
    sourceLabel = t('Inherited from global configuration')
  }

  const updateModel = (imageAllowed: boolean) => {
    props.onChange(
      normalizeModelInputModalities({
        ...props.value,
        [props.model]: imageAllowed ? ['text', 'image'] : ['text'],
      })
    )
  }

  return (
    <div className='border-border/60 bg-muted/20 grid gap-3 rounded-lg border px-4 py-3 md:grid-cols-[minmax(0,1fr)_auto] md:items-center'>
      <div className='min-w-0 space-y-2'>
        <div className='flex min-w-0 flex-wrap items-center gap-2'>
          <span className='truncate text-sm font-medium'>{props.model}</span>
          {props.removed && <Badge variant='destructive'>{t('Removed')}</Badge>}
        </div>
        {mappingTarget && (
          <p className='text-muted-foreground flex min-w-0 items-center gap-1.5 text-xs'>
            <span className='truncate'>{props.model}</span>
            <ArrowRight className='h-3.5 w-3.5 shrink-0' aria-hidden='true' />
            <span className='truncate'>{mappingTarget}</span>
          </p>
        )}
        <div className='flex flex-wrap gap-2'>
          <Badge variant='outline'>{t('Text')}</Badge>
          <Badge variant='outline'>{sourceLabel}</Badge>
        </div>
      </div>

      <div className='flex flex-wrap items-center gap-x-5 gap-y-2 md:justify-end'>
        <div className='flex h-8 items-center gap-2'>
          <span className='text-sm'>{t('Channel override')}</span>
          <Switch
            checked={overridden}
            disabled={props.disabled}
            aria-label={`${t('Channel override')}: ${props.model}`}
            onCheckedChange={(checked) => {
              if (!checked) {
                props.onChange(
                  removeModelInputModalityDeclaration(props.model, props.value)
                )
                return
              }
              props.onChange(
                enableChannelInputModalityOverride(
                  props.model,
                  props.value,
                  globalValue
                )
              )
            }}
          />
        </div>
        <div className='flex h-8 items-center gap-2'>
          <span className='text-sm'>{t('Image input')}</span>
          <Switch
            checked={imageEnabled}
            disabled={props.disabled || !overridden}
            aria-label={t('Image input for {{model}}', {
              model: props.model,
            })}
            onCheckedChange={updateModel}
          />
        </div>
      </div>
    </div>
  )
}
