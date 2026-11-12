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
import { AxiosError } from 'axios'

import { api, type ApiRequestConfig } from '@/lib/api'

import { usageLogSchema, type UsageLog } from './data/schema'
import type {
  GetLogsParams,
  GetLogsResponse,
  GetLogStatsParams,
  GetLogStatsResponse,
  GetMidjourneyLogsParams,
  GetTaskLogsParams,
  AdminUserOverview,
} from './types'

// ============================================================================
// Generic API Helpers
// ============================================================================

/**
 * Build query parameters while preserving explicit zero and false values.
 */
function buildQueryParams(params: Record<string, unknown>): URLSearchParams {
  const queryParams = new URLSearchParams()

  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') {
      queryParams.append(key, String(value))
    }
  })

  return queryParams
}

function buildApiPath(endpoint: string, isAdmin: boolean): string {
  return isAdmin ? endpoint : `${endpoint}/self`
}

async function fetchLogs<T>(
  endpoint: string,
  params: T,
  isAdmin: boolean,
  config?: ApiRequestConfig
): Promise<GetLogsResponse> {
  const paramRecord = params as unknown as Record<string, unknown>
  const queryParams = buildQueryParams({
    p: paramRecord.p || 1,
    page_size: paramRecord.page_size || 20,
    ...params,
  })
  const path = buildApiPath(endpoint, isAdmin)
  const res = await api.get(`${path}?${queryParams}`, config)
  return res.data
}

async function fetchLogStats<T>(
  endpoint: string,
  params: T,
  isAdmin: boolean
): Promise<GetLogStatsResponse> {
  const queryParams = buildQueryParams(
    params as unknown as Record<string, unknown>
  )
  const path = buildApiPath(endpoint, isAdmin)
  const res = await api.get(`${path}/stat?${queryParams}`)
  return res.data
}

// ============================================================================
// Common Log APIs
// ============================================================================

export const getAllLogs = (
  params: GetLogsParams = {},
  config?: ApiRequestConfig
) => fetchLogs('/api/log', params, true, config)

export async function getRequestAttemptLogs(
  requestId: string,
  signal?: AbortSignal
): Promise<UsageLog[]> {
  if (!requestId) throw new Error('Request ID is required')
  const logs: UsageLog[] = []
  for (let page = 1; ; page++) {
    const response = await getAllLogs(
      {
        request_id: requestId,
        latest_per_request: false,
        p: page,
        page_size: 100,
      },
      {
        signal,
        skipErrorHandler: true,
        skipBusinessError: true,
        disableDuplicate: true,
      }
    ).catch((error: unknown) => {
      // Keep detail failures in the dialog instead of the global HTTP 500 page.
      if (error instanceof AxiosError && error.response?.status === 500) {
        throw new Error('Failed to load retry details', { cause: error })
      }
      throw error
    })
    if (!response.success || !response.data) {
      throw new Error(response.message || 'Failed to load retry details')
    }
    if (!Number.isSafeInteger(response.data.total) || response.data.total < 0) {
      throw new Error('Retry details are incomplete')
    }
    const items = usageLogSchema.array().parse(response.data.items)
    logs.push(...items)
    if (logs.length >= response.data.total) return logs
    if (items.length === 0) {
      throw new Error('Retry details are incomplete')
    }
  }
}

export const getUserLogs = (
  params: Omit<GetLogsParams, 'username' | 'channel'> = {}
) => fetchLogs('/api/log', params, false)

export const getLogStats = (params: GetLogStatsParams = {}) =>
  fetchLogStats('/api/log', params, true)

export const getUserLogStats = (
  params: Omit<GetLogStatsParams, 'username' | 'channel'> = {}
) => fetchLogStats('/api/log', params, false)

export async function getAdminUserOverview(
  userId: number
): Promise<{ success: boolean; message?: string; data?: AdminUserOverview }> {
  const res = await api.get(`/api/user/${userId}/overview`)
  return res.data
}

// ============================================================================
// MjProxy (Drawing) Logs API
// ============================================================================

export const getAllMidjourneyLogs = (params: GetMidjourneyLogsParams) =>
  fetchLogs('/api/mj', params, true)

export const getUserMidjourneyLogs = (params: GetMidjourneyLogsParams) =>
  fetchLogs('/api/mj', params, false)

// ============================================================================
// Task Logs API
// ============================================================================

export const getAllTaskLogs = (params: GetTaskLogsParams) =>
  fetchLogs('/api/task', params, true)

export const getUserTaskLogs = (params: GetTaskLogsParams) =>
  fetchLogs('/api/task', params, false)
