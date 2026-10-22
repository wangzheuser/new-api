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
import type { AxiosRequestConfig } from 'axios'
import { useState, useRef, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { createOAuthAuthorization } from '../api'
import {
  buildGitHubOAuthUrl,
  buildDiscordOAuthUrl,
  buildOIDCOAuthUrl,
  buildLinuxDOOAuthUrl,
} from '../lib/oauth'
import type { SystemStatus, CustomOAuthProviderInfo } from '../types'

type LogoutRequestConfig = AxiosRequestConfig & {
  skipErrorHandler?: boolean
}

/**
 * Hook for managing OAuth login
 */
export function useOAuthLogin(status: SystemStatus | null) {
  const { t } = useTranslation()
  const [isLoading, setIsLoading] = useState(false)
  const [githubButtonText, setGithubButtonText] = useState('')
  const [githubButtonDisabled, setGithubButtonDisabled] = useState(false)
  const githubTimeoutRef = useRef<NodeJS.Timeout | null>(null)
  const { auth } = useAuthStore()

  useEffect(() => {
    setGithubButtonText(t('Continue with GitHub'))

    return () => {
      if (githubTimeoutRef.current) {
        clearTimeout(githubTimeoutRef.current)
      }
    }
  }, [t])

  const resetSession = async () => {
    try {
      auth.reset()
    } catch {
      // ignore store reset errors
    }
    try {
      await api.post('/api/user/auth/logout', undefined, {
        skipErrorHandler: true,
        skipAuthRefresh: true,
      } as LogoutRequestConfig)
    } catch {
      // ignore logout errors
    }
  }

  const handleGitHubLogin = async () => {
    if (!status?.github_client_id) return
    if (githubButtonDisabled) return

    setIsLoading(true)
    setGithubButtonDisabled(true)
    setGithubButtonText(t('Redirecting to GitHub...'))

    if (githubTimeoutRef.current) {
      clearTimeout(githubTimeoutRef.current)
    }

    githubTimeoutRef.current = setTimeout(() => {
      setIsLoading(false)
      setGithubButtonText(
        t('Request timed out, please refresh and restart GitHub login')
      )
      setGithubButtonDisabled(true)
    }, 20000)

    try {
      await resetSession()
      const authorization = await createOAuthAuthorization('github', 'login')
      const url =
        authorization.authorizationUrl ??
        buildGitHubOAuthUrl(status.github_client_id, authorization.state)
      window.open(url, '_self')
    } catch {
      toast.error(t('Failed to start GitHub login'))
      if (githubTimeoutRef.current) {
        clearTimeout(githubTimeoutRef.current)
      }
      setIsLoading(false)
      setGithubButtonText(t('Continue with GitHub'))
      setGithubButtonDisabled(false)
    }
  }

  const handleDiscordLogin = async () => {
    if (!status?.discord_client_id) return

    setIsLoading(true)
    try {
      await resetSession()
      const authorization = await createOAuthAuthorization('discord', 'login')
      const url =
        authorization.authorizationUrl ??
        buildDiscordOAuthUrl(status.discord_client_id, authorization.state)
      window.open(url, '_self')
    } catch {
      toast.error(t('Failed to start Discord login'))
    } finally {
      setIsLoading(false)
    }
  }

  const handleOIDCLogin = async () => {
    if (!status?.oidc_authorization_endpoint || !status?.oidc_client_id) return

    setIsLoading(true)
    try {
      await resetSession()
      const authorization = await createOAuthAuthorization('oidc', 'login')
      const url =
        authorization.authorizationUrl ??
        buildOIDCOAuthUrl(
          status.oidc_authorization_endpoint,
          status.oidc_client_id,
          authorization.state
        )
      window.open(url, '_self')
    } catch {
      toast.error(t('Failed to start OIDC login'))
    } finally {
      setIsLoading(false)
    }
  }

  const handleLinuxDOLogin = async () => {
    if (!status?.linuxdo_client_id) return

    setIsLoading(true)
    try {
      await resetSession()
      const authorization = await createOAuthAuthorization('linuxdo', 'login')
      const url =
        authorization.authorizationUrl ??
        buildLinuxDOOAuthUrl(status.linuxdo_client_id, authorization.state)
      window.open(url, '_self')
    } catch {
      toast.error(t('Failed to start LinuxDO login'))
    } finally {
      setIsLoading(false)
    }
  }

  const handleTelegramLogin = async () => {
    setIsLoading(true)
    try {
      const authorization = await createOAuthAuthorization('telegram', 'login')
      if (!authorization.authorizationUrl) {
        throw new Error(t('Failed to initialize OAuth'))
      }
      window.open(authorization.authorizationUrl, '_self')
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to start Telegram login')
      )
    } finally {
      setIsLoading(false)
    }
  }

  const handleCustomOAuthLogin = async (provider: CustomOAuthProviderInfo) => {
    if (!provider.authorization_endpoint || !provider.client_id) return

    setIsLoading(true)
    try {
      await resetSession()
      const authorization = await createOAuthAuthorization(
        provider.slug,
        'login'
      )

      const redirectUri = `${window.location.origin}/oauth/${provider.slug}`
      const url = new URL(provider.authorization_endpoint)
      url.searchParams.set('client_id', provider.client_id)
      url.searchParams.set('redirect_uri', redirectUri)
      url.searchParams.set('response_type', 'code')
      url.searchParams.set('state', authorization.state)
      if (provider.scopes) {
        url.searchParams.set('scope', provider.scopes)
      }

      window.open(authorization.authorizationUrl ?? url.toString(), '_self')
    } catch {
      toast.error(
        t('Failed to start {{provider}} login', { provider: provider.name })
      )
    } finally {
      setIsLoading(false)
    }
  }

  return {
    isLoading,
    githubButtonText,
    githubButtonDisabled,
    handleGitHubLogin,
    handleDiscordLogin,
    handleOIDCLogin,
    handleLinuxDOLogin,
    handleTelegramLogin,
    handleCustomOAuthLogin,
  }
}
