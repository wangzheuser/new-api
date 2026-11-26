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
import { test } from 'node:test'

import {
  channelTestSampleMatches,
  readChannelTestImage,
  type ChannelTestAttachment,
} from '../lib/channel-test-image'

test('custom images and incomplete sample answers require manual review', () => {
  const image: ChannelTestAttachment = {
    mime_type: 'image/png',
    data: '',
    name: 'sample.png',
    width: 520,
    height: 260,
    expectedCode: '9AB234',
  }
  assert.equal(
    channelTestSampleMatches(
      image,
      'A blue circle and red square. Code: 9AB234.'
    ),
    true
  )
  assert.equal(
    channelTestSampleMatches(image, 'A blue circle and red square.'),
    false
  )
  assert.equal(channelTestSampleMatches(image, '9AB234'), false)
  assert.equal(
    channelTestSampleMatches(image, 'I cannot see the image.'),
    false
  )
  assert.equal(
    channelTestSampleMatches(
      { ...image, expectedCode: undefined },
      'A blue circle and red square. Code: 9AB234.'
    ),
    false
  )
})

test('invalid, multiple and oversized attachments are rejected before decoding', async () => {
  const png = new File(['test'], 'test.png', { type: 'image/png' })
  await assert.rejects(readChannelTestImage([]), /at most one/)
  await assert.rejects(readChannelTestImage([png, png]), /at most one/)
  await assert.rejects(
    readChannelTestImage([
      new File(['svg'], 'test.svg', { type: 'image/svg+xml' }),
    ]),
    /PNG, JPEG or WebP/
  )
  await assert.rejects(
    readChannelTestImage([
      new File([new Uint8Array(4 * 1024 * 1024 + 1)], 'large.png', {
        type: 'image/png',
      }),
    ]),
    /4 MiB/
  )
})
