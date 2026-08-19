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
import type {
  ChannelNativeProbeResponse,
  ModelProtocolProfile,
  TextEndpointType,
} from '../types'

export const TEXT_PROTOCOLS: readonly TextEndpointType[] = [
  'openai',
  'openai-response',
  'anthropic',
  'gemini',
]

export type NativeProtocolProbeResultMap = Record<
  string,
  ChannelNativeProbeResponse
>

export type NativeProtocolProbeBatch = {
  models: readonly string[]
  expectedResultKeys: readonly string[]
  stopped: boolean
}

export type ModelOverridesDraftParseResult =
  | {
      success: true
      value: Record<string, ModelProtocolProfile>
    }
  | {
      success: false
      error: 'invalid_json' | 'not_object'
    }

export type ModelProtocolCoverageSummary = {
  totalModels: number
  coveredModels: number
  capabilities: Record<
    TextEndpointType,
    {
      nonStream: number
      stream: number
    }
  >
}

export type ModelProtocolPromotion = {
  native: ModelProtocolProfile['native']
  modelOverrides: Record<string, ModelProtocolProfile>
}

/**
 * Read a model's native capability object without trusting an editable JSON
 * draft to already match the TypeScript type.
 */
function modelNativeFor(
  overrides: Record<string, ModelProtocolProfile>,
  model: string
): ModelProtocolProfile['native'] | null {
  const profile = overrides[model] as unknown
  if (!profile || typeof profile !== 'object' || Array.isArray(profile)) {
    return null
  }
  const native = (profile as { native?: unknown }).native
  if (!native || typeof native !== 'object' || Array.isArray(native)) {
    return null
  }
  return native as ModelProtocolProfile['native']
}

/**
 * Build the stable key used to associate one probe response with its model,
 * protocol and request mode.
 */
export function nativeProtocolProbeKey(
  model: string,
  endpointType: TextEndpointType,
  stream: boolean
): string {
  return `${model}\u0000${endpointType}\u0000${stream ? 'stream' : 'normal'}`
}

/**
 * Capture the immutable model and task scope for one native protocol probe.
 */
export function createNativeProtocolProbeBatch(
  models: readonly string[]
): NativeProtocolProbeBatch {
  const batchModels = [...models]
  const expectedResultKeys = batchModels.flatMap((model) =>
    TEXT_PROTOCOLS.flatMap((endpointType) => [
      nativeProtocolProbeKey(model, endpointType, false),
      nativeProtocolProbeKey(model, endpointType, true),
    ])
  )

  return {
    models: batchModels,
    expectedResultKeys,
    stopped: false,
  }
}

/**
 * Determine whether every task in a non-stopped probe batch has a response.
 */
export function isNativeProtocolProbeBatchComplete(
  batch: NativeProtocolProbeBatch | null,
  results: NativeProtocolProbeResultMap
): boolean {
  if (!batch || batch.stopped || batch.expectedResultKeys.length === 0) {
    return false
  }
  return batch.expectedResultKeys.every((key) => Object.hasOwn(results, key))
}

/**
 * Parse the visible model-overrides draft, treating blank input as an empty
 * object so clearing the editor has an explicit configuration meaning.
 */
export function parseModelOverridesDraft(
  draft: string
): ModelOverridesDraftParseResult {
  if (!draft.trim()) {
    return { success: true, value: {} }
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(draft)
  } catch {
    return { success: false, error: 'invalid_json' }
  }

  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { success: false, error: 'not_object' }
  }

  return {
    success: true,
    value: parsed as Record<string, ModelProtocolProfile>,
  }
}

/**
 * Summarize protocol support declared by model overrides for channel models.
 */
export function summarizeModelProtocolOverrides(
  models: readonly string[],
  overrides: Record<string, ModelProtocolProfile>
): ModelProtocolCoverageSummary {
  const channelModels = [...new Set(models)]
  const coveredModels = channelModels.filter(
    (model) => modelNativeFor(overrides, model) !== null
  )
  const capabilities = Object.fromEntries(
    TEXT_PROTOCOLS.map((endpointType) => [
      endpointType,
      {
        nonStream: coveredModels.filter(
          (model) =>
            modelNativeFor(overrides, model)?.[endpointType]?.non_stream ===
            true
        ).length,
        stream: coveredModels.filter(
          (model) =>
            modelNativeFor(overrides, model)?.[endpointType]?.stream === true
        ).length,
      },
    ])
  ) as ModelProtocolCoverageSummary['capabilities']

  return {
    totalModels: channelModels.length,
    coveredModels: coveredModels.length,
    capabilities,
  }
}

/**
 * Promote the common capabilities of fully covered channel models to the
 * channel default and remove overrides that become identical to that default.
 */
export function promoteCommonModelProtocolCapabilities(
  models: readonly string[],
  overrides: Record<string, ModelProtocolProfile>
): ModelProtocolPromotion | null {
  const channelModels = [...new Set(models)]
  if (
    channelModels.length === 0 ||
    channelModels.some((model) => modelNativeFor(overrides, model) === null)
  ) {
    return null
  }

  const native: ModelProtocolProfile['native'] = {}
  for (const endpointType of TEXT_PROTOCOLS) {
    const nonStream = channelModels.every(
      (model) =>
        modelNativeFor(overrides, model)?.[endpointType]?.non_stream === true
    )
    const stream = channelModels.every(
      (model) =>
        modelNativeFor(overrides, model)?.[endpointType]?.stream === true
    )
    if (nonStream || stream) {
      native[endpointType] = { non_stream: nonStream, stream }
    }
  }
  if (Object.keys(native).length === 0) return null

  const modelOverrides = structuredClone(overrides)
  for (const model of channelModels) {
    const modelNative = modelNativeFor(overrides, model)
    if (!modelNative) continue
    const hasOnlyKnownProtocols = Object.keys(modelNative).every(
      (endpointType) =>
        TEXT_PROTOCOLS.includes(endpointType as TextEndpointType)
    )
    const matchesDefault =
      hasOnlyKnownProtocols &&
      TEXT_PROTOCOLS.every((endpointType) => {
        const modelCapability = modelNative[endpointType]
        const defaultCapability = native[endpointType]
        return (
          Boolean(modelCapability?.non_stream) ===
            Boolean(defaultCapability?.non_stream) &&
          Boolean(modelCapability?.stream) ===
            Boolean(defaultCapability?.stream)
        )
      })
    if (matchesDefault) {
      delete modelOverrides[model]
    }
  }

  return { native, modelOverrides }
}

/**
 * Replace overrides for every model in a complete probe batch while keeping
 * overrides for models outside that batch unchanged.
 */
export function applyNativeProtocolProbeResults(
  baseOverrides: Record<string, ModelProtocolProfile>,
  batch: NativeProtocolProbeBatch,
  results: NativeProtocolProbeResultMap
): Record<string, ModelProtocolProfile> | null {
  if (!isNativeProtocolProbeBatchComplete(batch, results)) {
    return null
  }

  const nextOverrides = structuredClone(baseOverrides)
  for (const model of batch.models) {
    // A completed batch is authoritative for its models, including no-match results.
    delete nextOverrides[model]
    const native: ModelProtocolProfile['native'] = {}

    for (const endpointType of TEXT_PROTOCOLS) {
      const nonStream =
        results[nativeProtocolProbeKey(model, endpointType, false)]
          ?.classification === 'confirmed'
      const stream =
        results[nativeProtocolProbeKey(model, endpointType, true)]
          ?.classification === 'confirmed'
      if (nonStream || stream) {
        native[endpointType] = { non_stream: nonStream, stream }
      }
    }

    if (Object.keys(native).length > 0) {
      nextOverrides[model] = { native }
    }
  }

  return nextOverrides
}
