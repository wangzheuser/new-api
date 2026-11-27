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
  ChannelProtocolPolicy,
  ChannelProtocolProbeResponse,
  ProtocolCapabilities,
  ProtocolCapability,
  ProtocolOverrides,
  ModelProtocolProfile,
  TextEndpointType,
} from '../types'

export { textProtocolLabel } from '../../../../../shared/usage-display-labels'

export const TEXT_PROTOCOLS: TextEndpointType[] = [
  'openai',
  'openai-response',
  'anthropic',
  'gemini',
]
export const REQUEST_MODES = ['non_stream', 'stream'] as const
export type RequestMode = (typeof REQUEST_MODES)[number]
export const protocolPaths: Record<TextEndpointType, string> = {
  openai: '/v1/chat/completions',
  'openai-response': '/v1/responses',
  anthropic: '/v1/messages',
  gemini: ':generateContent',
}
export const classificationLabels = {
  confirmed: 'Confirmed',
  path_mismatch: 'Path mismatch',
  auth_error: 'Authentication failed',
  rate_limited: 'Rate limited',
  upstream_error: 'Upstream error',
  transport_error: 'Transport error',
} as const
export const probeCases = [
  'basic',
  'assistant_history',
  'tool_cycle',
  'reasoning_history',
  'invalid_tool_id',
  'tool_id_collision',
] as const
export const modeLabels = {
  non_stream: 'Non-streaming',
  stream: 'Streaming',
} as const
export const emptyPolicy: ChannelProtocolPolicy = {
  native: {},
  auto_convert: true,
  runtime_fallback: true,
  max_quality: 'fair',
}

export function profileFor(policy: ChannelProtocolPolicy, model: string) {
  const profiles = policy.model_overrides
  return profiles && Object.hasOwn(profiles, model)
    ? profiles[model]
    : undefined
}

// Existing full profiles pin every availability flag until the administrator restores inheritance.
export function protocolOverrides(
  profile?: ModelProtocolProfile
): ProtocolOverrides {
  if (!profile?.native) return profile?.overrides ?? {}
  return Object.fromEntries(
    TEXT_PROTOCOLS.map((endpoint) => {
      const cap = profile.native?.[endpoint]
      if (!cap) return [endpoint, { non_stream: false, stream: false }]
      const fixed = { ...cap, mode: cap.mode ?? 'native' }
      if (endpoint === 'anthropic' && fixed.mode === 'normalized') {
        fixed.reasoning_history ??= 'preserve'
      }
      return [endpoint, fixed]
    })
  )
}

export function setModelOverrides(
  policy: ChannelProtocolPolicy,
  model: string,
  overrides: ProtocolOverrides
): ChannelProtocolPolicy {
  const profiles = { ...policy.model_overrides }
  if (!Object.keys(overrides).length) delete profiles[model]
  else {
    return {
      ...policy,
      model_overrides: { ...profiles, [model]: { overrides } },
    }
  }
  return { ...policy, model_overrides: profiles }
}

export function withAvailability(
  native: ProtocolCapabilities,
  endpoint: TextEndpointType,
  flags: Partial<Pick<ProtocolCapability, RequestMode>>,
  normalize: boolean
): ProtocolCapabilities {
  const next = { ...native }
  const old = native[endpoint]
  const cap: ProtocolCapability = {
    non_stream: false,
    stream: false,
    ...old,
    ...flags,
  }
  if (
    !old &&
    normalize &&
    (endpoint === 'openai-response' || endpoint === 'anthropic')
  ) {
    cap.mode = 'normalized'
    if (endpoint === 'anthropic') cap.reasoning_history = 'strip'
  }
  if (cap.stream || cap.non_stream) next[endpoint] = cap
  else delete next[endpoint]
  return next
}

// Apply only a complete reachability matrix for a single model. Transient failures are inconclusive.
export function applyDetectedProtocols(
  policy: ChannelProtocolPolicy,
  model: string,
  results: ChannelProtocolProbeResponse[],
  scope: 'model' | 'channel',
  normalize: boolean
): ChannelProtocolPolicy | null {
  let native = policy.native
  const patches: ProtocolOverrides = {}
  const old = protocolOverrides(profileFor(policy, model))
  for (const endpoint of TEXT_PROTOCOLS) {
    const flags = { non_stream: false, stream: false }
    for (const key of REQUEST_MODES) {
      const matches = results.filter(
        (r) =>
          r.model === model &&
          r.endpoint_type === endpoint &&
          r.stream === (key === 'stream') &&
          r.probe_case === 'basic'
      )
      if (matches.length !== 1) return null
      const classification = matches[0].classification
      if (
        classification !== 'confirmed' &&
        classification !== 'path_mismatch'
      ) {
        return null
      }
      flags[key] = classification === 'confirmed'
    }
    native = withAvailability(native, endpoint, flags, normalize)
    if (scope === 'model') {
      const patch: Partial<ProtocolCapability> = {}
      for (const key of REQUEST_MODES) {
        if (flags[key] !== !!policy.native[endpoint]?.[key]) {
          patch[key] = flags[key]
        }
      }
      const wire = old[endpoint]
      if (flags.non_stream || flags.stream) {
        if (wire?.mode) patch.mode = wire.mode
        else if (!policy.native[endpoint] && native[endpoint]?.mode) {
          patch.mode = native[endpoint]?.mode
        }
        if (wire?.reasoning_history) {
          patch.reasoning_history = wire.reasoning_history
        } else if (
          patch.mode === 'normalized' &&
          !policy.native[endpoint] &&
          endpoint === 'anthropic'
        ) {
          patch.reasoning_history = 'strip'
        }
      }
      if (Object.keys(patch).length) patches[endpoint] = patch
    }
  }
  if (scope === 'model') return setModelOverrides(policy, model, patches)
  return Object.keys(native).length ? { ...policy, native } : null
}
