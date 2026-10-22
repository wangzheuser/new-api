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
import { buildOAuthAuthorizationUrl } from '@/lib/oauth'
import {
  buildAssertionResult,
  prepareCredentialRequestOptions,
  isPasskeySupported as detectPasskeySupport,
} from '@/lib/passkey'
import {
  AuthOperationError,
  authRequestOptions,
  authResult,
} from '@/lib/secure-verification'

import { createOAuthAuthorization } from '../api'
import { openOAuthPopup } from '../lib/oauth-popup'
import { encryptPassword } from '../lib/password-encryption'
import { beginPasskeyVerification, finishPasskeyVerification } from '../passkey'
import type { SystemStatus } from '../types'
import type {
  SecurityProof,
  VerificationMethod,
  VerificationMethods,
  VerificationOperation,
} from './types'

/**
 * Fetch available verification methods for the current user.
 */
export async function checkVerificationMethods(
  operation: VerificationOperation
): Promise<VerificationMethods> {
  const [requirements, passkeySupported] = await Promise.all([
    authResult<{
      methods: Array<{ method: string; available: boolean }>
      oauth_providers?: Array<{ slug: string; name: string }>
      password_encryption_enabled?: boolean
    }>(
      api.get('/api/verify/methods', {
        ...authRequestOptions,
        params: { scope: operation.scope },
        disableDuplicate: true,
      })
    ),
    detectPasskeySupport(),
  ])
  const has2FA = requirements.methods.some(
    (method) => method.method === '2fa' && method.available
  )
  const hasPasskey = requirements.methods.some(
    (method) => method.method === 'passkey' && method.available
  )
  const hasPassword = requirements.methods.some(
    (method) => method.method === 'password' && method.available
  )

  return {
    has2FA,
    hasPasskey,
    passkeySupported,
    hasPassword,
    passwordEncryptionEnabled:
      requirements.password_encryption_enabled === true,
    oauthProviders: requirements.oauth_providers ?? [],
  }
}

/**
 * Execute a verification flow based on the method type.
 */
export async function verify(
  method: VerificationMethod,
  operation: VerificationOperation,
  code?: string,
  signal?: AbortSignal,
  passwordEncryptionEnabled = false
): Promise<SecurityProof> {
  switch (method) {
    case '2fa':
      return verifyTwoFA(operation, code, signal)
    case 'passkey':
      return verifyPasskey(operation, signal)
    case 'password':
      return verifyPassword(operation, code, signal, passwordEncryptionEnabled)
    case 'oauth':
      return verifyOAuth(operation, code, signal)
    default:
      throw new Error(`Unsupported verification method: ${method}`)
  }
}

async function verifyOAuth(
  operation: VerificationOperation,
  provider: string | undefined,
  signal?: AbortSignal
): Promise<SecurityProof> {
  if (!provider) throw new Error('Select a linked OAuth account first')

  const exchange = await openOAuthPopup({
    provider,
    intent: 'verify',
    signal: signal ?? new AbortController().signal,
    prepare: async (popupSignal) => {
      const [authorization, status] = await Promise.all([
        createOAuthAuthorization(provider, 'verify', operation, popupSignal),
        authResult<SystemStatus>(
          api.get('/api/status', {
            ...authRequestOptions,
            signal: popupSignal,
            disableDuplicate: true,
          })
        ),
      ])
      return {
        state: authorization.state,
        url:
          authorization.authorizationUrl ??
          buildOAuthAuthorizationUrl(provider, authorization.state, status),
      }
    },
  })

  try {
    const proof = await authResult<SecurityProof>(
      api.get(`/api/oauth/${provider}`, {
        ...authRequestOptions,
        singleUseAuthorization: true,
        disableDuplicate: true,
        signal: exchange.signal,
        params: {
          state: exchange.callback.state,
          code: exchange.callback.code,
          error: exchange.callback.error,
          error_description: exchange.callback.errorDescription,
        },
      })
    )
    exchange.signal.throwIfAborted()
    exchange.finish({ success: true })
    return proof
  } catch (error) {
    const failure = AuthOperationError.from(
      exchange.signal.aborted ? exchange.signal.reason : error
    )
    exchange.finish({ success: false, message: failure.message })
    throw failure
  }
}

async function verifyPassword(
  operation: VerificationOperation,
  password: string | undefined,
  signal?: AbortSignal,
  passwordEncryptionEnabled = false
): Promise<SecurityProof> {
  const value = password?.trim()
  if (!value) throw new Error('Please enter your password')
  const passwordFields = passwordEncryptionEnabled
    ? await encryptPassword(value)
    : { password: value }
  return authResult<SecurityProof>(
    api.post(
      '/api/verify',
      {
        method: 'password',
        scope: operation.scope,
        context: operation.context,
        ...passwordFields,
      },
      { ...authRequestOptions, signal }
    )
  )
}

/**
 * Perform 2FA verification flow.
 */
async function verifyTwoFA(
  operation: VerificationOperation,
  code?: string | null,
  signal?: AbortSignal
): Promise<SecurityProof> {
  const trimmed = code?.trim()
  if (!trimmed) {
    throw new Error('Please enter the verification code or backup code')
  }

  return authResult<SecurityProof>(
    api.post(
      '/api/verify',
      {
        method: '2fa',
        scope: operation.scope,
        context: operation.context,
        code: trimmed,
      },
      { ...authRequestOptions, signal }
    )
  )
}

/**
 * Perform Passkey verification flow.
 */
async function verifyPasskey(
  operation: VerificationOperation,
  signal?: AbortSignal
): Promise<SecurityProof> {
  if (typeof navigator === 'undefined' || !navigator.credentials) {
    throw new Error('Passkey verification is not supported in this environment')
  }

  try {
    const beginResponse = await beginPasskeyVerification(operation, signal)
    const publicKey = prepareCredentialRequestOptions(beginResponse.options)

    const credential = (await navigator.credentials.get({
      publicKey,
    })) as PublicKeyCredential | null

    if (!credential) {
      throw new Error('Passkey verification was cancelled')
    }

    const assertion = buildAssertionResult(credential)
    if (!assertion) {
      throw new Error('Unable to build Passkey assertion')
    }

    const proof = await finishPasskeyVerification(
      beginResponse.flow_token,
      assertion,
      signal
    )
    if (
      proof.scope !== operation.scope ||
      proof.method !== 'passkey' ||
      proof.expires_at * 1000 <= Date.now()
    ) {
      throw new AuthOperationError('Verification proof was not returned')
    }
    return proof
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === 'NotAllowedError') {
      throw new Error('Passkey verification was cancelled or timed out', {
        cause: error,
      })
    }
    if (error instanceof DOMException && error.name === 'InvalidStateError') {
      throw new Error(
        'Passkey verification is not available in the current state',
        { cause: error }
      )
    }
    throw error
  }
}
