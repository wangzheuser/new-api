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
import { AlertTriangle, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { disable2FA } from '@/lib/api'

// ============================================================================
// Two-FA Disable Dialog Component
// ============================================================================

interface TwoFADisableDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}

export function TwoFADisableDialog({
  open,
  onOpenChange,
  onSuccess,
}: TwoFADisableDialogProps) {
  const { t } = useTranslation()
  const [loading] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  const security = useSecureVerification({
    onSuccess: () => {
      toast.success(t('Two-factor authentication disabled'))
      onOpenChange(false)
      onSuccess()
      setConfirmed(false)
    },
  })

  const handleDisable = async () => {
    if (!confirmed) {
      toast.error(t('Please confirm that you understand the consequences'))
      return
    }

    await security.startVerification(
      (proofToken) => disable2FA(proofToken ?? ''),
      {
        operation: { scope: '2fa.disable' },
        preferredMethod: '2fa',
        title: t('Security verification'),
        description: t(
          'Confirm your identity before disabling two-factor authentication.'
        ),
      }
    )
  }

  const handleOpenChange = (open: boolean) => {
    if (!loading) {
      if (!open) {
        setConfirmed(false)
      }
      onOpenChange(open)
    }
  }

  return (
    <Dialog
      open={open && !security.open}
      onOpenChange={handleOpenChange}
      title={
        <>
          <AlertTriangle className='h-5 w-5' />
          {t('Disable Two-Factor Authentication')}
        </>
      }
      description={t(
        'This action will permanently remove 2FA protection from your account.'
      )}
      contentClassName='sm:max-w-md'
      titleClassName='text-destructive flex items-center gap-2'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => handleOpenChange(false)}
            disabled={loading}
          >
            {t('Cancel')}
          </Button>
          <Button
            variant='destructive'
            onClick={handleDisable}
            disabled={loading || !confirmed}
          >
            {loading && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            {loading ? t('Disabling...') : t('Disable 2FA')}
          </Button>
        </>
      }
    >
      <div className='space-y-4 py-4'>
        <Alert variant='destructive'>
          <AlertTriangle className='h-4 w-4' />
          <AlertDescription>
            {t('Warning: Disabling 2FA will make your account less secure.')}
          </AlertDescription>
        </Alert>

        <div className='flex items-start space-x-2'>
          <Checkbox
            id='confirm'
            checked={confirmed}
            onCheckedChange={(checked) => setConfirmed(checked as boolean)}
          />
          <Label
            htmlFor='confirm'
            className='text-sm leading-tight font-normal'
          >
            {t(
              'I understand that disabling 2FA will remove all protection and backup codes'
            )}
          </Label>
        </div>
      </div>
      <SecureVerificationDialog
        open={security.open}
        onOpenChange={(next) => !next && security.cancel()}
        methods={security.methods}
        state={security.state}
        onVerify={(method, value) => {
          void security.executeVerification(method, value)
        }}
        onCancel={security.cancel}
        onCodeChange={security.setCode}
        onMethodChange={security.switchMethod}
        onOAuthProviderChange={security.setOAuthProvider}
      />
    </Dialog>
  )
}
