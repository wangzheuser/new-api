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
