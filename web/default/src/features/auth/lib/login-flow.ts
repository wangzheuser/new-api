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
import type { AuthBundle, LoginChallenge } from '@/stores/auth-store'

export function isAuthBundle(value: unknown): value is AuthBundle {
  if (!value || typeof value !== 'object') return false
  const bundle = value as Partial<AuthBundle>
  const session = bundle.session
  return Boolean(
    typeof bundle.access_token === 'string' &&
      bundle.access_token.length > 0 &&
      typeof bundle.token_type === 'string' &&
      bundle.token_type.length > 0 &&
      typeof bundle.access_expires_at === 'number' &&
      Number.isFinite(bundle.access_expires_at) &&
      bundle.user &&
      typeof bundle.user === 'object' &&
      typeof bundle.user.id === 'number' &&
      Number.isFinite(bundle.user.id) &&
      session &&
      typeof session.sid === 'string' &&
      session.sid.length > 0 &&
      typeof session.expires_at === 'number' &&
      Number.isFinite(session.expires_at)
  )
}

export function isLoginChallenge(value: unknown): value is LoginChallenge {
  if (!value || typeof value !== 'object') return false
  const challenge = value as Partial<LoginChallenge>
  return Boolean(
    challenge.require_verification === true &&
      typeof challenge.flow_token === 'string' &&
      challenge.flow_token.length > 0 &&
      typeof challenge.expires_at === 'number' &&
      Number.isFinite(challenge.expires_at) &&
      Array.isArray(challenge.methods) &&
      challenge.methods.length > 0 &&
      challenge.methods.every(
        (method) =>
          method &&
          (method.method === '2fa' || method.method === 'passkey') &&
          typeof method.available === 'boolean'
      )
  )
}

export function safeLoginRedirect(path?: string): string | undefined {
  return path &&
    path.startsWith('/') &&
    !path.startsWith('//') &&
    !path.includes('\\')
    ? path
    : undefined
}
