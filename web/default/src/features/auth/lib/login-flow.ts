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
  return Boolean(
    typeof bundle.access_token === 'string' &&
    bundle.access_token.length > 0 &&
    bundle.user &&
    typeof bundle.user.id === 'number' &&
    bundle.session &&
    typeof bundle.session.sid === 'string'
  )
}

export function isLoginChallenge(value: unknown): value is LoginChallenge {
  if (!value || typeof value !== 'object') return false
  const challenge = value as Partial<LoginChallenge>
  return Boolean(
    challenge.require_verification === true &&
    typeof challenge.flow_token === 'string' &&
    challenge.flow_token.length > 0 &&
    typeof challenge.expires_at === 'number'
  )
}

export function safeLoginRedirect(path?: string): string | undefined {
  return path && path.startsWith('/') && !path.startsWith('//')
    ? path
    : undefined
}
