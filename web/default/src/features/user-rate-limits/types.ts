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
