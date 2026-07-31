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

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  installChunkLoadRecovery,
  isChunkLoadError,
  recoverFromChunkLoadError,
  refreshForLatestBuild,
} from './chunk-load-error.js';

test('detects Classic lazy chunk failures and reloads once', () => {
  assert.equal(
    isChunkLoadError(
      new TypeError(
        'error loading dynamically imported module: /static/js/old.js',
      ),
    ),
    true,
  );
  assert.equal(
    isChunkLoadError(new Error('Request failed with status 500')),
    false,
  );
  assert.equal(
    isChunkLoadError(new TypeError('Failed to load module script')),
    true,
  );

  const listeners = new Map();
  const storage = new Map();
  const replacements = [];
  const originalWindow = globalThis.window;
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      addEventListener: (type, listener) => listeners.set(type, listener),
      location: {
        href: 'https://example.test/console',
        replace: (url) => replacements.push(url),
      },
      sessionStorage: {
        getItem: (key) => storage.get(key) ?? null,
        setItem: (key, value) => storage.set(key, value),
      },
    },
  });

  try {
    installChunkLoadRecovery();
    listeners.get('unhandledrejection')?.({
      reason: new Error('Loading chunk 42 failed'),
    });
    listeners.get('unhandledrejection')?.({
      reason: new Error('Loading chunk 42 failed'),
    });
    assert.equal(replacements.length, 1);
    assert.match(replacements[0], /_newapi_reload=/);
  } finally {
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: originalWindow,
    });
  }
});

test('recovers once for each distinct Classic stale asset', () => {
  const storage = new Map();
  const replacements = [];
  const originalWindow = globalThis.window;
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      location: {
        href: 'https://example.test/log?type=all',
        replace: (url) => replacements.push(url),
      },
      sessionStorage: {
        getItem: (key) => storage.get(key) ?? null,
        setItem: (key, value) => storage.set(key, value),
      },
    },
  });

  try {
    const firstError = new TypeError(
      'Failed to fetch dynamically imported module: /static/first.js',
    );
    const secondError = new TypeError(
      'Failed to fetch dynamically imported module: /static/second.js',
    );

    assert.equal(recoverFromChunkLoadError(firstError), true);
    assert.equal(recoverFromChunkLoadError(firstError), false);
    assert.equal(recoverFromChunkLoadError(secondError), true);
    assert.equal(replacements.length, 2);
    assert.equal(storage.size, 2);
    assert.match(replacements[0], /type=all/);
  } finally {
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: originalWindow,
    });
  }
});

test('recovers once when Classic session storage is unavailable', () => {
  const replacements = [];
  const originalWindow = globalThis.window;
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      location: {
        href: 'https://example.test/token',
        replace: (url) => replacements.push(url),
      },
      sessionStorage: {
        getItem: () => {
          throw new Error('storage disabled');
        },
        setItem: () => {
          throw new Error('storage disabled');
        },
      },
    },
  });

  try {
    const error = new TypeError(
      'Failed to fetch dynamically imported module: /static/storage-off.js',
    );
    assert.equal(recoverFromChunkLoadError(error), true);
    assert.equal(recoverFromChunkLoadError(error), false);
    assert.equal(replacements.length, 1);
  } finally {
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: originalWindow,
    });
  }
});

test('Classic manual refresh always uses a fresh cache-busting marker', () => {
  const replacements = [];
  const originalWindow = globalThis.window;
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: {
      location: {
        href: 'https://example.test/console?_newapi_reload=old',
        replace: (url) => replacements.push(url),
      },
    },
  });

  try {
    refreshForLatestBuild();
    refreshForLatestBuild();
    assert.equal(replacements.length, 2);
    assert.doesNotMatch(replacements[0], /_newapi_reload=old/);
    assert.match(replacements[0], /_newapi_reload=/);
  } finally {
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: originalWindow,
    });
  }
});
