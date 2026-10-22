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
export type VerificationMethod = '2fa' | 'passkey' | 'password' | 'oauth'

export interface VerificationOperation {
  scope: string
  context?: Record<string, unknown>
}

export interface SecurityProof {
  proof_token: string
  expires_at: number
  method: VerificationMethod
  scope: string
}

export interface VerificationMethods {
  has2FA: boolean
  hasPasskey: boolean
  passkeySupported: boolean
  hasPassword: boolean
  passwordEncryptionEnabled: boolean
  oauthProviders: Array<{ slug: string; name: string }>
}

export interface SecureVerificationState {
  method: VerificationMethod | null
  loading: boolean
  code: string
  oauthProvider?: string
  title?: string
  description?: string
  operation?: VerificationOperation
}

export interface UseSecureVerificationOptions {
  onSuccess?: (result: unknown, method: VerificationMethod) => void
  onError?: (error: unknown) => void
  successMessage?: string
  autoReset?: boolean
}

export interface StartVerificationOptions {
  preferredMethod?: VerificationMethod
  title?: string
  description?: string
  operation: VerificationOperation
}
