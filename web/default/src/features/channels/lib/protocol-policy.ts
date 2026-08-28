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
  ChannelProtocolProbeResponse,
  ModelProtocolProfile,
  ProtocolCapability,
  TextEndpointType,
} from '../types'

export const TEXT_PROTOCOLS: readonly TextEndpointType[] = [
  'openai',
  'openai-response',
  'anthropic',
  'gemini',
]

export const PROTOCOL_PROBE_CASES = [
  'basic',
  'assistant_history',
  'tool_cycle',
  'reasoning_history',
  'invalid_tool_id',
  'tool_id_collision',
] as const

export const PROTOCOL_PROBE_CLASSIFICATION_LABEL_KEYS = {
  confirmed: 'Confirmed',
  path_mismatch: 'Path mismatch',
  auth_error: 'Authentication failed',
  rate_limited: 'Rate limited',
  upstream_error: 'Upstream error',
  transport_error: 'Transport error',
} as const satisfies Record<
  ChannelProtocolProbeResponse['classification'],
  string
>

export type ProtocolProbeCase = (typeof PROTOCOL_PROBE_CASES)[number]
export type ProtocolCapabilityState = 'unavailable' | 'native' | 'normalized'
export type ProtocolRequestMode = 'non_stream' | 'stream'

export type ProtocolProbeResultMap = Record<
  string,
  ChannelProtocolProbeResponse
>

export type ProtocolProbeBatch = {
  models: readonly string[]
  probeCases: readonly ProtocolProbeCase[]
  expectedResultKeys: readonly string[]
  capabilityLevel: 'endpoint' | 'semantic'
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

type ProtocolModeCount = {
  native: number
  normalized: number
}

export type ModelProtocolCoverageSummary = {
  totalModels: number
  coveredModels: number
  capabilities: Record<
    TextEndpointType,
    {
      nonStream: ProtocolModeCount
      stream: ProtocolModeCount
    }
  >
}

export type ModelProtocolPromotion = {
  native: ModelProtocolProfile['native']
  modelOverrides: Record<string, ModelProtocolProfile>
}

/** Report whether a saved credential or current draft key can run selected probes. */
export function isProtocolProbeReady(
  channelId: number | undefined,
  draftKey: string | undefined,
  selectedModelCount: number
): boolean {
  return (
    selectedModelCount > 0 &&
    Boolean(channelId || String(draftKey || '').trim())
  )
}

/**
 * Read a model's capability object without trusting an editable JSON draft to
 * already match the TypeScript type.
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
 * Return the backward-compatible handling mode of one enabled capability.
 */
export function effectiveProtocolCapabilityMode(
  capability?: ProtocolCapability
): 'native' | 'normalized' {
  return capability?.mode === 'normalized' ? 'normalized' : 'native'
}

/**
 * Return the three-state value displayed for one normal or streaming cell.
 */
export function protocolCapabilityState(
  capability: ProtocolCapability | undefined,
  requestMode: ProtocolRequestMode
): ProtocolCapabilityState {
  if (!capability?.[requestMode]) return 'unavailable'
  return effectiveProtocolCapabilityMode(capability)
}

/**
 * Report whether the backend has a final-wire normalizer for this endpoint.
 */
export function supportsNormalizedProtocol(
  endpointType: TextEndpointType
): boolean {
  return endpointType === 'anthropic' || endpointType === 'openai-response'
}

/**
 * Update one three-state cell while preserving the backend's shared mode field.
 */
export function updateProtocolCapabilityState(
  capability: ProtocolCapability | undefined,
  endpointType: TextEndpointType,
  requestMode: ProtocolRequestMode,
  state: ProtocolCapabilityState
): ProtocolCapability | null {
  const nextCapability: ProtocolCapability = {
    non_stream: capability?.non_stream === true,
    stream: capability?.stream === true,
  }
  nextCapability[requestMode] = state !== 'unavailable'

  if (!nextCapability.non_stream && !nextCapability.stream) {
    return null
  }

  const requestedMode =
    state === 'unavailable'
      ? effectiveProtocolCapabilityMode(capability)
      : state
  if (requestedMode === 'normalized') {
    nextCapability.mode = 'normalized'
    if (endpointType === 'anthropic') {
      nextCapability.reasoning_history =
        capability?.reasoning_history === 'preserve' ? 'preserve' : 'strip'
    }
  }
  return nextCapability
}

/**
 * Build the stable key associating a result with its model, protocol, request
 * mode, and scenario.
 */
export function protocolProbeKey(
  model: string,
  endpointType: TextEndpointType,
  stream: boolean,
  probeCase: ProtocolProbeCase
): string {
  return `${model}\u0000${endpointType}\u0000${stream ? 'stream' : 'normal'}\u0000${probeCase}`
}

/**
 * Capture the immutable scope for one endpoint-only or full semantic batch.
 */
export function createProtocolProbeBatch(
  models: readonly string[],
  capabilityLevel: 'endpoint' | 'semantic'
): ProtocolProbeBatch {
  const batchModels = [...models]
  const probeCases: readonly ProtocolProbeCase[] =
    capabilityLevel === 'semantic' ? PROTOCOL_PROBE_CASES : ['basic']
  const expectedResultKeys = batchModels.flatMap((model) =>
    TEXT_PROTOCOLS.flatMap((endpointType) =>
      [false, true].flatMap((stream) =>
        probeCases.map((probeCase) =>
          protocolProbeKey(model, endpointType, stream, probeCase)
        )
      )
    )
  )

  return {
    models: batchModels,
    probeCases,
    expectedResultKeys,
    capabilityLevel,
    stopped: false,
  }
}

/**
 * Determine whether every task in a non-stopped batch has a response.
 */
export function isProtocolProbeBatchComplete(
  batch: ProtocolProbeBatch | null,
  results: ProtocolProbeResultMap
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
 * Count native and normalized declarations without treating coverage as a
 * semantic compatibility claim.
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
    TEXT_PROTOCOLS.map((endpointType) => {
      const countMode = (requestMode: ProtocolRequestMode) => {
        const counts: ProtocolModeCount = { native: 0, normalized: 0 }
        for (const model of coveredModels) {
          const capability = modelNativeFor(overrides, model)?.[endpointType]
          const state = protocolCapabilityState(capability, requestMode)
          if (state === 'native' || state === 'normalized') {
            counts[state] += 1
          }
        }
        return counts
      }
      return [
        endpointType,
        {
          nonStream: countMode('non_stream'),
          stream: countMode('stream'),
        },
      ]
    })
  ) as ModelProtocolCoverageSummary['capabilities']

  return {
    totalModels: channelModels.length,
    coveredModels: coveredModels.length,
    capabilities,
  }
}

/**
 * Clone a capability in its canonical backward-compatible JSON form.
 */
function cloneProtocolCapability(
  capability: ProtocolCapability,
  nonStream: boolean,
  stream: boolean
): ProtocolCapability {
  const cloned: ProtocolCapability = {
    non_stream: nonStream,
    stream,
  }
  if (effectiveProtocolCapabilityMode(capability) === 'normalized') {
    cloned.mode = 'normalized'
    if (capability.reasoning_history) {
      cloned.reasoning_history = capability.reasoning_history
    }
  }
  return cloned
}

/**
 * Compare all persisted fields that affect one capability's runtime behavior.
 */
function protocolCapabilitiesEqual(
  left: ProtocolCapability | undefined,
  right: ProtocolCapability | undefined
): boolean {
  if (!left || !right) return !left && !right
  return (
    left.non_stream === right.non_stream &&
    left.stream === right.stream &&
    effectiveProtocolCapabilityMode(left) ===
      effectiveProtocolCapabilityMode(right) &&
    (left.reasoning_history ?? '') === (right.reasoning_history ?? '')
  )
}

/**
 * Promote common capabilities only when their handling modes are identical,
 * then remove model overrides that become exactly redundant.
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
    const capabilities = channelModels.map(
      (model) => modelNativeFor(overrides, model)?.[endpointType]
    )
    const firstCapability = capabilities[0]
    if (!firstCapability || capabilities.some((item) => !item)) continue
    if (
      capabilities.some(
        (item) =>
          effectiveProtocolCapabilityMode(item) !==
            effectiveProtocolCapabilityMode(firstCapability) ||
          (item?.reasoning_history ?? '') !==
            (firstCapability.reasoning_history ?? '')
      )
    ) {
      continue
    }

    const nonStream = capabilities.every(
      (capability) => capability?.non_stream === true
    )
    const stream = capabilities.every(
      (capability) => capability?.stream === true
    )
    if (nonStream || stream) {
      native[endpointType] = cloneProtocolCapability(
        firstCapability,
        nonStream,
        stream
      )
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
      TEXT_PROTOCOLS.every((endpointType) =>
        protocolCapabilitiesEqual(
          modelNative[endpointType],
          native[endpointType]
        )
      )
    if (matchesDefault) {
      delete modelOverrides[model]
    }
  }

  return { native, modelOverrides }
}

/**
 * Resolve one full semantic suite into a persisted handling mode. Basic
 * endpoint reachability participates as a prerequisite but never grants
 * native compatibility by itself.
 */
function probeSuiteRecommendedMode(
  batch: ProtocolProbeBatch,
  results: ProtocolProbeResultMap,
  model: string,
  endpointType: TextEndpointType,
  stream: boolean
): 'native' | 'normalized' | null {
  const basic = results[protocolProbeKey(model, endpointType, stream, 'basic')]
  if (basic?.classification !== 'confirmed') return null

  const semanticCases = batch.probeCases.filter(
    (probeCase) => probeCase !== 'basic'
  )
  if (semanticCases.length === 0) return null
  const semanticResults = semanticCases.map(
    (probeCase) =>
      results[protocolProbeKey(model, endpointType, stream, probeCase)]
  )
  if (
    semanticResults.some(
      (result) =>
        result?.classification !== 'confirmed' ||
        result.capability_level !== 'semantic' ||
        result.recommended_mode === 'unsupported'
    )
  ) {
    return null
  }
  const modes = new Set(
    semanticResults.map((result) => result.recommended_mode)
  )
  if (modes.size !== 1) return null
  const mode = semanticResults[0]?.recommended_mode
  return mode === 'native' || mode === 'normalized' ? mode : null
}

/**
 * Apply only a complete full semantic batch, preserving models outside the
 * batch and never converting endpoint-only results into native declarations.
 */
export function applyProtocolProbeResults(
  baseOverrides: Record<string, ModelProtocolProfile>,
  batch: ProtocolProbeBatch,
  results: ProtocolProbeResultMap
): Record<string, ModelProtocolProfile> | null {
  if (
    batch.capabilityLevel !== 'semantic' ||
    !isProtocolProbeBatchComplete(batch, results)
  ) {
    return null
  }

  const nextOverrides = structuredClone(baseOverrides)
  for (const model of batch.models) {
    delete nextOverrides[model]
    const native: ModelProtocolProfile['native'] = {}

    for (const endpointType of TEXT_PROTOCOLS) {
      const nonStreamMode = probeSuiteRecommendedMode(
        batch,
        results,
        model,
        endpointType,
        false
      )
      const streamMode = probeSuiteRecommendedMode(
        batch,
        results,
        model,
        endpointType,
        true
      )
      const sharedMode = nonStreamMode ?? streamMode
      if (!sharedMode) continue

      const nonStream = nonStreamMode === sharedMode
      const stream = streamMode === sharedMode
      const capability: ProtocolCapability = {
        non_stream: nonStream,
        stream,
      }
      if (sharedMode === 'normalized') {
        capability.mode = 'normalized'
        if (endpointType === 'anthropic') {
          capability.reasoning_history = 'strip'
        }
      }
      native[endpointType] = capability
    }

    if (Object.keys(native).length > 0) {
      nextOverrides[model] = { native }
    }
  }

  return nextOverrides
}
