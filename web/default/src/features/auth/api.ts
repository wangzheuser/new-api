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
import { AuthOperationError } from '@/lib/secure-verification'

import {
  clearPasswordEncryptionCache,
  encryptPassword,
} from './lib/password-encryption'
import { getAffiliateCode, getRegistrationCode } from './lib/storage'
import type { VerificationOperation } from './secure-verification/types'
import type {
  LoginPayload,
  LoginResponse,
  Login2FAResponse,
  TwoFAPayload,
  RegisterPayload,
  ApiResponse,
} from './types'

// ============================================================================
// Authentication APIs
// ============================================================================

// ----------------------------------------------------------------------------
// Login & Logout
// ----------------------------------------------------------------------------

// User login with username and password
export async function login(payload: LoginPayload): Promise<LoginResponse> {
  const turnstile = payload.turnstile ?? ''
  try {
    const passwordFields = payload.passwordEncryptionEnabled
      ? await encryptPassword(payload.password)
      : { password: payload.password }
    const res = await api.post<LoginResponse>(
      `/api/user/login?turnstile=${turnstile}`,
      {
        username: payload.username,
        ...passwordFields,
      },
      { skipAuthRefresh: true }
    )
    if (payload.passwordEncryptionEnabled && !res.data?.success) {
      clearPasswordEncryptionCache()
    }
    return res.data
  } catch (error: unknown) {
    if (payload.passwordEncryptionEnabled) {
      clearPasswordEncryptionCache()
    }
    throw error
  }
}

// Two-factor authentication login
export async function login2fa(payload: TwoFAPayload) {
  const res = await api.post<Login2FAResponse>(
    '/api/user/login/verify',
    { flow_token: payload.flow_token, method: '2fa', code: payload.code },
    { skipAuthRefresh: true }
  )
  return res.data
}

// User logout
export async function logout(): Promise<ApiResponse> {
  const res = await api.post('/api/user/auth/logout', undefined, {
    skipAuthRefresh: true,
    skipErrorHandler: true,
  })
  return res.data
}

// ----------------------------------------------------------------------------
// Password Management
// ----------------------------------------------------------------------------

// Send password reset email
export async function sendPasswordResetEmail(
  email: string,
  turnstile?: string
): Promise<ApiResponse> {
  const res = await api.get('/api/reset_password', {
    params: { email, turnstile },
  })
  return res.data
}

// ----------------------------------------------------------------------------
// OAuth
// ----------------------------------------------------------------------------

// Start GitHub OAuth flow
export async function githubOAuthStart(clientId: string, state: string) {
  const url = `https://github.com/login/oauth/authorize?client_id=${clientId}&state=${state}&scope=user:email`
  window.open(url)
}

// Get OAuth state for CSRF protection
export async function getOAuthState(provider: string): Promise<string> {
  const res = await api.post(
    '/api/oauth/state',
    {
      provider,
      intent: 'login',
      aff: getAffiliateCode() || undefined,
      registration_code: getRegistrationCode() || undefined,
    },
    {
      skipAuthRefresh: true,
      skipErrorHandler: true,
      skipBusinessError: true,
    }
  )
  if (res.data?.success) {
    if (typeof res.data.data === 'string') return res.data.data
    if (typeof res.data.data?.flow_token === 'string') {
      return res.data.data.flow_token
    }
  }
  return ''
}

/**
 * Create a server-owned OAuth flow for login, binding, or verification.
 */
export async function createOAuthAuthorization(
  provider: string,
  intent: 'login' | 'bind' | 'verify',
  operation?: VerificationOperation,
  signal?: AbortSignal,
  proofToken?: string
): Promise<{ state: string; authorizationUrl?: string }> {
  const res = await api.post(
    '/api/oauth/state',
    {
      provider,
      intent,
      aff: intent === 'login' ? getAffiliateCode() || undefined : undefined,
      registration_code:
        intent === 'login' ? getRegistrationCode() || undefined : undefined,
      scope: operation?.scope,
      ...(operation?.context ? { context: operation.context } : {}),
    },
    {
      skipAuthRefresh: intent === 'login',
      skipErrorHandler: true,
      skipBusinessError: true,
      singleUseAuthorization: intent === 'bind' || intent === 'verify',
      signal,
      ...(proofToken ? { headers: { 'X-Security-Proof': proofToken } } : {}),
    }
  )
  if (res.data?.success) {
    const data = res.data.data
    if (typeof data === 'string') return { state: data }
    if (typeof data?.flow_token === 'string') {
      return {
        state: data.flow_token,
        authorizationUrl: data.authorization_url,
      }
    }
  }
  throw new AuthOperationError(
    res.data?.message || 'Failed to initialize OAuth',
    res.data?.code
  )
}

// WeChat login by authorization code
export async function wechatLoginByCode(code: string): Promise<ApiResponse> {
  const res = await api.get('/api/oauth/wechat', {
    params: { code },
    headers: getRegistrationCode()
      ? { 'X-Registration-Code': getRegistrationCode() }
      : undefined,
    skipAuthRefresh: true,
  })
  return res.data
}

// ----------------------------------------------------------------------------
// Registration
// ----------------------------------------------------------------------------

// User registration
export async function register(payload: RegisterPayload): Promise<ApiResponse> {
  const res = await api.post(`/api/user/register`, payload, {
    params: { turnstile: payload.turnstile ?? '' },
  })
  return res.data
}

// Send email verification code
export async function sendEmailVerification(
  email: string,
  turnstile?: string
): Promise<ApiResponse> {
  const res = await api.get('/api/verification', {
    params: { email, turnstile },
    skipBusinessError: true,
  })
  return res.data
}

// Bind email to OAuth account
export async function bindEmail(
  flowToken: string,
  newCode: string,
  oldCode = '',
  signal?: AbortSignal
): Promise<ApiResponse> {
  const res = await api.post(
    '/api/oauth/email/bind',
    { flow_token: flowToken, new_code: newCode, old_code: oldCode },
    { singleUseAuthorization: true, signal }
  )
  return res.data
}
