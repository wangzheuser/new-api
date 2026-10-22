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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { PasswordInput } from '@/components/password-input'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'

import { changeAccountPassword } from '../../api'

interface ChangePasswordDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  username: string
}

export function ChangePasswordDialog(props: ChangePasswordDialogProps) {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(false)
  const [formData, setFormData] = useState({
    originalPassword: '',
    newPassword: '',
    confirmPassword: '',
  })
  const security = useSecureVerification({
    onSuccess: () => {
      toast.success(t('Password changed successfully'))
      props.onOpenChange(false)
      setFormData({
        originalPassword: '',
        newPassword: '',
        confirmPassword: '',
      })
    },
  })

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!formData.originalPassword || !formData.newPassword) {
      toast.error(t('Please enter your current and new password'))
      return
    }
    if (formData.newPassword.length < 8) {
      toast.error(t('Password must be at least 8 characters'))
      return
    }
    if (formData.originalPassword === formData.newPassword) {
      toast.error(t('New password must be different from current password'))
      return
    }
    if (formData.newPassword !== formData.confirmPassword) {
      toast.error(t('Passwords do not match'))
      return
    }
    setLoading(true)
    try {
      await security.startVerification(
        (proofToken) =>
          changeAccountPassword(
            {
              original_password: formData.originalPassword,
              password: formData.newPassword,
            },
            proofToken ?? ''
          ),
        {
          operation: { scope: 'account.password.change' },
          title: t('Security verification'),
          description: t(
            'Confirm your identity before changing your password.'
          ),
        }
      )
    } finally {
      setLoading(false)
    }
  }

  return (
    <>
      <Dialog
        open={props.open && !security.open}
        onOpenChange={props.onOpenChange}
        title={t('Change Password')}
        description={
          <>
            {t('Update your password for account:')}{' '}
            <strong>{props.username}</strong>
          </>
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
              disabled={loading}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='submit'
              form='change-password-form'
              disabled={loading || security.isLoading}
            >
              {loading && <Loader2 className='size-4 animate-spin' />}
              {t('Change Password')}
            </Button>
          </>
        }
      >
        <form id='change-password-form' onSubmit={submit} className='space-y-4'>
          <div className='space-y-2'>
            <Label htmlFor='currentPassword'>{t('Current Password')}</Label>
            <PasswordInput
              id='currentPassword'
              value={formData.originalPassword}
              onChange={(event) =>
                setFormData((prev) => ({
                  ...prev,
                  originalPassword: event.target.value,
                }))
              }
              disabled={loading}
              required
              autoComplete='current-password'
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='newPassword'>{t('New Password')}</Label>
            <PasswordInput
              id='newPassword'
              value={formData.newPassword}
              onChange={(event) =>
                setFormData((prev) => ({
                  ...prev,
                  newPassword: event.target.value,
                }))
              }
              disabled={loading}
              required
              minLength={8}
              autoComplete='new-password'
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='confirmPassword'>{t('Confirm New Password')}</Label>
            <PasswordInput
              id='confirmPassword'
              value={formData.confirmPassword}
              onChange={(event) =>
                setFormData((prev) => ({
                  ...prev,
                  confirmPassword: event.target.value,
                }))
              }
              disabled={loading}
              required
              autoComplete='new-password'
            />
          </div>
        </form>
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
