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
import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'

import { mergePreviousAssets } from './merge-previous-assets.mjs'

test('keeps current assets and adds missing previous hashes', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'new-api-assets-'))
  const previous = path.join(root, 'previous')
  const current = path.join(root, 'current')
  const relative = path.join('static', 'js', 'async')

  await Promise.all([
    mkdir(path.join(previous, relative), { recursive: true }),
    mkdir(path.join(current, relative), { recursive: true }),
  ])
  await Promise.all([
    writeFile(path.join(previous, 'index.html'), 'previous'),
    writeFile(path.join(previous, relative, 'route.old.js'), 'old'),
    writeFile(path.join(previous, relative, 'route.same.js'), 'previous'),
    writeFile(path.join(current, relative, 'route.same.js'), 'current'),
  ])

  assert.deepEqual(await mergePreviousAssets(previous, current), {
    copied: 1,
    verified: 2,
  })
  assert.equal(
    await readFile(path.join(current, relative, 'route.old.js'), 'utf8'),
    'old'
  )
  assert.equal(
    await readFile(path.join(current, relative, 'route.same.js'), 'utf8'),
    'current'
  )
  await assert.rejects(
    () => mergePreviousAssets(current, current),
    /must be different/
  )
})
