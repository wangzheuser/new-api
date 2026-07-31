/*
Copyright (C) 2025 QuantumNous

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

/* global __APP_BUILD_VERSION__ */

const CHUNK_ERROR_PATTERNS = [
  'chunkloaderror',
  'loading chunk',
  'loading css chunk',
  'failed to load module script',
  'failed to fetch dynamically imported module',
  'error loading dynamically imported module',
  'importing a module script failed',
  'expected a javascript-or-wasm module script',
];

const RELOAD_STORAGE_PREFIX = 'newapi:chunk-reload:classic:';
const RELOAD_QUERY_PARAM = '_newapi_reload';

let recoveryInstalled = false;
const memoryRecoveryAttempts = new Set();

/**
 * Return the build revision embedded by Rsbuild.
 */
function getBuildRevision() {
  return typeof __APP_BUILD_VERSION__ === 'string'
    ? __APP_BUILD_VERSION__
    : '0000';
}

/**
 * Build a stable identity for the failed resource.
 */
function getChunkErrorIdentity(error) {
  if (typeof error === 'string') return error.slice(0, 512);
  if (typeof error !== 'object' || error === null) return 'unknown';

  const targetUrl = error.target?.src ?? error.target?.href;
  if (typeof targetUrl === 'string' && targetUrl.length > 0) {
    return targetUrl.slice(0, 512);
  }

  const message = String(error.message ?? '');
  const assetUrl = message.match(
    /(?:https?:\/\/[^\s"'<>]+)?\/static\/[^\s"'<>]+\.(?:css|js)(?:[?#][^\s"'<>]*)?/i,
  )?.[0];
  if (assetUrl) return assetUrl.slice(0, 512);

  return `${String(error.name ?? '')}:${message}`.slice(0, 512);
}

/**
 * Return the current page without the recovery cache-busting marker.
 */
function getCanonicalPageUrl() {
  const url = new URL(window.location.href);
  url.searchParams.delete(RELOAD_QUERY_PARAM);
  return url;
}

/**
 * Detect stale JavaScript and CSS chunk load failures.
 */
export function isChunkLoadError(error) {
  if (typeof error === 'string') {
    const text = error.toLowerCase();
    return CHUNK_ERROR_PATTERNS.some((pattern) => text.includes(pattern));
  }
  if (typeof error !== 'object' || error === null) return false;

  const text =
    `${String(error.name ?? '')}: ${String(error.message ?? '')}`.toLowerCase();
  if (CHUNK_ERROR_PATTERNS.some((pattern) => text.includes(pattern))) {
    return true;
  }

  const assetUrl = String(error.target?.src ?? error.target?.href ?? '');
  return /\/static\/.+\.(?:css|js)(?:[?#]|$)/.test(assetUrl);
}

/**
 * Reload once per failed resource and build.
 */
export function recoverFromChunkLoadError(error) {
  if (typeof window === 'undefined' || !isChunkLoadError(error)) return false;

  const revision = getBuildRevision();
  const canonicalUrl = getCanonicalPageUrl();
  const recoveryId = `${revision}:${canonicalUrl.href}:${getChunkErrorIdentity(error)}`;
  const reloadKey = `${RELOAD_STORAGE_PREFIX}${recoveryId}`;

  try {
    if (window.sessionStorage.getItem(reloadKey) === '1') {
      return false;
    }
    window.sessionStorage.setItem(reloadKey, '1');
  } catch {
    // Keep one recovery attempt when browser privacy settings disable storage.
    if (
      memoryRecoveryAttempts.has(recoveryId) ||
      new URL(window.location.href).searchParams.has(RELOAD_QUERY_PARAM)
    ) {
      return false;
    }
    memoryRecoveryAttempts.add(recoveryId);
  }

  canonicalUrl.searchParams.set(RELOAD_QUERY_PARAM, revision);
  window.location.replace(canonicalUrl.href);
  return true;
}

/**
 * Force navigation to the latest build from the rendered error page.
 */
export function refreshForLatestBuild() {
  if (typeof window === 'undefined') return;

  const url = getCanonicalPageUrl();
  url.searchParams.set(
    RELOAD_QUERY_PARAM,
    `${getBuildRevision()}.${Date.now().toString(36)}`,
  );
  window.location.replace(url.href);
}

/**
 * Install recovery listeners before React starts resolving lazy routes.
 */
export function installChunkLoadRecovery() {
  if (typeof window === 'undefined' || recoveryInstalled) return;
  recoveryInstalled = true;

  window.addEventListener(
    'error',
    (event) => {
      recoverFromChunkLoadError(event.error ?? event);
    },
    true,
  );
  window.addEventListener('unhandledrejection', (event) => {
    recoverFromChunkLoadError(event.reason);
  });
}
