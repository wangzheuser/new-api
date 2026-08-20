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
import { ArrowRight, Plus, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import {
  enableChannelInputModalityOverride,
  MAX_MODEL_INPUT_MODALITY_ENTRIES,
  normalizeModelInputModalities,
  removeModelInputModalityDeclaration,
  resolveModelInputModalities,
  type ModelInputModalities,
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

/** Render the shared global list or channel inheritance editor for model input modalities. */
export function ModelInputModalityEditor(props: ModelInputModalityEditorProps) {
  const { t } = useTranslation()
  const [draftModel, setDraftModel] = useState('')
  const [draftError, setDraftError] = useState('')
  const [selectedModel, setSelectedModel] = useState('')
  const [modelDrafts, setModelDrafts] = useState<Record<string, string>>({})
  const [modelErrors, setModelErrors] = useState<Record<string, string>>({})

  const models = useMemo(() => {
    const values = new Set([
      ...(props.modelOptions || []),
      ...Object.keys(props.mapping || {}),
      ...Object.keys(props.value),
    ])
    return [...values].filter(Boolean)
  }, [props.mapping, props.modelOptions, props.value])

  const activeModel =
    selectedModel && models.includes(selectedModel)
      ? selectedModel
      : Object.keys(props.mapping || {})[0] ||
        Object.keys(props.value)[0] ||
        props.modelOptions?.[0] ||
        models[0] ||
        ''

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
    if (!model) {
      setDraftError(t('Model name is required'))
      return
    }
    if (Object.hasOwn(props.value, model)) {
      setDraftError(t('This model already has an input modality configuration'))
      return
    }
    updateModel(model, false)
    setDraftModel('')
    setDraftError('')
  }

  const renameModel = (previousModel: string) => {
    const model = (modelDrafts[previousModel] || '').trim()
    if (!model) {
      setModelErrors((current) => ({
        ...current,
        [previousModel]: t('Model name is required'),
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
    if (Object.hasOwn(props.value, model)) {
      setModelErrors((current) => ({
        ...current,
        [previousModel]: t(
          'This model already has an input modality configuration'
        ),
      }))
      return
    }
    const next = { ...props.value, [model]: props.value[previousModel] }
    delete next[previousModel]
    props.onChange(normalizeModelInputModalities(next))
    setModelErrors((current) => {
      const nextErrors = { ...current }
      delete nextErrors[previousModel]
      return nextErrors
    })
  }

  if (props.scope === 'channel') {
    if (models.length === 0) {
      return (
        <div className='text-muted-foreground rounded-lg border border-dashed p-4 text-sm'>
          {t('Add a client model before configuring input modalities.')}
        </div>
      )
    }

    const globalValue = props.globalValue || {}
    const effective = resolveModelInputModalities(
      activeModel,
      props.value,
      globalValue
    )
    const overridden = effective.source === 'channel'
    const imageEnabled = effective.modalities.includes('image')
    const mappingTarget = props.mapping?.[activeModel]
    let sourceLabel = t('Unconfigured compatibility behavior')
    if (effective.source === 'channel') {
      sourceLabel = t('Channel override')
    } else if (effective.source === 'global') {
      sourceLabel = t('Inherited from global configuration')
    }

    return (
      <div className='space-y-4'>
        <div className='space-y-2'>
          <label className='text-sm font-medium' htmlFor='input-modality-model'>
            {t('Client-requested model')}
          </label>
          <Combobox
            id='input-modality-model'
            options={models.map((model) => ({ value: model, label: model }))}
            value={activeModel}
            onValueChange={(value) => setSelectedModel(value || '')}
            placeholder={t('Select a model')}
            searchPlaceholder={t('Search models...')}
            emptyText={t('No models found.')}
            openOnFocus={false}
          />
          {mappingTarget && (
            <p className='text-muted-foreground flex items-center gap-1.5 text-xs'>
              <span>{activeModel}</span>
              <ArrowRight className='h-3.5 w-3.5' aria-hidden='true' />
              <span>{mappingTarget}</span>
            </p>
          )}
        </div>

        <div className='bg-muted/20 flex items-center justify-between gap-4 rounded-lg border px-4 py-3'>
          <div className='space-y-1'>
            <div className='flex flex-wrap items-center gap-2'>
              <span className='text-sm font-medium'>
                {t('Channel override')}
              </span>
              <Badge variant='outline'>{sourceLabel}</Badge>
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'When disabled, this model inherits the exact global declaration.'
              )}
            </p>
          </div>
          <Switch
            checked={overridden}
            disabled={props.disabled}
            onCheckedChange={(checked) => {
              if (!checked) {
                removeModel(activeModel)
                return
              }
              props.onChange(
                enableChannelInputModalityOverride(
                  activeModel,
                  props.value,
                  globalValue
                )
              )
            }}
          />
        </div>

        <div className='space-y-2'>
          <div className='bg-muted/20 flex items-center justify-between gap-4 rounded-lg border px-4 py-3'>
            <div>
              <p className='text-sm font-medium'>{t('Text input')}</p>
              <p className='text-muted-foreground text-xs'>
                {t('Required and always enabled in this version.')}
              </p>
            </div>
            <Switch checked disabled aria-label={t('Text input')} />
          </div>
          <div className='bg-muted/20 flex items-center justify-between gap-4 rounded-lg border px-4 py-3'>
            <div>
              <p className='text-sm font-medium'>{t('Image input')}</p>
              <p className='text-muted-foreground text-xs'>
                {t('Allow structured image content in LLM requests.')}
              </p>
            </div>
            <Switch
              checked={imageEnabled}
              disabled={props.disabled || !overridden}
              aria-label={t('Image input')}
              onCheckedChange={(checked) => updateModel(activeModel, checked)}
            />
          </div>
        </div>
      </div>
    )
  }

  const configuredModels = Object.keys(props.value)
  return (
    <div className='space-y-4'>
      <div className='flex flex-col gap-2 sm:flex-row'>
        <Input
          value={draftModel}
          disabled={props.disabled}
          placeholder={t('Enter an exact client-requested model name')}
          onChange={(event) => {
            setDraftModel(event.target.value)
            setDraftError('')
          }}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault()
              addModel()
            }
          }}
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
      {draftError && <p className='text-destructive text-sm'>{draftError}</p>}

      {configuredModels.length === 0 ? (
        <div className='text-muted-foreground rounded-lg border border-dashed p-6 text-center text-sm'>
          {t(
            'No model input modalities are configured. Requests keep the existing compatibility behavior.'
          )}
        </div>
      ) : (
        <div className='space-y-2'>
          {configuredModels.map((model) => {
            const imageEnabled = props.value[model].includes('image')
            return (
              <div
                key={model}
                className='flex flex-col gap-3 rounded-lg border p-4 sm:flex-row sm:items-center'
              >
                <div className='min-w-0 flex-1 space-y-1'>
                  <Input
                    value={modelDrafts[model] ?? model}
                    disabled={props.disabled}
                    aria-label={t('Model name')}
                    onChange={(event) => {
                      setModelDrafts((current) => ({
                        ...current,
                        [model]: event.target.value,
                      }))
                      setModelErrors((current) => {
                        const next = { ...current }
                        delete next[model]
                        return next
                      })
                    }}
                    onBlur={() => renameModel(model)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') event.currentTarget.blur()
                    }}
                  />
                  {modelErrors[model] && (
                    <p className='text-destructive text-sm'>
                      {modelErrors[model]}
                    </p>
                  )}
                  <div className='flex flex-wrap gap-2'>
                    <Badge variant='outline'>{t('Text')}</Badge>
                    <Badge variant={imageEnabled ? 'default' : 'outline'}>
                      {imageEnabled ? t('Image') : t('Image disabled')}
                    </Badge>
                  </div>
                </div>
                <div className='flex items-center justify-between gap-3 sm:justify-end'>
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
