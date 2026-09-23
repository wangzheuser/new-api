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
import { z } from 'zod'

export const CACHE_OPTION = 'cache_usage_simulation.policy'
export const CONTEXT_OPTION = 'context_truncation.policy'
export const SYSTEM_PROMPT_OPTION = 'system_prompt.policy'

export const cachePolicySchema = z
  .object({
    enabled: z.boolean().optional(),
    force_disabled: z.boolean().optional(),
    mode: z.enum(['inherit', 'off', 'custom']).optional(),
    creation_trigger_percent: z.number().int().min(0).max(100).default(20),
    read_trigger_percent: z.number().int().min(0).max(100).default(60),
    creation_token_percent: z.number().int().min(0).max(100).default(30),
    read_token_percent: z.number().int().min(0).max(100).default(50),
  })
  .passthrough()
  .refine(
    (p) => p.creation_trigger_percent !== 100 || p.read_trigger_percent !== 100,
    'Both cache probabilities cannot be 100%.'
  )
  .refine(
    (p) => p.creation_token_percent + p.read_token_percent <= 100,
    'Cache token shares must total at most 100%.'
  )

const ruleSchema = z
  .object({
    mode: z.enum(['inherit', 'off', 'custom']),
    window_tokens: z.number().int().min(1).max(2147483647).optional(),
    output_reserve_tokens: z.number().int().min(1).max(1073741823).optional(),
  })
  .superRefine((r, ctx) => {
    if (r.mode !== 'custom') return
    const window = r.window_tokens ?? 0
    const safety = Math.min(Math.ceil(window * 0.02), 8192)
    if (
      !window ||
      !r.output_reserve_tokens ||
      safety >= window ||
      (r.output_reserve_tokens ?? 0) + safety >= window
    ) {
      ctx.addIssue({
        code: 'custom',
        message: 'A valid context window and output budget are required.',
      })
    }
  })

export const contextPolicySchema = z
  .object({
    force_disabled: z.boolean().optional(),
    models: z
      .record(
        z
          .string()
          .min(1)
          .max(255)
          .refine((id) => id.trim() === id),
        ruleSchema
      )
      .refine((models) => Object.keys(models).length <= 256),
  })
  .passthrough()

export type CachePolicy = z.input<typeof cachePolicySchema>
export type ContextPolicy = z.input<typeof contextPolicySchema>
export type ContextRule = ContextPolicy['models'][string]

export const systemPromptPolicySchema = z.object({
  models: z
    .record(
      z
        .string()
        .min(1)
        .max(255)
        .refine((id) => id.trim() === id),
      z.string().trim().min(1).max(65536)
    )
    .refine((models) => Object.keys(models).length <= 256),
})

export type SystemPromptPolicy = z.input<typeof systemPromptPolicySchema>

/** Validate a draft before replacing or adding one model prompt. */
export function updateGlobalModelSystemPrompts(
  value: Record<string, string>,
  previousModel: string | null,
  model: string,
  prompt: string
): Record<string, string> | null {
  const id = model.trim()
  if (
    !id ||
    !prompt.trim() ||
    (id !== previousModel && Object.hasOwn(value, id))
  ) {
    return null
  }

  const next = { ...value, [id]: prompt }
  if (previousModel !== null && previousModel !== id) delete next[previousModel]
  return validateInputPolicies(
    JSON.stringify({ system_prompt: { models: next } }),
    false
  )
    ? next
    : null
}

export const DEFAULT_CACHE: CachePolicy = {
  enabled: false,
  creation_trigger_percent: 20,
  read_trigger_percent: 60,
  creation_token_percent: 30,
  read_token_percent: 50,
}

/** Validate complete payloads without removing provider-specific settings. */
export function validateInputPolicies(
  settings: string,
  channel = true
): boolean {
  try {
    if (new TextEncoder().encode(settings).length > 65535) return false
    const parsed = JSON.parse(settings)
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      return false
    }
    if (parsed.context_truncation !== undefined) {
      const p = contextPolicySchema.parse(parsed.context_truncation)
      if (channel && p.force_disabled) return false
      if (
        !channel &&
        Object.values(p.models).some((r) => r.mode === 'inherit')
      ) {
        return false
      }
    }
    if (parsed.system_prompt !== undefined) {
      systemPromptPolicySchema.parse(parsed.system_prompt)
    }
    if (parsed.cache_usage_simulation !== undefined) {
      const p = cachePolicySchema.parse(parsed.cache_usage_simulation)
      if (channel && (p.force_disabled || p.enabled)) return false
      if (!channel && p.mode !== undefined) return false
    }
    return true
  } catch {
    return false
  }
}

/** Merge only the selected feature; unrelated and future provider fields survive. */
export function mergeInputPolicy(
  settings: string,
  key: string,
  value: unknown
): string {
  return JSON.stringify({ ...JSON.parse(settings || '{}'), [key]: value })
}
