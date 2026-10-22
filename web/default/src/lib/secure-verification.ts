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
import axios, { type AxiosError } from 'axios'

export interface VerificationRequiredInfo {
  code?: string
  message: string
  required: boolean
}

/**
 * Determine whether an Axios error indicates secure verification is required.
 */
export function isVerificationRequiredError(
  error: unknown
): error is AxiosError {
  if (!error || typeof error !== 'object') return false
  const axiosError = error as AxiosError<{ code?: string }>
  const status = axiosError.response?.status
  if (status !== 403) return false

  const code = axiosError.response?.data?.code
  if (!code) return false

  const verificationCodes = new Set([
    'VERIFICATION_REQUIRED',
    'VERIFICATION_EXPIRED',
    'VERIFICATION_INVALID',
    'SECURITY_PROOF_REQUIRED',
    'SECURITY_PROOF_EXPIRED',
    'SECURITY_PROOF_INVALID',
    'SECURITY_PROOF_CONSUMED',
    'SECURITY_PROOF_SCOPE_MISMATCH',
    'SECURITY_PROOF_CONTEXT_MISMATCH',
  ])

  return verificationCodes.has(code)
}

export class AuthOperationError extends Error {
  readonly code?: string

  constructor(message: string, code?: string, options?: ErrorOptions) {
    super(message, options)
    this.name = 'AuthOperationError'
    this.code = code
  }

  static from(
    error: unknown,
    fallback = 'Verification failed. Please try again.'
  ): AuthOperationError {
    if (error instanceof AuthOperationError) return error
    if (axios.isAxiosError<{ message?: string; code?: string }>(error)) {
      return new AuthOperationError(
        (error.response && error.response.status >= 500
          ? 'Please try again later.'
          : undefined) ||
          error.response?.data?.message ||
          error.message ||
          fallback,
        error.response?.data?.code,
        { cause: error }
      )
    }
    return new AuthOperationError(
      error instanceof Error ? error.message : fallback,
      undefined,
      { cause: error }
    )
  }
}

export const authRequestOptions = {
  skipBusinessError: true,
  skipErrorHandler: true,
}

export async function authResult<T>(
  request: Promise<{
    data: { success: boolean; message?: string; code?: string; data?: T }
  }>,
  fallback = 'Verification failed. Please try again.'
): Promise<T> {
  try {
    const { data: response } = await request
    if (!response.success || response.data === undefined) {
      throw new AuthOperationError(response.message || fallback, response.code)
    }
    return response.data
  } catch (error) {
    throw AuthOperationError.from(error, fallback)
  }
}

/**
 * Extract verification requirement info from an Axios error.
 */
export function extractVerificationInfo(
  error: unknown
): VerificationRequiredInfo {
  const axiosError = error as AxiosError<{ code?: string; message?: string }>
  const code = axiosError.response?.data?.code
  const message =
    axiosError.response?.data?.message ?? 'Secure verification is required'

  return {
    code,
    message,
    required: true,
  }
}
