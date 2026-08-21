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

export type ChannelTestResponseKind = 'dynamic' | 'text' | 'structured'

export type ChannelTestEndpointConfig = {
  value: string
  label: string
  supportsStream: boolean
  responseKind: ChannelTestResponseKind
}

export type ChannelTestResultProtocol = {
  effectiveEndpointType?: string
  stream: boolean
}

export const CHANNEL_TEST_ENDPOINTS: readonly ChannelTestEndpointConfig[] = [
  {
    value: 'auto',
    label: 'Auto detect (default)',
    supportsStream: true,
    responseKind: 'dynamic',
  },
  {
    value: 'openai',
    label: 'OpenAI (/v1/chat/completions)',
    supportsStream: true,
    responseKind: 'text',
  },
  {
    value: 'openai-response',
    label: 'OpenAI Responses (/v1/responses)',
    supportsStream: true,
    responseKind: 'text',
  },
  {
    value: 'openai-response-compact',
    label: 'OpenAI Response Compaction (/v1/responses/compact)',
    supportsStream: false,
    responseKind: 'structured',
  },
  {
    value: 'anthropic',
    label: 'Anthropic (/v1/messages)',
    supportsStream: true,
    responseKind: 'text',
  },
  {
    value: 'gemini',
    label: 'Gemini (/v1beta/models/{model}:generateContent)',
    supportsStream: true,
    responseKind: 'text',
  },
  {
    value: 'jina-rerank',
    label: 'Jina Rerank (/v1/rerank)',
    supportsStream: false,
    responseKind: 'structured',
  },
  {
    value: 'image-generation',
    label: 'Image Generation (/v1/images/generations)',
    supportsStream: false,
    responseKind: 'structured',
  },
  {
    value: 'embeddings',
    label: 'Embeddings (/v1/embeddings)',
    supportsStream: false,
    responseKind: 'structured',
  },
]

/** Returns the shared UI capability record for one channel-test endpoint. */
export function getChannelTestEndpointConfig(
  endpointType?: string
): ChannelTestEndpointConfig | undefined {
  return CHANNEL_TEST_ENDPOINTS.find(
    (endpoint) => endpoint.value === endpointType
  )
}

/** Reports whether the selected endpoint supports streaming channel tests. */
export function channelTestEndpointSupportsStream(
  endpointType: string
): boolean {
  return getChannelTestEndpointConfig(endpointType)?.supportsStream ?? false
}

/** Resolves the detail layout from backend metadata before falling back to the selected endpoint. */
export function resolveChannelTestResponseKind(
  effectiveEndpointType?: string,
  selectedEndpointType = 'auto'
): Exclude<ChannelTestResponseKind, 'dynamic'> {
  const effectiveKind = getChannelTestEndpointConfig(
    effectiveEndpointType
  )?.responseKind
  if (effectiveKind && effectiveKind !== 'dynamic') {
    return effectiveKind
  }

  const selectedKind =
    getChannelTestEndpointConfig(selectedEndpointType)?.responseKind
  return selectedKind === 'structured' ? 'structured' : 'text'
}

/** Uses backend-confirmed protocol metadata while remaining compatible with older responses. */
export function resolveChannelTestResultProtocol(
  requestedStream: boolean,
  details?: { effective_endpoint_type?: string; stream?: boolean }
): ChannelTestResultProtocol {
  return {
    effectiveEndpointType: details?.effective_endpoint_type,
    stream: details?.stream ?? requestedStream,
  }
}
