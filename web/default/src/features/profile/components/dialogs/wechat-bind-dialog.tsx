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
import { QrCode } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'

import { bindWeChat } from '../../api'

interface WeChatBindDialogProps {
  open: boolean
  qrCodeUrl?: string
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}

export function WeChatBindDialog(props: WeChatBindDialogProps) {
  const { t } = useTranslation()
  const security = useSecureVerification({
    onSuccess: () => {
      toast.success(t('Binding successful!'))
      props.onOpenChange(false)
      props.onSuccess()
    },
  })
  const submit = async () => {
    const code =
      document
        .querySelector<HTMLInputElement>('#wechat-bind-code')
        ?.value.trim() ?? ''
    if (!code) {
      toast.error(t('Enter the verification code'))
      return
    }
    await security.startVerification(
      (proofToken) => bindWeChat(code, proofToken),
      {
        operation: {
          scope: 'account.binding.bind',
          context: { provider: 'wechat', code },
        },
        title: t('Security verification'),
        description: t('Confirm your identity before binding WeChat.'),
      }
    )
  }
  return (
    <>
      <Dialog
        open={props.open && !security.open}
        onOpenChange={(open) => {
          if (!open) security.cancel()
          props.onOpenChange(open)
        }}
        title={t('Bind WeChat Account')}
        description={t('Scan the QR code with WeChat to bind your account')}
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
              disabled={security.isLoading}
            >
              {t('Bind')}
            </Button>
          </>
        }
      >
        <div className='space-y-4'>
          <Alert>
            <QrCode className='h-4 w-4' />
            <AlertDescription>
              {t(
                'Use WeChat to scan the QR code, then enter the returned verification code.'
              )}
            </AlertDescription>
          </Alert>
          {props.qrCodeUrl ? (
            <img
              src={props.qrCodeUrl}
              alt={t('WeChat login QR code')}
              className='mx-auto size-48 rounded-lg border object-contain'
            />
          ) : (
            <p className='text-muted-foreground text-sm'>
              {t('QR code is not configured. Please contact support.')}
            </p>
          )}
          <div className='space-y-2'>
            <Label htmlFor='wechat-bind-code'>{t('Verification code')}</Label>
            <Input
              id='wechat-bind-code'
              autoComplete='one-time-code'
              placeholder={t('Enter the verification code')}
            />
          </div>
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
