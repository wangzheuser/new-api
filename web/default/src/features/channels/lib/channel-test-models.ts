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

/** Returns tested model names with the requested completed status. */
export function getChannelTestModelsWithStatus(
  models: readonly string[],
  results: Readonly<Record<string, { status: string } | undefined>>,
  status: 'success' | 'error'
): string[] {
  return models.filter((model) => results[model]?.status === status)
}

/** Resolves selected names against the complete model list, not the visible page. */
export function getSelectedChannelTestModels(
  models: readonly string[],
  selection: Readonly<Record<string, boolean>>
): string[] {
  return models.filter((model) => selection[model] === true)
}
