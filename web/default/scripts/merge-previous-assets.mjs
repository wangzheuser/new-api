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
import { access, copyFile, mkdir, readdir } from 'node:fs/promises'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

/**
 * Merge hashed assets from the previous production build without replacing
 * files produced by the current build.
 */
export async function mergePreviousAssets(previousDist, currentDist) {
  if (path.resolve(previousDist) === path.resolve(currentDist)) {
    throw new Error('previous and current dist directories must be different')
  }
  await access(path.resolve(previousDist, 'index.html'), constants.R_OK)

  const sourceRoot = path.resolve(previousDist, 'static')
  const targetRoot = path.resolve(currentDist, 'static')
  let copied = 0
  let verified = 0

  /**
   * Copy one directory recursively while preserving current-build files.
   */
  async function copyDirectory(source, target) {
    await mkdir(target, { recursive: true })
    for (const entry of await readdir(source, { withFileTypes: true })) {
      const sourcePath = path.join(source, entry.name)
      const targetPath = path.join(target, entry.name)
      if (entry.isDirectory()) {
        await copyDirectory(sourcePath, targetPath)
      } else if (entry.isFile()) {
        verified += 1
        try {
          await copyFile(sourcePath, targetPath, constants.COPYFILE_EXCL)
          copied += 1
        } catch (error) {
          if (error?.code !== 'EEXIST') throw error
        }
        await access(targetPath, constants.R_OK)
      }
    }
  }

  await copyDirectory(sourceRoot, targetRoot)
  if (verified === 0) {
    throw new Error('previous dist contains no static assets')
  }
  return { copied, verified }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const previousDist = process.argv[2]
  const currentDist = process.argv[3] ?? 'dist'
  if (!previousDist) {
    throw new Error(
      'usage: node scripts/merge-previous-assets.mjs <previous-dist> [current-dist]'
    )
  }
  const result = await mergePreviousAssets(previousDist, currentDist)
  process.stdout.write(
    `merged_previous_assets=${result.copied}\nverified_previous_assets=${result.verified}\n`
  )
}
