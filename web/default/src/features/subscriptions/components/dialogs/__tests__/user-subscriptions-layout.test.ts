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
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

test('long subscription tables keep their height so the drawer can scroll', () => {
  const source = readFileSync(
    new URL('../user-subscriptions-dialog.tsx', import.meta.url),
    'utf8'
  )

  assert.ok(
    /<StaticDataTable\b\s+className=['"][^'"]*\bshrink-0\b[^'"]*['"]/.test(
      source
    ),
    'The drawer must scroll the full table instead of shrinking and clipping it'
  )
})
