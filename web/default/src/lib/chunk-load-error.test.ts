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
import test from 'node:test'

import {
  installChunkLoadRecovery,
  isChunkLoadError,
  recoverFromChunkLoadError,
  refreshForLatestBuild,
} from './chunk-load-error'

test('detects stale deployment chunk errors without matching ordinary errors', () => {
  assert.equal(
    isChunkLoadError(
      new TypeError('Failed to fetch dynamically imported module: /static/a.js')
    ),
    true
  )
  assert.equal(
    isChunkLoadError(
      new TypeError('error loading dynamically imported module: /static/b.js')
    ),
    true
  )
  assert.equal(
    isChunkLoadError(new Error('Request failed with status 500')),
    false
  )
  assert.equal(
    isChunkLoadError({
      target: { src: 'https://example.test/static/js/async/old.js' },
    }),
    true
  )
})

test('reloads once when an asynchronous chunk rejection escapes the router', () => {
  const listeners = new Map<string, (event: unknown) => void>()
  const storage = new Map<string, string>()
  const replacements: string[] = []
  const originalWindow = globalThis.window

  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      addEventListener: (type: string, listener: (event: unknown) => void) => {
        listeners.set(type, listener)
      },
      location: {
        href: 'https://example.test/usage-logs/common',
        replace: (url: string) => replacements.push(url),
      },
      sessionStorage: {
        getItem: (key: string) => storage.get(key) ?? null,
        setItem: (key: string, value: string) => storage.set(key, value),
      },
    },
  })

  try {
    installChunkLoadRecovery()
    installChunkLoadRecovery()
    assert.equal(listeners.size, 2)

    listeners.get('unhandledrejection')?.({
      reason: new TypeError(
        'Failed to fetch dynamically imported module: /static/old.js'
      ),
    })
    listeners.get('unhandledrejection')?.({
      reason: new TypeError(
        'Failed to fetch dynamically imported module: /static/old.js'
      ),
    })
    assert.equal(replacements.length, 1)
    assert.match(replacements[0], /_newapi_reload=/)
  } finally {
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: originalWindow,
    })
  }
})

test('recovers once for each distinct stale asset on the same page', () => {
  const storage = new Map<string, string>()
  const replacements: string[] = []
  const originalWindow = globalThis.window

  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      location: {
        href: 'https://example.test/wallet?tab=history',
        replace: (url: string) => replacements.push(url),
      },
      sessionStorage: {
        getItem: (key: string) => storage.get(key) ?? null,
        setItem: (key: string, value: string) => storage.set(key, value),
      },
    },
  })

  try {
    const firstError = new TypeError(
      'Failed to fetch dynamically imported module: /static/first.js'
    )
    const secondError = new TypeError(
      'Failed to fetch dynamically imported module: /static/second.js'
    )

    assert.equal(recoverFromChunkLoadError(firstError), true)
    assert.equal(recoverFromChunkLoadError(firstError), false)
    assert.equal(recoverFromChunkLoadError(secondError), true)
    assert.equal(replacements.length, 2)
    assert.equal(storage.size, 2)
    assert.match(replacements[0], /tab=history/)
  } finally {
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: originalWindow,
    })
  }
})

test('falls back to one in-memory recovery when session storage is unavailable', () => {
  const replacements: string[] = []
  const originalWindow = globalThis.window

  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      location: {
        href: 'https://example.test/models',
        replace: (url: string) => replacements.push(url),
      },
      sessionStorage: {
        getItem: () => {
          throw new Error('storage disabled')
        },
        setItem: () => {
          throw new Error('storage disabled')
        },
      },
    },
  })

  try {
    const error = new TypeError(
      'Failed to fetch dynamically imported module: /static/storage-off.js'
    )
    assert.equal(recoverFromChunkLoadError(error), true)
    assert.equal(recoverFromChunkLoadError(error), false)
    assert.equal(replacements.length, 1)
  } finally {
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: originalWindow,
    })
  }
})

test('manual refresh always navigates with a fresh cache-busting marker', () => {
  const replacements: string[] = []
  const originalWindow = globalThis.window

  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      location: {
        href: 'https://example.test/usage-logs/common?_newapi_reload=old',
        replace: (url: string) => replacements.push(url),
      },
    },
  })

  try {
    refreshForLatestBuild()
    refreshForLatestBuild()
    assert.equal(replacements.length, 2)
    assert.doesNotMatch(replacements[0], /_newapi_reload=old/)
    assert.match(replacements[0], /_newapi_reload=/)
  } finally {
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: originalWindow,
    })
  }
})
