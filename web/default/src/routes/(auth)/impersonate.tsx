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
import { createFileRoute } from '@tanstack/react-router'
import { Link2Off, LoaderCircle, ServerCrash, ShieldAlert } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { AuthLayout } from '@/features/auth/auth-layout'
import { saveUserId } from '@/features/auth/lib/storage'
import { redeemUserImpersonationTicket } from '@/features/users/api'
import { useAuthStore } from '@/stores/auth-store'

type ImpersonationPageState =
  | 'loading'
  | 'existing-session'
  | 'invalid'
  | 'server-error'

export const Route = createFileRoute('/(auth)/impersonate')({
  component: ImpersonatePage,
})

/**
 * Redeems an impersonation ticket in a clean browser context.
 */
function ImpersonatePage() {
  const { t } = useTranslation()
  const [state, setState] = useState<ImpersonationPageState>('loading')
  const started = useRef(false)

  useEffect(() => {
    if (started.current) return
    started.current = true

    const ticket = new URLSearchParams(window.location.hash.slice(1)).get(
      'ticket'
    )
    window.history.replaceState(null, '', '/impersonate')

    if (useAuthStore.getState().auth.user) {
      setState('existing-session')
      return
    }
    if (!ticket?.trim()) {
      setState('invalid')
      return
    }

    const redeem = async () => {
      try {
        const result = await redeemUserImpersonationTicket(ticket.trim())
        if (!result.success || !result.data) {
          if (result.code === 'impersonation_existing_session') {
            setState('existing-session')
          } else if (result.code === 'impersonation_ticket_invalid') {
            setState('invalid')
          } else {
            setState('server-error')
          }
          return
        }

        saveUserId(result.data.id)
        useAuthStore.getState().auth.setUser(result.data)
        window.location.replace('/dashboard')
      } catch {
        setState('server-error')
      }
    }

    void redeem()
  }, [])

  const content = {
    loading: {
      icon: (
        <LoaderCircle className='h-8 w-8 animate-spin' aria-hidden='true' />
      ),
      title: t('Establishing user session'),
      description: t('Verifying the one-time Incognito login link...'),
    },
    'existing-session': {
      icon: <ShieldAlert className='h-8 w-8' aria-hidden='true' />,
      title: t('This window already has a session'),
      description: t(
        'Paste the link into a signed-out Chrome or Edge Incognito window. This link has not been used.'
      ),
    },
    invalid: {
      icon: <Link2Off className='h-8 w-8' aria-hidden='true' />,
      title: t('Incognito login link is invalid'),
      description: t(
        'The link is missing, expired, changed, or already used. Return to the administrator window and generate a new link.'
      ),
    },
    'server-error': {
      icon: <ServerCrash className='h-8 w-8' aria-hidden='true' />,
      title: t('Failed to establish user session'),
      description: t(
        'Return to the administrator window and generate a new link.'
      ),
    },
  }[state]

  return (
    <AuthLayout>
      <main
        className='border-border/70 bg-card rounded-xl border px-6 py-8 shadow-sm sm:px-8'
        aria-live='polite'
      >
        <div className='bg-primary/10 text-primary mb-5 flex h-14 w-14 items-center justify-center rounded-full'>
          {content.icon}
        </div>
        <h2 className='text-2xl font-semibold tracking-tight'>
          {content.title}
        </h2>
        <p className='text-muted-foreground mt-3 text-sm leading-6 sm:text-base'>
          {content.description}
        </p>
      </main>
    </AuthLayout>
  )
}
