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
import axios, { type AxiosError, type AxiosRequestConfig } from 'axios'
import { t } from 'i18next'
import { toast } from 'sonner'

import { isAuthBundle } from '@/features/auth/lib/login-flow'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

declare module 'axios' {
  export interface AxiosRequestConfig {
    skipBusinessError?: boolean
    skipErrorHandler?: boolean
    disableDuplicate?: boolean
    skipAuthRefresh?: boolean
    authRetry?: boolean
  }
}

export type ApiRequestConfig = AxiosRequestConfig

// ============================================================================
// Axios Instance Configuration
// ============================================================================

// Base URL: empty string for same-origin API requests
const baseURL = ''

// Create axios instance with default config
export const api = axios.create({
  baseURL,
  withCredentials: true, // Include cookies in cross-origin requests
  headers: {
    'Cache-Control': 'no-store', // Prevent caching
  },
})

export function applyAuthBundle(bundle: AuthBundle): void {
  useAuthStore.getState().auth.setBundle(bundle)
}

let refreshPromise: Promise<boolean> | null = null

async function refreshAuthentication(): Promise<boolean> {
  if (!refreshPromise) {
    refreshPromise = (async () => {
      try {
        const response = await api.post('/api/user/auth/refresh', undefined, {
          skipAuthRefresh: true,
          skipErrorHandler: true,
        })
        const bundle = response.data?.success ? response.data.data : undefined
        if (!isAuthBundle(bundle)) {
          useAuthStore.getState().auth.reset()
          return false
        }
        applyAuthBundle(bundle)
        return true
      } catch {
        useAuthStore.getState().auth.reset()
        return false
      } finally {
        refreshPromise = null
      }
    })()
  }
  return refreshPromise
}

// ============================================================================
// Request Deduplication
// ============================================================================

// Deduplicate concurrent GET requests to the same URL
// Prevents multiple identical requests from being sent simultaneously
const inFlightGet = new Map<string, Promise<unknown>>()
const originalGet = api.get.bind(api)

api.get = ((url: string, config: ApiRequestConfig = {}) => {
  const disableDuplicate = config.disableDuplicate
  if (disableDuplicate) return originalGet(url, config)

  const params = config.params ? JSON.stringify(config.params) : '{}'
  const key = `${url}?${params}`

  // Return existing in-flight request if available
  const existingRequest = inFlightGet.get(key)
  if (existingRequest) return existingRequest

  // Create new request and clean up after completion
  const req = originalGet(url, config).finally(() => inFlightGet.delete(key))
  inFlightGet.set(key, req)
  return req
}) as typeof api.get

// ============================================================================
// Response Interceptor
// ============================================================================

// Handle business logic errors and HTTP errors globally
api.interceptors.response.use(
  (response) => {
    const skipBusiness = response.config.skipBusinessError

    // Unified business response format: { success, message, data }
    if (
      !skipBusiness &&
      response &&
      response.data &&
      typeof response.data.success === 'boolean'
    ) {
      if (!response.data.success) {
        // Show error toast for business failures
        const msg = response.data.message || t('Request failed')
        toast.error(msg)
      }
    }
    return response
  },
  async (error: AxiosError) => {
    const config = error.config as ApiRequestConfig | undefined
    const skip = config?.skipErrorHandler
    const status = error?.response?.status

    if (status === 401) {
      if (config && !config.skipAuthRefresh && !config.authRetry) {
        config.authRetry = true
        if (await refreshAuthentication()) {
          const accessToken = useAuthStore.getState().auth.accessToken
          if (accessToken) {
            config.headers = {
              ...config.headers,
              Authorization: `Bearer ${accessToken}`,
            }
          }
          return api.request(config)
        }
      } else if (!config?.skipAuthRefresh) {
        useAuthStore.getState().auth.reset()
      }

      if (!skip) {
        toast.error(t('Session expired!'))
      }
    } else if (!skip) {
      // Other errors: show error message from response or default
      const responseData = error.response?.data as
        | { message?: string }
        | undefined
      const msg = responseData?.message || error.message || t('Request failed')
      toast.error(msg)
    }
    return Promise.reject(error)
  }
)

// ============================================================================
// Common Headers Utility
// ============================================================================

/**
 * Get user ID from localStorage
 */
function getUserId(): string | null {
  try {
    if (typeof window !== 'undefined') {
      return window.localStorage.getItem('uid')
    }
  } catch {
    /* empty */
  }
  return null
}

/**
 * Get common request headers (for both axios and SSE requests)
 */
export function getCommonHeaders(): Record<string, string> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }

  const uid = getUserId()
  if (uid) {
    headers['New-Api-User'] = uid
  }
  const accessToken = useAuthStore.getState().auth.accessToken
  if (accessToken) {
    headers.Authorization = `Bearer ${accessToken}`
  }

  return headers
}

// ============================================================================
// Request Interceptor
// ============================================================================

// Attach user ID header for all requests
api.interceptors.request.use((config) => {
  const uid = getUserId()
  if (uid) {
    // Custom header for user identification
    ;(config.headers as Record<string, string>)['New-Api-User'] = uid
  }
  const accessToken = useAuthStore.getState().auth.accessToken
  if (accessToken) {
    config.headers.Authorization = `Bearer ${accessToken}`
  }
  return config
})

// ============================================================================
// Common API Functions
// ============================================================================

// ----------------------------------------------------------------------------
// User APIs
// ----------------------------------------------------------------------------

// Get current user info
export async function getSelf() {
  const res = await api.get('/api/user/self', {
    // Avoid global 401 toast during guards/preloads
    skipErrorHandler: true,
  })
  return res.data
}

// Get user available models
export async function getUserModels(targetUserId?: number): Promise<{
  success: boolean
  message?: string
  data?: string[]
}> {
  const res = await api.get('/api/user/models', {
    params: targetUserId ? { user_id: targetUserId } : undefined,
  })
  return res.data
}

// Get user groups with descriptions and ratios
export async function getUserGroups(targetUserId?: number): Promise<{
  success: boolean
  message?: string
  data?: Record<string, { desc: string; ratio: number | string }>
}> {
  const res = await api.get('/api/user/self/groups', {
    params: targetUserId ? { user_id: targetUserId } : undefined,
  })
  return res.data
}

// ----------------------------------------------------------------------------
// System APIs
// ----------------------------------------------------------------------------

// Get system status
export async function getStatus() {
  const res = await api.get('/api/status')
  return res.data?.data as Record<string, unknown>
}

// Get system notice
export async function getNotice(): Promise<{
  success: boolean
  message?: string
  data?: string
}> {
  const res = await api.get('/api/notice')
  return res.data
}

// ----------------------------------------------------------------------------
// 2FA Management APIs
// ----------------------------------------------------------------------------

// Get 2FA status
export async function get2FAStatus() {
  const res = await api.get('/api/user/2fa/status')
  return res.data
}

// Setup 2FA
export async function setup2FA() {
  const res = await api.post('/api/user/2fa/setup')
  return res.data
}

// Enable 2FA with verification code
export async function enable2FA(code: string) {
  const res = await api.post('/api/user/2fa/enable', { code })
  return res.data
}

// Disable 2FA with verification code
export async function disable2FA(code: string) {
  const res = await api.post('/api/user/2fa/disable', { code })
  return res.data
}

// Regenerate 2FA backup codes
export async function regenerate2FABackupCodes(code: string) {
  const res = await api.post('/api/user/2fa/backup_codes', { code })
  return res.data
}
