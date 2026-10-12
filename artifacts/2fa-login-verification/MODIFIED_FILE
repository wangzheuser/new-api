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
import { useNavigate } from '@tanstack/react-router'
import i18n from 'i18next'

import type { User } from '@/features/users/types'
import { applyAuthBundle, getSelf } from '@/lib/api'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import {
  isAuthBundle,
  isLoginChallenge,
  safeLoginRedirect,
} from '../lib/login-flow'
import { saveUserId } from '../lib/storage'

function getSavedLanguage(user: User): string | undefined {
  const userData = user as Record<string, unknown>
  if (typeof userData.language === 'string') {
    return userData.language
  }

  if (typeof userData.setting !== 'string') {
    return undefined
  }

  try {
    const setting = JSON.parse(userData.setting) as { language?: unknown }
    return typeof setting.language === 'string' ? setting.language : undefined
  } catch {
    return undefined
  }
}

/**
 * Hook for handling authentication redirects and user data management
 */
export function useAuthRedirect() {
  const navigate = useNavigate()
  const { auth } = useAuthStore()

  /**
   * Handle successful login
   * @param userData - Optional user data from login response
   * @param redirectTo - Redirect path after login
   */
  const handleLoginSuccess = async (
    userData?: AuthBundle | { id?: number } | null,
    redirectTo?: string
  ) => {
    if (isAuthBundle(userData)) {
      applyAuthBundle(userData)
      const savedLang = getSavedLanguage(userData.user as User)
      if (savedLang && savedLang !== i18n.language) {
        await i18n.changeLanguage(savedLang)
      }
      const targetPath = safeLoginRedirect(redirectTo) || '/dashboard'
      await navigate({ href: targetPath, replace: true })
      return
    }

    // Save user ID if available
    if (userData?.id) {
      saveUserId(userData.id)
    }

    // Fetch and set user data
    try {
      const self = await getSelf()
      if (self?.success && self.data) {
        const user = self.data as User
        auth.setUser(user)

        // Update user ID if not already set
        if (user.id) {
          saveUserId(user.id)
        }

        // Restore saved language preference
        const savedLang = getSavedLanguage(user)
        if (savedLang && savedLang !== i18n.language) {
          i18n.changeLanguage(savedLang)
        }
      }
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('Failed to fetch user data:', error)
    }

    // Navigate to target page
    const targetPath = safeLoginRedirect(redirectTo) || '/dashboard'
    await navigate({ href: targetPath, replace: true })
  }

  /** Route the primary login response without treating a pending challenge as a session. */
  const handleLoginResult = async (
    result: unknown,
    redirectTo?: string
  ): Promise<boolean> => {
    if (isAuthBundle(result)) {
      await handleLoginSuccess(result, redirectTo)
      return true
    }
    if (!isLoginChallenge(result)) {
      throw new Error('Login failed')
    }
    if (result.expires_at * 1000 <= Date.now()) {
      throw new Error('Login flow expired. Please sign in again.')
    }
    auth.setPendingLoginVerification({
      challenge: result,
      redirectTo: safeLoginRedirect(redirectTo),
    })
    await navigate({ to: '/otp', replace: true })
    return false
  }

  /**
   * Redirect to 2FA page
   */
  const redirectTo2FA = () => {
    navigate({ to: '/otp', replace: true })
  }

  /**
   * Redirect to login page
   */
  const redirectToLogin = () => {
    auth.setPendingLoginVerification(null)
    void navigate({ to: '/sign-in', replace: true })
  }

  /**
   * Redirect to register page
   */
  const redirectToRegister = () => {
    navigate({ to: '/sign-up', replace: true })
  }

  return {
    handleLoginSuccess,
    handleLoginResult,
    redirectTo2FA,
    redirectToLogin,
    redirectToRegister,
  }
}
