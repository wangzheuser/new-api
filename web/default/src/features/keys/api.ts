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
import { api } from '@/lib/api'

import type {
  ApiKey,
  ApiResponse,
  GetApiKeysParams,
  GetApiKeysResponse,
  SearchApiKeysParams,
  ApiKeyFormData,
} from './types'

// ============================================================================
// API Key Management
// ============================================================================

// Build the optional target-user scope shared by every API key request.
function getTargetUserParams(targetUserId?: number) {
  return targetUserId ? { user_id: targetUserId } : {}
}

// Get paginated API keys list
export async function getApiKeys(
  params: GetApiKeysParams = {},
  targetUserId?: number
): Promise<GetApiKeysResponse> {
  const { p = 1, size = 10 } = params
  const res = await api.get('/api/token/', {
    params: { p, size, ...getTargetUserParams(targetUserId) },
  })
  return res.data
}

// Search API keys by keyword or token (with pagination)
export async function searchApiKeys(
  params: SearchApiKeysParams,
  targetUserId?: number
): Promise<GetApiKeysResponse> {
  const { keyword = '', token = '', p, size } = params
  const res = await api.get('/api/token/search', {
    params: {
      keyword: keyword || undefined,
      token: token || undefined,
      p,
      size,
      ...getTargetUserParams(targetUserId),
    },
  })
  return res.data
}

// Get single API key by ID
export async function getApiKey(
  id: number,
  targetUserId?: number
): Promise<ApiResponse<ApiKey>> {
  const res = await api.get(`/api/token/${id}`, {
    params: getTargetUserParams(targetUserId),
  })
  return res.data
}

// Create a new API key
export async function createApiKey(
  data: ApiKeyFormData,
  targetUserId?: number
): Promise<ApiResponse<ApiKey>> {
  const res = await api.post('/api/token/', data, {
    params: getTargetUserParams(targetUserId),
  })
  return res.data
}

// Update an existing API key
export async function updateApiKey(
  data: ApiKeyFormData & { id: number },
  targetUserId?: number
): Promise<ApiResponse<ApiKey>> {
  const res = await api.put('/api/token/', data, {
    params: getTargetUserParams(targetUserId),
  })
  return res.data
}

// Delete a single API key
export async function deleteApiKey(
  id: number,
  targetUserId?: number
): Promise<ApiResponse> {
  const res = await api.delete(`/api/token/${id}/`, {
    params: getTargetUserParams(targetUserId),
  })
  return res.data
}

// Batch delete multiple API keys
export async function batchDeleteApiKeys(
  ids: number[],
  targetUserId?: number
): Promise<ApiResponse<number>> {
  const res = await api.post(
    '/api/token/batch',
    { ids },
    { params: getTargetUserParams(targetUserId) }
  )
  return res.data
}

// Update API key status (enable/disable)
export async function updateApiKeyStatus(
  id: number,
  status: number,
  targetUserId?: number
): Promise<ApiResponse<ApiKey>> {
  const res = await api.put(
    '/api/token/',
    { id, status },
    {
      params: {
        status_only: true,
        ...getTargetUserParams(targetUserId),
      },
    }
  )
  return res.data
}

// Fetch the real (unmasked) key for a token by ID
export async function fetchTokenKey(
  id: number,
  targetUserId?: number
): Promise<{ success: boolean; message?: string; data?: { key: string } }> {
  const res = await api.post(`/api/token/${id}/key`, undefined, {
    params: getTargetUserParams(targetUserId),
  })
  return res.data
}

// Batch fetch real (unmasked) keys for multiple tokens
export async function fetchTokenKeysBatch(
  ids: number[],
  targetUserId?: number
): Promise<{
  success: boolean
  message?: string
  data?: { keys: Record<number, string> }
}> {
  const res = await api.post(
    '/api/token/batch/keys',
    { ids },
    { params: getTargetUserParams(targetUserId) }
  )
  return res.data
}
