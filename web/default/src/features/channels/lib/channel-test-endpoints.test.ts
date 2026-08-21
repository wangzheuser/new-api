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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  CHANNEL_TEST_ENDPOINTS,
  channelTestEndpointSupportsStream,
  resolveChannelTestResponseKind,
  resolveChannelTestResultProtocol,
} from './channel-test-endpoints'

describe('channel test endpoint capabilities', () => {
  test('keeps all nine dialog choices in one capability table', () => {
    assert.deepEqual(
      CHANNEL_TEST_ENDPOINTS.map((endpoint) => [
        endpoint.value,
        endpoint.supportsStream,
        endpoint.responseKind,
      ]),
      [
        ['auto', true, 'dynamic'],
        ['openai', true, 'text'],
        ['openai-response', true, 'text'],
        ['openai-response-compact', false, 'structured'],
        ['anthropic', true, 'text'],
        ['gemini', true, 'text'],
        ['jina-rerank', false, 'structured'],
        ['image-generation', false, 'structured'],
        ['embeddings', false, 'structured'],
      ]
    )
  })

  test('derives stream availability from the shared table', () => {
    for (const endpoint of CHANNEL_TEST_ENDPOINTS) {
      assert.equal(
        channelTestEndpointSupportsStream(endpoint.value),
        endpoint.supportsStream
      )
    }
  })

  test('prefers the effective endpoint when choosing the detail layout', () => {
    assert.equal(
      resolveChannelTestResponseKind('embeddings', 'auto'),
      'structured'
    )
    assert.equal(
      resolveChannelTestResponseKind('openai-response', 'embeddings'),
      'text'
    )
    assert.equal(
      resolveChannelTestResponseKind(undefined, 'jina-rerank'),
      'structured'
    )
    assert.equal(resolveChannelTestResponseKind(undefined, 'auto'), 'text')
  })

  test('uses backend stream metadata and falls back for old responses', () => {
    assert.deepEqual(
      resolveChannelTestResultProtocol(false, {
        effective_endpoint_type: 'openai-response',
        stream: true,
      }),
      {
        effectiveEndpointType: 'openai-response',
        stream: true,
      }
    )
    assert.deepEqual(resolveChannelTestResultProtocol(true), {
      effectiveEndpointType: undefined,
      stream: true,
    })
  })
})
