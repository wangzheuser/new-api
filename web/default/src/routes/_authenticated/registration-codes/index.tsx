/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { createFileRoute, redirect } from '@tanstack/react-router'
import z from 'zod'

import { RegistrationCodes } from '@/features/registration-codes'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

const registrationCodesSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(20),
  filter: z.string().optional().catch(''),
  status: z
    .array(z.enum(['1', '2']))
    .optional()
    .catch([]),
  usage: z
    .array(z.enum(['unused', 'partial', 'exhausted']))
    .optional()
    .catch([]),
  validity: z
    .array(z.enum(['pending', 'active', 'expired']))
    .optional()
    .catch([]),
})

export const Route = createFileRoute('/_authenticated/registration-codes/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.SUPER_ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  validateSearch: registrationCodesSearchSchema,
  component: RegistrationCodes,
})
