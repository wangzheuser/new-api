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
import { authRequestOptions, authResult } from '@/lib/secure-verification'

import type {
  SecurityProof,
  VerificationOperation,
} from '../secure-verification/types'
import type { ApiResponse, PasskeyOptionsPayload, PasskeyStatus } from './types'

export async function getPasskeyStatus(): Promise<ApiResponse<PasskeyStatus>> {
  const res = await api.get<ApiResponse<PasskeyStatus>>('/api/user/passkey')
  return res.data
}

export async function beginPasskeyRegistration(
  proofToken?: string,
  signal?: AbortSignal
): Promise<{ options: PasskeyOptionsPayload; flow_token: string }> {
  return authResult(
    api.post('/api/user/passkey/register/begin', undefined, {
      ...authRequestOptions,
      ...(proofToken ? { headers: { 'X-Security-Proof': proofToken } } : {}),
      signal,
    })
  )
}

export async function finishPasskeyRegistration(
  payload: Record<string, unknown>,
  signal?: AbortSignal
): Promise<ApiResponse> {
  const res = await api.post<ApiResponse>(
    '/api/user/passkey/register/finish',
    payload,
    { ...authRequestOptions, signal }
  )
  return res.data
}

export async function deletePasskey(
  proofToken?: string,
  signal?: AbortSignal
): Promise<ApiResponse> {
  const res = await api.delete<ApiResponse>('/api/user/passkey', {
    ...authRequestOptions,
    ...(proofToken ? { headers: { 'X-Security-Proof': proofToken } } : {}),
    signal,
  })
  return res.data
}

export async function beginPasskeyLogin(): Promise<
  ApiResponse<PasskeyOptionsPayload>
> {
  const res = await api.post<ApiResponse<PasskeyOptionsPayload>>(
    '/api/user/passkey/login/begin'
  )
  return res.data
}

export async function finishPasskeyLogin(
  payload: Record<string, unknown>
): Promise<ApiResponse> {
  const res = await api.post<ApiResponse>(
    '/api/user/passkey/login/finish',
    payload
  )
  return res.data
}

export async function beginPasskeyLoginVerification(
  loginFlowToken: string,
  signal?: AbortSignal
): Promise<{
  flow_token: string
  options: PasskeyOptionsPayload
}> {
  return authResult(
    api.post(
      '/api/user/login/passkey/begin',
      { flow_token: loginFlowToken },
      { ...authRequestOptions, skipAuthRefresh: true, signal }
    )
  )
}

export async function finishPasskeyLoginVerification(
  loginFlowToken: string,
  passkeyFlowToken: string,
  credential: Record<string, unknown>,
  signal?: AbortSignal
): Promise<import('@/stores/auth-store').AuthBundle> {
  return authResult(
    api.post(
      '/api/user/login/passkey/finish',
      {
        flow_token: loginFlowToken,
        passkey_flow_token: passkeyFlowToken,
        credential,
      },
      { ...authRequestOptions, skipAuthRefresh: true, signal }
    )
  )
}

export async function beginPasskeyVerification(
  operation: VerificationOperation,
  signal?: AbortSignal
): Promise<{
  options: PasskeyOptionsPayload
  flow_token: string
}> {
  return authResult(
    api.post(
      '/api/user/passkey/verify/begin',
      { scope: operation.scope, context: operation.context },
      { ...authRequestOptions, signal }
    )
  )
}

export async function finishPasskeyVerification(
  flowToken: string,
  credential: Record<string, unknown>,
  signal?: AbortSignal
): Promise<SecurityProof> {
  return authResult(
    api.post(
      '/api/user/passkey/verify/finish',
      { flow_token: flowToken, credential },
      { ...authRequestOptions, signal }
    )
  )
}
