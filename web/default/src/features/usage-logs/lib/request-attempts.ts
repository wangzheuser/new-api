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
import type { UsageLog } from '../data/schema'
import type { LogOtherData } from '../types'

export interface RequestAttempt {
  attempt: number | null
  channel: number
  channelName: string
  statusCode: number | null
  errorCode: string | number | undefined
  message: string
}

export function getAttemptMetadata(log: UsageLog): LogOtherData {
  try {
    const other: unknown = JSON.parse(log.other)
    if (other && typeof other === 'object' && !Array.isArray(other)) {
      return other as LogOtherData
    }
  } catch {
    return {}
  }
  return {}
}

export function canShowRetryDetails(log: UsageLog, isAdmin: boolean): boolean {
  const admin = getAttemptMetadata(log).admin_info
  const channels = admin?.use_channel
  return (
    isAdmin &&
    Boolean(log.request_id) &&
    ((Array.isArray(channels) && channels.length > 1) ||
      (Array.isArray(admin?.request_policy) &&
        admin.request_policy.some(
          (event) =>
            Number.isSafeInteger(event?.attempt) && (event.attempt ?? 0) > 1
        )))
  )
}

export function normalizeRequestAttempts(
  logs: UsageLog[],
  selected: UsageLog
): { attempts: RequestAttempt[]; incomplete: boolean } {
  const known = new Map<
    number,
    RequestAttempt & { intermediate: boolean; createdAt: number }
  >()
  const unknown: RequestAttempt[] = []
  let expected = 0
  let terminal = selected
  let incomplete = false

  for (const log of [selected, ...logs]) {
    const other = getAttemptMetadata(log)
    const chain = other.admin_info?.use_channel
    if (Array.isArray(chain)) expected = Math.max(expected, chain.length)
    if (Array.isArray(other.admin_info?.request_policy)) {
      for (const event of other.admin_info.request_policy) {
        if (Number.isSafeInteger(event?.attempt) && (event.attempt ?? 0) > 0) {
          expected = Math.max(expected, event.attempt ?? 0)
        }
      }
    }
  }
  const recordedTerminal =
    logs.find((log) => getAttemptMetadata(log).public_error === true) ??
    logs.find((log) => log.type === 2)
  if (recordedTerminal) terminal = recordedTerminal

  for (const log of logs) {
    const other = getAttemptMetadata(log)
    if (log.type !== 5 || other.public_error === true) continue
    const chain = other.admin_info?.use_channel
    let attempt = Array.isArray(chain) && chain.length > 0 ? chain.length : null
    if (attempt === null && Array.isArray(other.admin_info?.request_policy)) {
      for (const event of other.admin_info.request_policy) {
        if (Number.isSafeInteger(event?.attempt) && (event.attempt ?? 0) > 0) {
          attempt = event.attempt ?? null
        }
      }
    }
    const selectedChannel = Array.isArray(chain)
      ? Number(chain.at(-1))
      : Number.NaN
    const channel =
      Number.isSafeInteger(selectedChannel) && selectedChannel > 0
        ? selectedChannel
        : log.channel
    const status = other.status_code
    const item = {
      attempt,
      channel,
      channelName: channel === log.channel ? (log.channel_name ?? '') : '',
      statusCode:
        typeof status === 'number' && status >= 100 && status <= 599
          ? status
          : null,
      errorCode:
        typeof other.error_code === 'string' ||
        typeof other.error_code === 'number'
          ? other.error_code
          : undefined,
      message: log.content,
      intermediate: log.is_intermediate || other.intermediate === true,
      createdAt: log.created_at,
    }
    if (!item.message) incomplete = true
    if (attempt === null) {
      unknown.push(item)
      incomplete = true
      continue
    }
    const previous = known.get(attempt)
    if (
      !previous ||
      (item.intermediate && !previous.intermediate) ||
      (item.intermediate === previous.intermediate &&
        item.createdAt > previous.createdAt)
    ) {
      known.set(attempt, item)
    }
  }

  const terminalOther = getAttemptMetadata(terminal)
  const successful =
    terminal.type === 2 &&
    !terminalOther.violation_fee &&
    !terminalOther.violation_fee_code &&
    !terminalOther.violation_fee_marker &&
    (!terminalOther.stream_status ||
      terminalOther.stream_status.status === 'ok')
  const expectedFailures = successful ? expected - 1 : expected
  if (
    [...known.keys()].filter((attempt) => attempt <= expectedFailures)
      .length !== expectedFailures
  ) {
    incomplete = true
  }
  if (expected === 0) incomplete = true
  return {
    attempts: [
      ...[...known.values()].sort(
        (a, b) => (a.attempt ?? 0) - (b.attempt ?? 0)
      ),
      ...unknown,
    ],
    incomplete,
  }
}
