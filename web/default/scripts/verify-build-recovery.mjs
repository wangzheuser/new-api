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

import { constants } from 'node:fs'
import { access, readFile } from 'node:fs/promises'
import path from 'node:path'

/**
 * Verify that a production build contains its entry assets and versioned
 * stale-chunk recovery marker.
 */
async function verifyBuildRecovery(dist, expectedVersion) {
  if (!expectedVersion) {
    throw new Error(
      'usage: node scripts/verify-build-recovery.mjs <expected-version> [dist]'
    )
  }

  const root = path.resolve(dist)
  const html = await readFile(path.join(root, 'index.html'), 'utf8')
  const assets = [
    ...html.matchAll(/(?:src|href)="(\/static\/[^"?#]+)(?:[?#][^"]*)?"/g),
  ].map((match) => match[1])
  if (assets.length === 0) {
    throw new Error('build index contains no static assets')
  }
  for (const asset of assets) {
    await access(path.join(root, asset.slice(1)), constants.R_OK)
  }

  const mainAsset = assets.find((asset) =>
    /\/static\/js\/index\.[0-9a-f]+\.js$/.test(asset)
  )
  if (!mainAsset) {
    throw new Error('build index does not reference a main JavaScript asset')
  }
  const main = await readFile(path.join(root, mainAsset.slice(1)), 'utf8')
  if (!main.includes('newapi:chunk-reload:')) {
    throw new Error('main asset does not install stale-chunk recovery')
  }
  if (!main.includes(expectedVersion)) {
    throw new Error(`main asset does not contain version ${expectedVersion}`)
  }

  return { assets: assets.length, mainAsset }
}

const expectedVersion =
  process.argv[2] ?? process.env.VITE_REACT_APP_VERSION ?? '0000'
const dist = process.argv[3] ?? 'dist'
const result = await verifyBuildRecovery(dist, expectedVersion)
process.stdout.write(
  `recovery_build_version=${expectedVersion}\nrecovery_main_asset=${result.mainAsset}\nentry_assets_verified=${result.assets}\n`
)
