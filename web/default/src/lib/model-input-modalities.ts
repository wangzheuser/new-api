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
import { z } from 'zod'

export const MAX_MODEL_INPUT_MODALITY_ENTRIES = 256

export type InputModality = 'text' | 'image'
export type ModelInputModalities = Record<string, InputModality[]>
export type InputModalityConfigSource = 'channel' | 'global' | 'unconfigured'
export type ModelInputModalityNameError =
  | 'required'
  | 'too_long'
  | 'duplicate'
  | null

export type ChannelInputModalityModelGroups = {
  currentModels: string[]
  removedModels: string[]
}

const inputModalitiesSchema = z
  .array(z.enum(['text', 'image']))
  .min(1)
  .refine((modalities) => modalities.includes('text'))
  .refine((modalities) => new Set(modalities).size === modalities.length)

export const modelInputModalitiesSchema = z
  .record(z.string(), inputModalitiesSchema)
  .superRefine((value, context) => {
    const entries = Object.entries(value)
    if (entries.length > MAX_MODEL_INPUT_MODALITY_ENTRIES) {
      context.addIssue({
        code: 'custom',
      })
    }
    for (const [model] of entries) {
      if (
        !model.trim() ||
        model !== model.trim() ||
        new TextEncoder().encode(model).length > 255
      ) {
        context.addIssue({
          code: 'custom',
          path: [model],
        })
      }
    }
  })

/** Normalize model keys and modality ordering for stable form comparisons. */
export function normalizeModelInputModalities(
  value: ModelInputModalities
): ModelInputModalities {
  const normalized: ModelInputModalities = {}
  const models = Object.keys(value).sort()
  for (const model of models) {
    const modalities = value[model] || []
    normalized[model] = modalities.includes('image')
      ? ['text', 'image']
      : ['text']
  }
  return normalized
}

/** Keep declarations for exact model names that remain in the channel model list. */
export function filterModelInputModalitiesForModels(
  value: ModelInputModalities,
  models: string[]
): ModelInputModalities {
  const allowedModels = new Set(
    models.map((model) => model.trim()).filter(Boolean)
  )
  return normalizeModelInputModalities(
    Object.fromEntries(
      Object.entries(value).filter(([model]) => allowedModels.has(model))
    )
  )
}

/** Build a stable global picker list from catalog and persisted model names. */
export function buildGlobalInputModalityModelOptions(
  modelOptions: string[],
  value: ModelInputModalities
): string[] {
  const options = new Set<string>()
  for (const model of [...modelOptions, ...Object.keys(value)]) {
    const normalizedModel = model.trim()
    if (normalizedModel) options.add(normalizedModel)
  }
  return [...options].sort()
}

/** Exclude model names already used by another global declaration. */
export function getAvailableInputModalityModelOptions(
  modelOptions: string[],
  value: ModelInputModalities,
  currentModel?: string
): string[] {
  const configuredModels = new Set(Object.keys(value))
  if (currentModel) configuredModels.delete(currentModel)
  return modelOptions.filter((model) => !configuredModels.has(model))
}

/** Validate one trimmed model name before adding or renaming a declaration. */
export function getModelInputModalityNameError(
  model: string,
  value: ModelInputModalities,
  currentModel?: string
): ModelInputModalityNameError {
  if (!model) return 'required'
  if (new TextEncoder().encode(model).length > 255) return 'too_long'
  if (model !== currentModel && Object.hasOwn(value, model)) return 'duplicate'
  return null
}

/** Separate live channel models from saved overrides removed from the form list. */
export function groupChannelInputModalityModels(
  modelOptions: string[],
  channelValue: ModelInputModalities
): ChannelInputModalityModelGroups {
  const currentModels: string[] = []
  const currentModelSet = new Set<string>()
  for (const model of modelOptions) {
    const normalizedModel = model.trim()
    if (!normalizedModel || currentModelSet.has(normalizedModel)) continue
    currentModelSet.add(normalizedModel)
    currentModels.push(normalizedModel)
  }

  const removedModels = Object.keys(channelValue)
    .filter((model) => !currentModelSet.has(model))
    .sort()
  return { currentModels, removedModels }
}

/** Filter channel models by requested name or mapped upstream target. */
export function filterChannelInputModalityModels(
  models: string[],
  mapping: Record<string, string>,
  search: string
): string[] {
  const normalizedSearch = search.trim().toLowerCase()
  if (!normalizedSearch) return models

  return models.filter((model) => {
    const mappingTarget = mapping[model] || ''
    return (
      model.toLowerCase().includes(normalizedSearch) ||
      mappingTarget.toLowerCase().includes(normalizedSearch)
    )
  })
}

/** Parse one persisted option or channel setting value into a validated map. */
export function parseModelInputModalities(
  value: unknown
): ModelInputModalities {
  return parseModelInputModalitiesWithStatus(value).value
}

/** Parse a persisted value while retaining whether its syntax and domain rules were valid. */
export function parseModelInputModalitiesWithStatus(value: unknown): {
  value: ModelInputModalities
  valid: boolean
} {
  let parsed = value
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (!trimmed) return { value: {}, valid: true }
    try {
      parsed = JSON.parse(trimmed)
    } catch {
      return { value: {}, valid: false }
    }
  }
  const result = modelInputModalitiesSchema.safeParse(parsed)
  return result.success
    ? { value: normalizeModelInputModalities(result.data), valid: true }
    : { value: {}, valid: false }
}

/** Serialize a capability map with deterministic model and modality ordering. */
export function stringifyModelInputModalities(
  value: ModelInputModalities
): string {
  return JSON.stringify(normalizeModelInputModalities(value))
}

/** Parse a model mapping into exact non-empty source and target names. */
export function parseInputModalityModelMapping(
  value: unknown
): Record<string, string> {
  let parsed = value
  if (typeof value === 'string') {
    if (!value.trim()) return {}
    try {
      parsed = JSON.parse(value)
    } catch {
      return {}
    }
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}

  const mapping: Record<string, string> = {}
  for (const [rawSource, rawTarget] of Object.entries(parsed)) {
    const source = rawSource.trim()
    const target = typeof rawTarget === 'string' ? rawTarget.trim() : ''
    if (source && target) mapping[source] = target
  }
  return mapping
}

/** Enable one channel override, copying an exact global declaration when present. */
export function enableChannelInputModalityOverride(
  model: string,
  channelValue: ModelInputModalities,
  globalValue: ModelInputModalities
): ModelInputModalities {
  const inherited = globalValue[model]
  return normalizeModelInputModalities({
    ...channelValue,
    [model]: inherited || ['text', 'image'],
  })
}

/** Remove one model declaration so a channel value resumes global inheritance. */
export function removeModelInputModalityDeclaration(
  model: string,
  channelValue: ModelInputModalities
): ModelInputModalities {
  const next = { ...channelValue }
  delete next[model]
  return normalizeModelInputModalities(next)
}

/** Resolve one model's effective channel-first capability declaration. */
export function resolveModelInputModalities(
  model: string,
  channelValue: ModelInputModalities,
  globalValue: ModelInputModalities
): {
  modalities: InputModality[]
  configured: boolean
  source: InputModalityConfigSource
} {
  if (Object.hasOwn(channelValue, model)) {
    return {
      modalities: channelValue[model],
      configured: true,
      source: 'channel',
    }
  }
  if (Object.hasOwn(globalValue, model)) {
    return {
      modalities: globalValue[model],
      configured: true,
      source: 'global',
    }
  }
  return {
    modalities: ['text', 'image'],
    configured: false,
    source: 'unconfigured',
  }
}
