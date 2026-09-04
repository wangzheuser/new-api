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
import {
  MULTI_KEY_STATUS_CONFIG,
  MULTI_KEY_CONFIRM_MESSAGES,
} from '../constants'
import type {
  KeyStatus,
  MultiKeyConfirmAction,
  MultiKeyTestResult,
} from '../types'

/**
 * Get status badge configuration for multi-key status
 */
export function getMultiKeyStatusConfig(status: number) {
  return (
    MULTI_KEY_STATUS_CONFIG[status as keyof typeof MULTI_KEY_STATUS_CONFIG] || {
      variant: 'neutral' as const,
      label: 'Unknown',
    }
  )
}

/** Resolve the visible status without changing the legacy numeric status contract. */
export function getMultiKeyEffectiveStatusConfig(key: KeyStatus) {
  if (key.temporary_disabled) {
    return {
      variant: 'warning' as const,
      label: 'Temporary Disabled',
    }
  }
  return getMultiKeyStatusConfig(key.status)
}

/** Return the rounded-up remaining cooldown shown by the key status table. */
export function getMultiKeyCooldownMinutes(
  disabledUntil: number,
  nowSeconds: number
): number {
  return Math.max(0, Math.ceil((disabledUntil - nowSeconds) / 60))
}

/**
 * Get confirmation message for multi-key action
 */
export function getMultiKeyConfirmMessage(
  action: MultiKeyConfirmAction | null
): string {
  if (!action) return ''

  switch (action.type) {
    case 'delete':
      return MULTI_KEY_CONFIRM_MESSAGES.DELETE
    case 'enable':
      return MULTI_KEY_CONFIRM_MESSAGES.ENABLE
    case 'disable':
      return MULTI_KEY_CONFIRM_MESSAGES.DISABLE
    case 'enable-all':
      return MULTI_KEY_CONFIRM_MESSAGES.ENABLE_ALL
    case 'disable-all':
      return MULTI_KEY_CONFIRM_MESSAGES.DISABLE_ALL
    case 'delete-disabled':
      return MULTI_KEY_CONFIRM_MESSAGES.DELETE_DISABLED
    case 'disable-unavailable':
      return 'Disable all keys whose latest test result is unavailable?'
    case 'enable-available':
      return 'Enable all keys whose latest test result is available?'
    default:
      return ''
  }
}

/**
 * Check if action is destructive
 */
export function isDestructiveAction(
  action: MultiKeyConfirmAction | null
): boolean {
  if (!action) return false
  return (
    action.type === 'delete' ||
    action.type === 'delete-disabled' ||
    action.type === 'disable-all' ||
    action.type === 'disable-unavailable'
  )
}

/** Selects batch action targets from completed test results. */
export function getMultiKeyTestActionIndexes(
  results: Iterable<MultiKeyTestResult>
): { available: number[]; unavailable: number[] } {
  const available: number[] = []
  const unavailable: number[] = []

  for (const result of results) {
    if (result.classification === 'available') {
      available.push(result.key_index)
    } else {
      unavailable.push(result.key_index)
    }
  }

  return { available, unavailable }
}
