/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

export type RegistrationCode = {
  id: number
  code: string
  status: number
  name: string
  max_uses: number
  used_count: number
  open_time: number
  end_time: number
  created_time: number
}

export type RegistrationCodePage = {
  items: RegistrationCode[]
  total: number
}

export type ApiResponse<T = null> = {
  success: boolean
  message?: string
  data?: T
}
