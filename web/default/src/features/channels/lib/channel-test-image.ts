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
import type { ChannelTestImage } from '../types'

export const MAX_CHANNEL_TEST_IMAGE_BYTES = 4 * 1024 * 1024

export type ChannelTestAttachment = ChannelTestImage & {
  name: string
  width: number
  height: number
  expectedCode?: string
}

export async function readChannelTestImage(
  files: File[]
): Promise<ChannelTestAttachment> {
  if (files.length !== 1) {
    throw new Error('Channel tests accept at most one image.')
  }
  const file = files[0]
  if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) {
    throw new Error('Use a PNG, JPEG or WebP image.')
  }
  if (file.size > MAX_CHANNEL_TEST_IMAGE_BYTES) {
    throw new Error('Test images must not exceed 4 MiB.')
  }
  let bitmap: ImageBitmap
  try {
    bitmap = await createImageBitmap(file)
  } catch {
    throw new Error('Invalid test image data.')
  }
  const { width, height } = bitmap
  bitmap.close()
  if (width > 4096 || height > 4096 || width * height > 16000000) {
    throw new Error(
      'Test images must fit within 4096 pixels and 16 megapixels.'
    )
  }
  const dataURL = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.addEventListener('load', () => resolve(String(reader.result)), {
      once: true,
    })
    reader.addEventListener(
      'error',
      () => reject(new Error('Invalid test image data.')),
      { once: true }
    )
    reader.readAsDataURL(file)
  })
  return {
    mime_type: file.type,
    data: dataURL.slice(dataURL.indexOf(',') + 1),
    name: file.name,
    width,
    height,
  }
}

export function createChannelTestSample(): ChannelTestAttachment {
  const canvas = document.createElement('canvas')
  canvas.width = 520
  canvas.height = 260
  const context = canvas.getContext('2d')
  if (!context) throw new Error('Unable to create the test image.')
  const expectedCode = [...crypto.getRandomValues(new Uint8Array(3))]
    .map((value) => value.toString(16).padStart(2, '0'))
    .join('')
    .toUpperCase()
  context.fillStyle = '#f4f0e7'
  context.fillRect(0, 0, 520, 260)
  context.fillStyle = '#177ee5'
  context.beginPath()
  context.arc(150, 105, 48, 0, 2 * Math.PI)
  context.fill()
  context.fillStyle = '#e65653'
  context.fillRect(315, 57, 96, 96)
  context.fillStyle = '#24324b'
  context.font = 'bold 36px monospace'
  context.textAlign = 'center'
  context.fillText(expectedCode, 260, 230)
  return {
    mime_type: 'image/png',
    data: canvas.toDataURL('image/png').split(',')[1],
    name: 'vision-check.png',
    width: 520,
    height: 260,
    expectedCode,
  }
}

export function channelTestSampleMatches(
  image: ChannelTestAttachment,
  content: string
): boolean {
  if (!image.expectedCode) return false
  const normalized = content.toLowerCase()
  return (
    normalized.includes(image.expectedCode.toLowerCase()) &&
    ['blue', 'circle', 'red', 'square'].every((word) =>
      normalized.includes(word)
    )
  )
}
