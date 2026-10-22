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
import {
  createFileRoute,
  useNavigate,
  useParams,
  useSearch,
} from '@tanstack/react-router'
import type { AxiosRequestConfig } from 'axios'
import i18next from 'i18next'
import { useEffect, useRef } from 'react'
import { toast } from 'sonner'

import { OAuthCallbackScreen } from '@/features/auth/components/oauth-callback-screen'
import {
  OAUTH_POPUP_CALLBACK_MESSAGE,
  OAUTH_POPUP_RESULT_MESSAGE,
} from '@/features/auth/constants'
import { useAuthRedirect } from '@/features/auth/hooks/use-auth-redirect'
import {
  getOAuthSessionStorage,
  resolveOAuthCallbackMode,
} from '@/features/auth/lib/oauth-callback-mode'
import { api } from '@/lib/api'
import { AuthOperationError } from '@/lib/secure-verification'

type OAuthRequestConfig = AxiosRequestConfig & {
  skipBusinessError?: boolean
  skipAuthRefresh?: boolean
}

function OAuthCallback() {
  const navigate = useNavigate()
  const { handleLoginResult } = useAuthRedirect()
  const handled = useRef(false)
  const { provider } = useParams({ from: '/oauth/$provider' }) as {
    provider: string
  }
  const search = useSearch({ from: '/oauth/$provider' }) as {
    code?: string
    state?: string
    error?: string
    error_description?: string
    redirect?: string
  }
  const state = search.state ?? ''
  const mode =
    typeof window === 'undefined'
      ? 'login'
      : resolveOAuthCallbackMode(provider, state, {
          opener: window.opener,
          storage: getOAuthSessionStorage(window),
        })

  useEffect(() => {
    if (typeof window === 'undefined' || handled.current) return
    handled.current = true
    const code = search.code ?? ''
    if (mode === 'bind' || mode === 'verify') {
      if (!window.opener || window.opener.closed) {
        toast.error(i18next.t('OAuth window is no longer available.'))
        return
      }
      const handleResult = (event: MessageEvent<unknown>) => {
        if (
          event.origin !== window.location.origin ||
          event.source !== window.opener
        ) {
          return
        }
        const result = event.data as {
          type?: string
          intent?: string
          provider?: string
          state?: string
          success?: boolean
          message?: string
        } | null
        if (
          result?.type !== OAUTH_POPUP_RESULT_MESSAGE ||
          result.intent !== mode ||
          result.provider !== provider ||
          result.state !== state
        ) {
          return
        }
        window.removeEventListener('message', handleResult)
        window.clearTimeout(closeTimer)
        if (!result.success) {
          toast.error(result.message || i18next.t('OAuth failed'))
          window.setTimeout(() => window.close(), 1500)
          return
        }
        toast.success(i18next.t('Binding successful!'))
        window.close()
      }
      window.addEventListener('message', handleResult)
      window.opener.postMessage(
        {
          type: OAUTH_POPUP_CALLBACK_MESSAGE,
          intent: mode,
          provider,
          code,
          state,
          error: search.error,
          errorDescription: search.error_description,
        },
        window.location.origin
      )
      const closeTimer = window.setTimeout(() => window.close(), 30_000)
      return () => {
        window.removeEventListener('message', handleResult)
        window.clearTimeout(closeTimer)
      }
    }

    if (!code && !search.error) {
      toast.error(i18next.t('Missing code'))
      void navigate({ to: '/sign-in', replace: true })
      return
    }
    void (async () => {
      try {
        const response = await api.get(`/api/oauth/${provider}`, {
          params: {
            code: code || undefined,
            state,
            error: search.error,
            error_description: search.error_description,
          },
          skipBusinessError: true,
          skipAuthRefresh: true,
        } as OAuthRequestConfig)
        if (response.data?.success) {
          await handleLoginResult(response.data.data, search.redirect)
          return
        }
        throw new AuthOperationError(
          response.data?.message || i18next.t('OAuth failed'),
          response.data?.code
        )
      } catch (error) {
        toast.error(
          error instanceof Error ? error.message : i18next.t('OAuth failed')
        )
        void navigate({ to: '/sign-in', replace: true })
      }
    })()
  }, [
    handleLoginResult,
    mode,
    navigate,
    provider,
    search.code,
    search.error,
    search.error_description,
    search.redirect,
    state,
  ])

  return (
    <OAuthCallbackScreen
      provider={provider}
      mode={mode === 'verify' ? 'bind' : mode}
    />
  )
}

export const Route = createFileRoute('/oauth/$provider')({
  component: OAuthCallback,
})
