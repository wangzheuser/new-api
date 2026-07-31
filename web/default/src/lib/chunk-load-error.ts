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

import { getBuildRevision } from '@/lib/build-metadata'

const CHUNK_ERROR_PATTERNS = [
  'chunkloaderror',
  'loading chunk',
  'loading css chunk',
  'failed to load module script',
  'failed to fetch dynamically imported module',
  'error loading dynamically imported module',
  'importing a module script failed',
  'expected a javascript-or-wasm module script',
]

const RELOAD_STORAGE_PREFIX = 'newapi:chunk-reload:'
const RELOAD_QUERY_PARAM = '_newapi_reload'

let recoveryInstalled = false
const memoryRecoveryAttempts = new Set<string>()

/**
 * Build a stable identity for the failed resource so independent stale chunks
 * can each trigger one recovery without allowing the same chunk to loop.
 */
function getChunkErrorIdentity(error: unknown): string {
  if (typeof error === 'string') return error.slice(0, 512)
  if (typeof error !== 'object' || error === null) return 'unknown'

  const candidate = error as {
    message?: unknown
    name?: unknown
    target?: { href?: unknown; src?: unknown }
  }
  const targetUrl = candidate.target?.src ?? candidate.target?.href
  if (typeof targetUrl === 'string' && targetUrl.length > 0) {
    return targetUrl.slice(0, 512)
  }

  const message = String(candidate.message ?? '')
  const assetUrl = message.match(
    /(?:https?:\/\/[^\s"'<>]+)?\/static\/[^\s"'<>]+\.(?:css|js)(?:[?#][^\s"'<>]*)?/i
  )?.[0]
  if (assetUrl) return assetUrl.slice(0, 512)

  return `${String(candidate.name ?? '')}:${message}`.slice(0, 512)
}

/**
 * Return the current page without the recovery marker used for cache busting.
 */
function getCanonicalPageUrl(): URL {
  const url = new URL(window.location.href)
  url.searchParams.delete(RELOAD_QUERY_PARAM)
  return url
}

/**
 * Detect errors caused by a browser requesting chunks from an older build.
 */
export function isChunkLoadError(error: unknown): boolean {
  if (typeof error === 'string') {
    const text = error.toLowerCase()
    return CHUNK_ERROR_PATTERNS.some((pattern) => text.includes(pattern))
  }
  if (typeof error !== 'object' || error === null) return false

  const candidate = error as {
    message?: unknown
    name?: unknown
    target?: { href?: unknown; src?: unknown }
  }
  const text =
    `${String(candidate.name ?? '')}: ${String(candidate.message ?? '')}`.toLowerCase()
  if (CHUNK_ERROR_PATTERNS.some((pattern) => text.includes(pattern))) {
    return true
  }

  const assetUrl = String(candidate.target?.src ?? candidate.target?.href ?? '')
  return /\/static\/.+\.(?:css|js)(?:[?#]|$)/.test(assetUrl)
}

/**
 * Reload once per failed resource and build when a stale chunk is detected.
 */
export function recoverFromChunkLoadError(error: unknown): boolean {
  if (typeof window === 'undefined' || !isChunkLoadError(error)) return false

  const revision = getBuildRevision()
  const canonicalUrl = getCanonicalPageUrl()
  const recoveryId = `${revision}:${canonicalUrl.href}:${getChunkErrorIdentity(error)}`
  const reloadKey = `${RELOAD_STORAGE_PREFIX}${recoveryId}`

  try {
    if (window.sessionStorage.getItem(reloadKey) === '1') return false
    window.sessionStorage.setItem(reloadKey, '1')
  } catch {
    // Keep recovery available when browser privacy settings disable storage.
    if (
      memoryRecoveryAttempts.has(recoveryId) ||
      new URL(window.location.href).searchParams.has(RELOAD_QUERY_PARAM)
    ) {
      return false
    }
    memoryRecoveryAttempts.add(recoveryId)
  }

  canonicalUrl.searchParams.set(RELOAD_QUERY_PARAM, revision)
  window.location.replace(canonicalUrl.href)
  return true
}

/**
 * Force a navigation with a unique cache-busting marker from the error page.
 */
export function refreshForLatestBuild(): void {
  if (typeof window === 'undefined') return

  const url = getCanonicalPageUrl()
  url.searchParams.set(
    RELOAD_QUERY_PARAM,
    `${getBuildRevision()}.${Date.now().toString(36)}`
  )
  window.location.replace(url.href)
}

/**
 * Install eager listeners before the router starts loading asynchronous routes.
 */
export function installChunkLoadRecovery(): void {
  if (typeof window === 'undefined' || recoveryInstalled) return
  recoveryInstalled = true

  window.addEventListener(
    'error',
    (event) => {
      recoverFromChunkLoadError(event.error ?? event)
    },
    true
  )
  window.addEventListener('unhandledrejection', (event) => {
    recoverFromChunkLoadError(event.reason)
  })
}
