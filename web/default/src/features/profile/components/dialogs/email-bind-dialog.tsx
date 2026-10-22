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
import { Loader2 } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { useCountdown } from '@/hooks/use-countdown'

import { bindEmail, resendEmailBinding, startEmailBinding } from '../../api'
import type { EmailBindingFlow } from '../../types'

interface EmailBindDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentEmail?: string
  onSuccess: () => void
}

export function EmailBindDialog(props: EmailBindDialogProps) {
  const { t } = useTranslation()
  const security = useSecureVerification({
    onSuccess: (result) => {
      if (result && typeof result === 'object' && 'flow_token' in result) {
        applyFlow(result as EmailBindingFlow)
      }
    },
  })
  const [flow, setFlow] = useState<EmailBindingFlow | null>(null)
  const [email, setEmail] = useState('')
  const [newCode, setNewCode] = useState('')
  const [oldCode, setOldCode] = useState('')
  const resend = useCountdown({ initialSeconds: 60 })
  const deadline = useCountdown({ initialSeconds: 600 })
  const cancelSecurity = security.cancel
  const resetResend = resend.reset
  const resetDeadline = deadline.reset

  const reset = useCallback(() => {
    cancelSecurity()
    setFlow(null)
    setEmail('')
    setNewCode('')
    setOldCode('')
    resetResend()
    resetDeadline()
  }, [cancelSecurity, resetDeadline, resetResend])
  useEffect(() => {
    if (!props.open) reset()
  }, [props.open, reset])

  const applyFlow = (next: EmailBindingFlow) => {
    setFlow(next)
    setEmail(next.email)
    setNewCode('')
    setOldCode('')
    const now = Date.now() / 1000
    resend.start(Math.max(1, Math.ceil(next.resend_at - now)))
    deadline.start(Math.max(1, Math.ceil(next.expires_at - now)))
  }

  const submit = async () => {
    const normalizedEmail = email.trim().toLowerCase()
    if (!flow && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(normalizedEmail)) {
      toast.error(t('Please enter a valid email address'))
      return
    }
    if (!flow) {
      await security.startVerification(
        (proofToken) => startEmailBinding(normalizedEmail, proofToken),
        {
          operation: {
            scope: 'account.binding.bind',
            context: { provider: 'email', email: normalizedEmail },
          },
          title: t('Security verification'),
          description: t('Confirm your identity before changing your email.'),
        }
      )
      return
    }
    if (!deadline.isActive) return
    if (!/^\d{6}$/.test(newCode.trim())) {
      toast.error(t('Enter the 6-digit email verification code.'))
      return
    }
    if (flow.old_email_required && !/^\d{6}$/.test(oldCode.trim())) {
      toast.error(t('Enter the current email verification code.'))
      return
    }
    const result = await security.withVerification(
      () => bindEmail(flow.flow_token, newCode.trim(), oldCode.trim()),
      {
        operation: {
          scope: 'account.binding.bind',
          context: { provider: 'email', email: flow.email },
        },
      }
    )
    if (!result) return
    toast.success(t('Email bound successfully!'))
    props.onOpenChange(false)
    props.onSuccess()
  }

  const resendCodes = async () => {
    if (!flow || resend.isActive || !deadline.isActive) return
    try {
      applyFlow(await resendEmailBinding(flow.flow_token))
      toast.success(t('Verification code sent! Please check your email.'))
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Failed to resend codes')
      )
    }
  }

  const expired = Boolean(flow && !deadline.isActive)
  return (
    <>
      <Dialog
        open={props.open && !security.open}
        onOpenChange={(open) => {
          if (!open) reset()
          props.onOpenChange(open)
        }}
        title={t('Bind Email')}
        description={
          props.currentEmail
            ? t('Current email: {{email}}. Enter a new email to change.', {
                email: props.currentEmail,
              })
            : t('Bind an email address to your account.')
        }
        contentClassName='sm:max-w-md'
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => props.onOpenChange(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              onClick={() => void submit()}
              disabled={security.isLoading || expired}
            >
              {security.isLoading && (
                <Loader2 className='size-4 animate-spin' />
              )}
              {flow ? t('Confirm email') : t('Continue')}
            </Button>
          </>
        }
      >
        <div className='space-y-4'>
          <div className='space-y-2'>
            <Label htmlFor='bind-email'>{t('Email Address')}</Label>
            <Input
              id='bind-email'
              type='email'
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              disabled={Boolean(flow) || security.isLoading}
            />
          </div>
          {flow && (
            <>
              <p className='text-muted-foreground text-sm'>
                {t(
                  flow.old_email_required
                    ? 'Confirm both your current and new email addresses to finish this change.'
                    : 'Confirm the verification code sent to your new email address.'
                )}
              </p>
              <div className='space-y-2'>
                <Label htmlFor='new-email-code'>
                  {t('New email verification code')}
                </Label>
                <Input
                  id='new-email-code'
                  inputMode='numeric'
                  maxLength={6}
                  value={newCode}
                  onChange={(event) => setNewCode(event.target.value)}
                  disabled={expired || security.isLoading}
                />
              </div>
              {flow.old_email_required && (
                <div className='space-y-2'>
                  <Label htmlFor='old-email-code'>
                    {t('Current email verification code')}
                  </Label>
                  <Input
                    id='old-email-code'
                    inputMode='numeric'
                    maxLength={6}
                    value={oldCode}
                    onChange={(event) => setOldCode(event.target.value)}
                    disabled={expired || security.isLoading}
                  />
                </div>
              )}
              <Button
                type='button'
                variant='outline'
                onClick={() => void resendCodes()}
                disabled={resend.isActive || expired || security.isLoading}
              >
                {resend.isActive
                  ? t('Resend in {{seconds}}s', { seconds: resend.secondsLeft })
                  : t('Resend codes')}
              </Button>
              {expired && (
                <p className='text-destructive text-sm'>
                  {t('Email verification has ended. Start again to continue.')}
                </p>
              )}
            </>
          )}
        </div>
      </Dialog>
      <SecureVerificationDialog
        open={security.open}
        onOpenChange={(open) => {
          if (!open) security.cancel()
        }}
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
