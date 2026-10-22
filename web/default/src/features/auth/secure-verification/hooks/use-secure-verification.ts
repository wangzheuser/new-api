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
import i18next from 'i18next'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'

import {
  extractVerificationInfo,
  isVerificationRequiredError,
} from '@/lib/secure-verification'

import { checkVerificationMethods, verify } from '../api'
import type {
  SecureVerificationState,
  StartVerificationOptions,
  UseSecureVerificationOptions,
  VerificationMethod,
  VerificationMethods,
  VerificationOperation,
} from '../types'

type ApiCall = ((proofToken?: string) => Promise<unknown>) | null

interface InternalState extends SecureVerificationState {
  apiCall: ApiCall
}

const defaultMethods: VerificationMethods = {
  has2FA: false,
  hasPasskey: false,
  passkeySupported: false,
  hasPassword: false,
  passwordEncryptionEnabled: false,
  oauthProviders: [],
}

const initialState: InternalState = {
  method: null,
  loading: false,
  code: '',
  oauthProvider: undefined,
  title: undefined,
  description: undefined,
  apiCall: null,
}

export function useSecureVerification(
  options: UseSecureVerificationOptions = {}
) {
  const { onSuccess, onError, successMessage, autoReset = true } = options

  const [methods, setMethods] = useState<VerificationMethods>(defaultMethods)
  const [state, setState] = useState<InternalState>(initialState)
  const [open, setOpen] = useState(false)
  const controllerRef = useRef<AbortController | null>(null)

  useEffect(
    () => () => {
      controllerRef.current?.abort()
    },
    []
  )

  const fetchVerificationMethods = useCallback(
    async (operation: VerificationOperation) => {
      const result = await checkVerificationMethods(operation)
      setMethods(result)
      return result
    },
    []
  )

  const reset = useCallback(() => {
    controllerRef.current?.abort()
    controllerRef.current = null
    setState(initialState)
    setOpen(false)
  }, [])

  const startVerification = useCallback(
    async (
      apiCall: (proofToken?: string) => Promise<unknown>,
      config: StartVerificationOptions
    ) => {
      const { preferredMethod, title, description, operation } = config
      let availableMethods: VerificationMethods
      try {
        availableMethods = await fetchVerificationMethods(operation)
      } catch (error) {
        const message =
          error instanceof Error
            ? error.message
            : i18next.t('Verification failed')
        toast.error(message)
        onError?.(error)
        return false
      }

      if (
        !availableMethods.has2FA &&
        !availableMethods.hasPasskey &&
        !availableMethods.hasPassword &&
        availableMethods.oauthProviders.length === 0
      ) {
        toast.error(
          i18next.t(
            'Please enable Two-factor Authentication or Passkey before proceeding'
          )
        )
        onError?.(
          new Error(
            'No verification methods available. Enable 2FA or Passkey to continue.'
          )
        )
        return false
      }

      let defaultMethod: VerificationMethod | null = preferredMethod ?? null
      if (!defaultMethod) {
        if (availableMethods.hasPasskey && availableMethods.passkeySupported) {
          defaultMethod = 'passkey'
        } else if (availableMethods.has2FA) {
          defaultMethod = '2fa'
        } else if (availableMethods.hasPassword) {
          defaultMethod = 'password'
        } else if (availableMethods.oauthProviders.length > 0) {
          defaultMethod = 'oauth'
        }
      }

      controllerRef.current?.abort()
      controllerRef.current = new AbortController()
      setState((prev) => ({
        ...prev,
        apiCall,
        method: defaultMethod,
        title,
        description,
        operation,
        oauthProvider:
          defaultMethod === 'oauth'
            ? availableMethods.oauthProviders[0]?.slug
            : undefined,
      }))
      setOpen(true)
      return true
    },
    [fetchVerificationMethods, onError]
  )

  const executeVerification = useCallback(
    async (method?: VerificationMethod, code?: string) => {
      if (!state.apiCall) {
        toast.error(i18next.t('Verification is not configured properly'))
        return
      }

      const actualMethod = method ?? state.method
      if (!actualMethod) {
        toast.error(i18next.t('Select a verification method first'))
        return
      }

      setState((prev) => ({ ...prev, loading: true }))

      try {
        if (!state.operation) {
          throw new Error('Verification operation is not configured')
        }
        const proof = await verify(
          actualMethod,
          state.operation,
          actualMethod === 'oauth' ? state.oauthProvider : (code ?? state.code),
          controllerRef.current?.signal,
          methods.passwordEncryptionEnabled
        )
        const result = await state.apiCall(proof.proof_token)

        if (successMessage) {
          toast.success(successMessage)
        }

        onSuccess?.(result, actualMethod)

        if (autoReset) {
          reset()
        }

        return result
      } catch (error) {
        const message =
          error instanceof Error
            ? error.message
            : i18next.t('Verification failed')
        toast.error(message)
        onError?.(error)
        throw error
      } finally {
        setState((prev) => ({ ...prev, loading: false }))
      }
    },
    [
      state,
      methods.passwordEncryptionEnabled,
      successMessage,
      onSuccess,
      onError,
      autoReset,
      reset,
    ]
  )

  const setCode = useCallback((code: string) => {
    setState((prev) => ({ ...prev, code }))
  }, [])

  const setOAuthProvider = useCallback((oauthProvider: string) => {
    setState((prev) => ({ ...prev, oauthProvider }))
  }, [])

  const switchMethod = useCallback((method: VerificationMethod) => {
    setState((prev) => ({
      ...prev,
      method,
      code: '',
      oauthProvider: method === 'oauth' ? prev.oauthProvider : undefined,
    }))
  }, [])

  const cancel = useCallback(() => {
    reset()
  }, [reset])

  const withVerification = useCallback(
    async (
      apiCall: (proofToken?: string) => Promise<unknown>,
      config: StartVerificationOptions
    ) => {
      try {
        return await apiCall()
      } catch (error) {
        if (isVerificationRequiredError(error)) {
          const info = extractVerificationInfo(error)
          toast.info(info.message)
          await startVerification(apiCall, config)
          return null
        }
        throw error
      }
    },
    [startVerification]
  )

  const canUseMethod = useCallback(
    (method: VerificationMethod) => {
      if (method === '2fa') return methods.has2FA
      if (method === 'passkey') {
        return methods.hasPasskey && methods.passkeySupported
      }
      if (method === 'password') return methods.hasPassword
      if (method === 'oauth') return methods.oauthProviders.length > 0
      return false
    },
    [methods]
  )

  const recommendedMethod = useMemo<VerificationMethod | null>(() => {
    if (methods.hasPasskey && methods.passkeySupported) return 'passkey'
    if (methods.has2FA) return '2fa'
    if (methods.hasPassword) return 'password'
    if (methods.oauthProviders.length > 0) return 'oauth'
    return null
  }, [methods])

  return {
    open,
    setOpen,
    methods,
    state,
    startVerification,
    executeVerification,
    cancel,
    reset,
    setCode,
    setOAuthProvider,
    switchMethod,
    withVerification,
    fetchVerificationMethods,
    canUseMethod,
    recommendedMethod,
    hasAnyMethod:
      methods.has2FA ||
      methods.hasPasskey ||
      methods.hasPassword ||
      methods.oauthProviders.length > 0,
    isLoading: state.loading,
    currentMethod: state.method,
    code: state.code,
  }
}
