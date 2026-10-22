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
import { AlertTriangle, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { logout } from '@/features/auth/api'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { useAuthStore } from '@/stores/auth-store'

import { deleteUserAccount } from '../../api'

interface DeleteAccountDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  username: string
}

export function DeleteAccountDialog(props: DeleteAccountDialogProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const reset = useAuthStore((state) => state.auth.reset)
  const [confirmation, setConfirmation] = useState('')
  const security = useSecureVerification({
    onSuccess: async () => {
      toast.success(t('Account deleted successfully'))
      try {
        await logout()
      } catch {
        // The account is already deleted; local state still must be cleared.
      }
      reset()
      props.onOpenChange(false)
      void navigate({ to: '/sign-in' })
    },
  })

  const handleDelete = async () => {
    if (confirmation !== props.username) {
      toast.error(t('Username confirmation does not match'))
      return
    }
    await security.startVerification(
      (proofToken) => deleteUserAccount(proofToken ?? ''),
      {
        operation: { scope: 'account.delete' },
        title: t('Security verification'),
        description: t('Confirm your identity before deleting your account.'),
      }
    )
  }

  return (
    <>
      <Dialog
        open={props.open && !security.open}
        onOpenChange={props.onOpenChange}
        title={
          <>
            <AlertTriangle className='text-destructive h-5 w-5' />
            {t('Delete Account')}
          </>
        }
        description={t(
          'This action cannot be undone. This will permanently delete your account and remove all your data from our servers.'
        )}
        contentClassName='sm:max-w-md'
        titleClassName='text-destructive flex items-center gap-2'
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
              variant='destructive'
              onClick={() => void handleDelete()}
              disabled={security.isLoading || confirmation !== props.username}
            >
              {security.isLoading && (
                <Loader2 className='size-4 animate-spin' />
              )}
              {t('Delete Account')}
            </Button>
          </>
        }
      >
        <div className='my-6 space-y-4'>
          <Alert variant='destructive'>
            <AlertTriangle className='h-4 w-4' />
            <AlertDescription>
              {t('Warning: This action is permanent and irreversible!')}
            </AlertDescription>
          </Alert>
          <div className='space-y-2'>
            <Label htmlFor='confirmation'>
              {t('Type')} <strong>{props.username}</strong> {t('to confirm')}
            </Label>
            <Input
              id='confirmation'
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
              placeholder={props.username}
              autoComplete='off'
            />
          </div>
        </div>
      </Dialog>
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
