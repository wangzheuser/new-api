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
import { api } from '@/lib/api'

import type {
  ApiResponse,
  UserRateLimitConfig,
  UserRateLimitRule,
  UserRateLimitRulePage,
  UserRateLimitRulePayload,
  UserSearchPage,
} from './types'

export async function getUserRateLimitConfig(): Promise<UserRateLimitConfig> {
  const response = await api.get<ApiResponse<UserRateLimitConfig>>(
    '/api/user-model-rate-limits/config'
  )
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function updateUserRateLimitConfig(input: {
  delay_seconds: number
  default_response: { status_code: number; error_message: string }
  group_responses: Array<{
    group: string
    status_code: number
    error_message: string
  }>
}): Promise<UserRateLimitConfig> {
  const response = await api.put<ApiResponse<UserRateLimitConfig>>(
    '/api/user-model-rate-limits/config',
    input
  )
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function getUserRateLimitRules(input: {
  page: number
  pageSize: number
  keyword: string
  group: string
}): Promise<UserRateLimitRulePage> {
  const response = await api.get<ApiResponse<UserRateLimitRulePage>>(
    '/api/user-model-rate-limits/rules',
    {
      params: {
        page: input.page,
        page_size: input.pageSize,
        keyword: input.keyword || undefined,
        group: input.group || undefined,
      },
    }
  )
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function createUserRateLimitRule(
  input: UserRateLimitRulePayload
): Promise<UserRateLimitRule> {
  const response = await api.post<ApiResponse<UserRateLimitRule>>(
    '/api/user-model-rate-limits/rules',
    input
  )
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function updateUserRateLimitRule(
  id: number,
  input: UserRateLimitRulePayload
): Promise<UserRateLimitRule> {
  const response = await api.put<ApiResponse<UserRateLimitRule>>(
    `/api/user-model-rate-limits/rules/${id}`,
    input
  )
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function deleteUserRateLimitRule(id: number): Promise<void> {
  const response = await api.delete<ApiResponse<null>>(
    `/api/user-model-rate-limits/rules/${id}`
  )
  if (!response.data.success) throw new Error(response.data.message)
}

export async function searchUsers(keyword: string): Promise<UserSearchPage> {
  const response = await api.get<ApiResponse<UserSearchPage>>(
    '/api/user/search',
    {
      params: { keyword, p: 1, page_size: 20 },
    }
  )
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function getUserGroups(
  userId: number
): Promise<Record<string, { desc: string; ratio: number | string }>> {
  const response = await api.get<
    ApiResponse<Record<string, { desc: string; ratio: number | string }>>
  >('/api/user/self/groups', { params: { user_id: userId } })
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function getSystemGroups(): Promise<string[]> {
  const response = await api.get<ApiResponse<string[]>>('/api/group/')
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}
