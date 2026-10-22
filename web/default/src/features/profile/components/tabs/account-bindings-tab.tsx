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
import { Mail, Shield, Link2, Unlink } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { SiGithub, SiWechat, SiLinux } from 'react-icons/si'
import { toast } from 'sonner'

import { IconDiscord } from '@/assets/brand-icons'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { createOAuthAuthorization } from '@/features/auth/api'
import {
  OAUTH_POPUP_CALLBACK_MESSAGE,
  OAUTH_POPUP_RESULT_MESSAGE,
} from '@/features/auth/constants'
import {
  getOAuthSessionStorage,
  markOAuthPopup,
} from '@/features/auth/lib/oauth-callback-mode'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { useDialogs } from '@/hooks/use-dialog'
import { useStatus } from '@/hooks/use-status'
import { api } from '@/lib/api'
import { buildOAuthAuthorizationUrl } from '@/lib/oauth'

import {
  getSelfOAuthBindings,
  unbindCustomOAuth,
  type CustomOAuthBinding,
} from '../../api'
import type { UserProfile, BindingItem } from '../../types'
import { EmailBindDialog } from '../dialogs/email-bind-dialog'
import { WeChatBindDialog } from '../dialogs/wechat-bind-dialog'

interface AccountBindingsTabProps {
  profile: UserProfile | null
  onUpdate: () => void
}

type DialogKey = 'email' | 'wechat'
type PendingAction =
  | { kind: 'oauth'; provider: string; popup: Window; state?: string }
  | { kind: 'unbind'; target: CustomOAuthBinding }
  | null

export function AccountBindingsTab(props: AccountBindingsTabProps) {
  const { t } = useTranslation()
  const dialogs = useDialogs<DialogKey>()
  const { status, loading } = useStatus()
  const [customBindings, setCustomBindings] = useState<CustomOAuthBinding[]>([])
  const [unbindTarget, setUnbindTarget] = useState<CustomOAuthBinding | null>(
    null
  )
  const pending = useRef<PendingAction>(null)
  const customProviders = status?.custom_oauth_providers as
    | Array<{ id: number; name: string; slug: string }>
    | undefined

  const fetchCustomBindings = useCallback(async () => {
    if (!customProviders?.length) return
    try {
      const response = await getSelfOAuthBindings()
      if (response.success && response.data) setCustomBindings(response.data)
    } catch {
      // Keep the profile usable when optional custom binding data is unavailable.
    }
  }, [customProviders])

  const security = useSecureVerification({
    onSuccess: async (result) => {
      const action = pending.current
      if (!action) return
      if (action.kind === 'unbind') {
        pending.current = null
        setUnbindTarget(null)
        toast.success(
          t('Unbound {{provider}}', { provider: action.target.provider_name })
        )
        await fetchCustomBindings()
        props.onUpdate()
        return
      }
      const authorization = result as { state?: string; url?: string }
      if (!authorization.state || !authorization.url) {
        action.popup.close()
        pending.current = null
        toast.error(t('Failed to initialize OAuth'))
        return
      }
      if (
        !markOAuthPopup(
          getOAuthSessionStorage(action.popup),
          action.provider,
          authorization.state,
          'bind'
        )
      ) {
        action.popup.close()
        pending.current = null
        toast.error(t('OAuth popup storage is unavailable.'))
        return
      }
      pending.current = { ...action, state: authorization.state }
      action.popup.location.replace(authorization.url)
    },
  })

  useEffect(() => {
    void fetchCustomBindings()
  }, [fetchCustomBindings])

  useEffect(() => {
    const handleMessage = (event: MessageEvent<unknown>) => {
      if (event.origin !== window.location.origin) return
      const result = event.data as {
        type?: string
        intent?: string
        provider?: string
        state?: string
        code?: string
        error?: string
        errorDescription?: string
        success?: boolean
        message?: string
      } | null
      const action = pending.current
      if (
        !action ||
        action.kind !== 'oauth' ||
        (result?.type !== OAUTH_POPUP_CALLBACK_MESSAGE &&
          result?.type !== OAUTH_POPUP_RESULT_MESSAGE) ||
        result.intent !== 'bind' ||
        result.provider !== action.provider ||
        (action.state && result.state !== action.state) ||
        event.source !== action.popup
      ) {
        return
      }
      if (result.type === OAUTH_POPUP_CALLBACK_MESSAGE) {
        pending.current = { ...action, state: result.state }
        void api
          .get(`/api/oauth/${action.provider}`, {
            params: {
              state: result.state,
              code: result.code,
              error: result.error,
              error_description: result.errorDescription,
            },
            skipBusinessError: true,
            skipErrorHandler: true,
          })
          .then((response) => {
            const success = Boolean(response.data?.success)
            action.popup.postMessage(
              {
                type: OAUTH_POPUP_RESULT_MESSAGE,
                intent: 'bind',
                provider: action.provider,
                state: result.state,
                success,
                message: response.data?.message,
              },
              window.location.origin
            )
            if (success) {
              toast.success(t('Binding successful!'))
              props.onUpdate()
            } else {
              toast.error(response.data?.message || t('OAuth failed'))
            }
            action.popup.close()
            pending.current = null
          })
          .catch((error) => {
            toast.error(
              error instanceof Error ? error.message : t('OAuth failed')
            )
            action.popup.close()
            pending.current = null
          })
        return
      }
      action.popup.close()
      pending.current = null
    }
    window.addEventListener('message', handleMessage)
    return () => window.removeEventListener('message', handleMessage)
  }, [props, t])

  const startOAuthBinding = async (provider: string) => {
    const popup = window.open('', '_blank')
    if (!popup) {
      toast.error(t('OAuth pop-up was blocked'))
      return
    }
    pending.current = { kind: 'oauth', provider, popup }
    const started = await security.startVerification(
      (proofToken) =>
        createOAuthAuthorization(
          provider,
          'bind',
          { scope: 'account.binding.bind', context: { provider } },
          undefined,
          proofToken
        ).then((authorization) => ({
          state: authorization.state,
          url:
            authorization.authorizationUrl ??
            buildOAuthAuthorizationUrl(
              provider,
              authorization.state,
              status ?? {}
            ),
        })),
      {
        operation: { scope: 'account.binding.bind', context: { provider } },
        title: t('Security verification'),
        description: t('Confirm your identity before binding this account.'),
      }
    )
    if (!started) {
      popup.close()
      pending.current = null
    }
  }

  const handleUnbindCustom = async () => {
    const target = unbindTarget
    if (!target) return
    pending.current = { kind: 'unbind', target }
    const started = await security.startVerification(
      (proofToken) => unbindCustomOAuth(target.provider_id, proofToken),
      {
        operation: {
          scope: 'account.binding.unbind',
          context: { provider_id: Number(target.provider_id) },
        },
        title: t('Security verification'),
        description: t('Confirm your identity before unbinding this account.'),
      }
    )
    if (!started) pending.current = null
  }

  const bindings: BindingItem[] = useMemo(() => {
    if (!props.profile || !status) return []
    const user = props.profile as unknown as Record<string, unknown>
    const oauth = (provider: string, value: string | undefined) => {
      let label = provider[0].toUpperCase() + provider.slice(1)
      if (provider === 'linuxdo') label = 'LinuxDO'
      if (provider === 'oidc') label = 'OIDC'
      let icon: React.ElementType = Shield
      if (provider === 'github') icon = SiGithub
      if (provider === 'discord') icon = IconDiscord
      if (provider === 'linuxdo') {
        icon = SiLinux as React.ComponentType<{ className?: string }>
      }
      const enabledValue = (status as Record<string, unknown>)[
        `${provider}_oauth`
      ]
      const enabled = provider === 'oidc' ? status.oidc_enabled : enabledValue
      return {
        id: provider,
        label: t(label),
        icon,
        value,
        isBound: Boolean(value),
        isEnabled: Boolean(enabled),
        onBind: () => void startOAuthBinding(provider),
      }
    }
    return [
      {
        id: 'email',
        label: t('Email'),
        icon: Mail,
        value: props.profile.email,
        isBound: Boolean(props.profile.email),
        isEnabled: true,
        onBind: () => dialogs.open('email'),
      },
      {
        id: 'wechat',
        label: t('WeChat'),
        icon: SiWechat as React.ComponentType<{ className?: string }>,
        value: user.wechat_id as string | undefined,
        isBound: Boolean(user.wechat_id),
        isEnabled: Boolean(status.wechat_login),
        onBind: () => dialogs.open('wechat'),
      },
      oauth('github', user.github_id as string | undefined),
      oauth('discord', user.discord_id as string | undefined),
      oauth('oidc', user.oidc_id as string | undefined),
      oauth('telegram', user.telegram_id as string | undefined),
      oauth('linuxdo', user.linux_do_id as string | undefined),
    ].filter((binding) => binding.isEnabled)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.profile, status, t])

  if (!props.profile || loading) return null

  return (
    <>
      <div className='grid grid-cols-1 gap-2.5 sm:grid-cols-2 sm:gap-3'>
        {bindings.map((binding) => {
          let actionLabel = t('Bind')
          if (binding.isBound) {
            actionLabel = binding.id === 'email' ? t('Change') : t('Bound')
          }
          return (
            <div
              key={binding.id}
              className='flex items-center justify-between gap-2.5 rounded-lg border p-2.5 sm:gap-3 sm:p-3'
            >
              <div className='flex min-w-0 items-center gap-2.5 sm:gap-3'>
                <div className='bg-muted shrink-0 rounded-md p-1.5 sm:p-2'>
                  <binding.icon className='h-4 w-4' />
                </div>
                <div className='min-w-0'>
                  <div className='flex items-center gap-1.5'>
                    <p className='text-sm font-medium'>{binding.label}</p>
                    {binding.isBound && (
                      <StatusBadge
                        label={t('Bound')}
                        variant='success'
                        copyable={false}
                      />
                    )}
                  </div>
                  <p className='text-muted-foreground truncate text-xs'>
                    {binding.value || t('Not bound')}
                  </p>
                </div>
              </div>
              <Button
                variant='outline'
                size='sm'
                className='h-7 shrink-0 px-2.5 text-xs'
                onClick={binding.onBind}
                disabled={
                  security.isLoading ||
                  (binding.isBound && binding.id !== 'email')
                }
              >
                {actionLabel}
              </Button>
            </div>
          )
        })}
      </div>
      {customProviders?.length ? (
        <>
          <Separator className='my-4' />
          <p className='text-muted-foreground mb-3 text-sm font-medium'>
            {t('Custom OAuth')}
          </p>
          <div className='grid grid-cols-1 gap-2.5 sm:grid-cols-2 sm:gap-3'>
            {customProviders.map((provider) => {
              const binding = customBindings.find(
                (item) => item.provider_id === provider.id
              )
              return (
                <div
                  key={provider.id}
                  className='flex items-center justify-between gap-2.5 rounded-lg border p-2.5 sm:gap-3 sm:p-3'
                >
                  <div className='flex min-w-0 items-center gap-2.5'>
                    <div className='bg-muted rounded-md p-1.5'>
                      <Link2 className='h-4 w-4' />
                    </div>
                    <div className='min-w-0'>
                      <p className='text-sm font-medium'>{provider.name}</p>
                      <p className='text-muted-foreground truncate text-xs'>
                        {binding?.external_id || t('Not bound')}
                      </p>
                    </div>
                  </div>
                  {binding ? (
                    <Button
                      variant='ghost'
                      size='sm'
                      className='text-destructive h-7 px-2.5 text-xs'
                      onClick={() => setUnbindTarget(binding)}
                      disabled={security.isLoading}
                    >
                      <Unlink className='mr-1 h-3 w-3' />
                      {t('Unbind')}
                    </Button>
                  ) : (
                    <Button
                      variant='outline'
                      size='sm'
                      className='h-7 px-2.5 text-xs'
                      onClick={() => void startOAuthBinding(provider.slug)}
                      disabled={security.isLoading}
                    >
                      {t('Bind')}
                    </Button>
                  )}
                </div>
              )
            })}
          </div>
        </>
      ) : null}
      <ConfirmDialog
        open={Boolean(unbindTarget)}
        onOpenChange={(open) => !open && setUnbindTarget(null)}
        title={t('Confirm Unbind')}
        desc={t('Are you sure you want to unbind {{provider}}?', {
          provider: unbindTarget?.provider_name || '',
        })}
        confirmText={t('Confirm Unbind')}
        destructive
        handleConfirm={handleUnbindCustom}
        isLoading={security.isLoading}
      />
      <EmailBindDialog
        open={dialogs.isOpen('email')}
        onOpenChange={(open) =>
          open ? dialogs.open('email') : dialogs.close('email')
        }
        currentEmail={props.profile.email}
        onSuccess={props.onUpdate}
      />
      <WeChatBindDialog
        open={dialogs.isOpen('wechat')}
        onOpenChange={(open) =>
          open ? dialogs.open('wechat') : dialogs.close('wechat')
        }
        qrCodeUrl={String(
          (status as Record<string, unknown>).wechat_qrcode ||
            (status as Record<string, unknown>).wechat_qrcode_image_url ||
            ''
        )}
        onSuccess={props.onUpdate}
      />
      <SecureVerificationDialog
        open={security.open}
        onOpenChange={(open) => !open && security.cancel()}
        methods={security.methods}
        state={security.state}
        onVerify={(method, code) => {
          void security.executeVerification(method, code)
        }}
        onCancel={security.cancel}
        onCodeChange={security.setCode}
        onMethodChange={security.switchMethod}
        onOAuthProviderChange={security.setOAuthProvider}
      />
    </>
  )
}
