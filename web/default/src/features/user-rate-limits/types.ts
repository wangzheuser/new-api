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
export type ApiResponse<T> = {
  success: boolean
  message: string
  data: T
}

export type RateLimitResponse = {
  status_code: number
  error_message: string
}

export type GroupRateLimitResponse = RateLimitResponse & {
  group: string
}

export type UserRateLimitConfig = {
  base_limit: {
    enabled: boolean
    period_minutes: number
    total_count: number
    success_count: number
  }
  delay_seconds: number
  default_response: RateLimitResponse
  group_responses: GroupRateLimitResponse[]
}

export type UserSummary = {
  id: number
  username: string
  display_name: string
  email: string
  status: number
}

export type UserRateLimitRule = {
  id: number
  user: UserSummary
  group: string
  total_count: number
  success_count: number
  response: RateLimitResponse | null
  effective_response: RateLimitResponse & {
    source: 'global' | 'group' | 'user_group'
  }
  created_at: number
  updated_at: number
}

export type UserRateLimitRulePage = {
  page: number
  page_size: number
  total: number
  items: UserRateLimitRule[]
}

export type UserSearchPage = {
  page: number
  page_size: number
  total: number
  items: UserSummary[]
}

export type UserRateLimitRulePayload = {
  user_id: number
  group: string
  total_count: number
  success_count: number
  response: RateLimitResponse | null
}
