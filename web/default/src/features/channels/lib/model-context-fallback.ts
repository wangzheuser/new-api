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
import type { ModelContextFallback } from '../types'

export const DEFAULT_CONTEXT_FALLBACK_THRESHOLD = 90
export const MAX_CONTEXT_FALLBACK_RULES = 256

const KNOWN_RULE_FIELDS = new Set([
  'source_context_window_tokens',
  'threshold_percent',
  'fallback_model',
  'fallback_context_window_tokens',
  'route_mode',
  'target_channel_ids',
])

export type ContextFallbackRuleDraft = {
  id: string
  sourceModel: string
  sourceContextWindowTokens: string
  thresholdPercent: string
  fallbackModel: string
  fallbackContextWindowTokens: string
  routeMode: 'same_channel' | 'cross_channel'
  targetMode: 'auto' | 'limited'
  targetChannelIds: number[]
  extra: Record<string, unknown>
}

export type ContextFallbackRuleField =
  | 'sourceModel'
  | 'sourceContextWindowTokens'
  | 'thresholdPercent'
  | 'fallbackModel'
  | 'fallbackContextWindowTokens'
  | 'routeMode'
  | 'targetChannelIds'

export type ContextFallbackValidationError = {
  message: string
  ruleId?: string
  field?: ContextFallbackRuleField
}

export type ParsedContextFallbackValue = {
  rules: ContextFallbackRuleDraft[]
  error: ContextFallbackValidationError | null
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isPositiveSafeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) > 0
}

function isIntegerText(value: string): boolean {
  return /^\d+$/.test(value)
}

/**
 * Validates the persisted wire format before it is converted into editable drafts.
 */
function validateWireRules(
  value: Record<string, unknown>
): ContextFallbackValidationError | null {
  const entries = Object.entries(value)
  if (entries.length > MAX_CONTEXT_FALLBACK_RULES) {
    return {
      message: 'Context fallback rules cannot exceed 256 entries',
    }
  }

  for (const [sourceModel, rawRule] of entries) {
    if (
      !sourceModel.trim() ||
      sourceModel !== sourceModel.trim() ||
      sourceModel.length > 255 ||
      !isRecord(rawRule)
    ) {
      return {
        message: 'Context fallback rule contains an invalid source model',
      }
    }
    if (!isPositiveSafeInteger(rawRule.source_context_window_tokens)) {
      return {
        message: 'Source context window must be a positive integer',
      }
    }
    if (
      rawRule.threshold_percent != null &&
      (!Number.isInteger(rawRule.threshold_percent) ||
        Number(rawRule.threshold_percent) < 1 ||
        Number(rawRule.threshold_percent) > 100)
    ) {
      return { message: 'Threshold percent must be between 1 and 100' }
    }
    if (
      typeof rawRule.fallback_model !== 'string' ||
      !rawRule.fallback_model.trim() ||
      rawRule.fallback_model !== rawRule.fallback_model.trim() ||
      rawRule.fallback_model.length > 255 ||
      rawRule.fallback_model === sourceModel
    ) {
      return { message: 'Fallback model is invalid' }
    }
    if (!isPositiveSafeInteger(rawRule.fallback_context_window_tokens)) {
      return {
        message: 'Fallback context window must be a positive integer',
      }
    }
    if (
      rawRule.route_mode !== 'same_channel' &&
      rawRule.route_mode !== 'cross_channel'
    ) {
      return { message: 'Route mode must be same_channel or cross_channel' }
    }

    const targetIds = rawRule.target_channel_ids ?? []
    if (
      !Array.isArray(targetIds) ||
      targetIds.some((id) => !isPositiveSafeInteger(id)) ||
      new Set(targetIds).size !== targetIds.length ||
      (rawRule.route_mode === 'same_channel' && targetIds.length > 0)
    ) {
      return { message: 'Target channel IDs are invalid' }
    }
  }

  return null
}

/**
 * Parses persisted JSON without normalizing or writing it back to the form.
 */
export function parseContextFallbackValue(
  value: string
): ParsedContextFallbackValue {
  if (!value.trim()) return { rules: [], error: null }

  let parsed: unknown
  try {
    parsed = JSON.parse(value)
  } catch {
    return {
      rules: [],
      error: { message: 'Context fallback rules must be valid JSON' },
    }
  }
  if (!isRecord(parsed)) {
    return {
      rules: [],
      error: { message: 'Context fallback rules must be a JSON object' },
    }
  }

  const wireError = validateWireRules(parsed)
  if (wireError) return { rules: [], error: wireError }

  const rules = Object.entries(parsed).map(([sourceModel, rawRule], index) => {
    const rule = rawRule as Record<string, unknown>
    const targetIds = (rule.target_channel_ids as number[] | undefined) ?? []
    const extra = Object.fromEntries(
      Object.entries(rule).filter(([key]) => !KNOWN_RULE_FIELDS.has(key))
    )

    return {
      id: `context-fallback-${index + 1}`,
      sourceModel,
      sourceContextWindowTokens: String(rule.source_context_window_tokens),
      thresholdPercent: String(
        rule.threshold_percent ?? DEFAULT_CONTEXT_FALLBACK_THRESHOLD
      ),
      fallbackModel: String(rule.fallback_model),
      fallbackContextWindowTokens: String(rule.fallback_context_window_tokens),
      routeMode: rule.route_mode as 'same_channel' | 'cross_channel',
      targetMode: targetIds.length > 0 ? 'limited' : 'auto',
      targetChannelIds: [...targetIds],
      extra,
    } satisfies ContextFallbackRuleDraft
  })

  return { rules, error: null }
}

/**
 * Validates editable drafts with the same constraints as the backend contract.
 */
export function validateContextFallbackDrafts(
  rules: ContextFallbackRuleDraft[]
): ContextFallbackValidationError[] {
  if (rules.length > MAX_CONTEXT_FALLBACK_RULES) {
    return [{ message: 'Context fallback rules cannot exceed 256 entries' }]
  }

  const errors: ContextFallbackValidationError[] = []
  const seenModels = new Set<string>()
  for (const rule of rules) {
    if (
      !rule.sourceModel.trim() ||
      rule.sourceModel !== rule.sourceModel.trim() ||
      rule.sourceModel.length > 255
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'sourceModel',
        message: 'Enter a valid source model name without surrounding spaces',
      })
    } else if (seenModels.has(rule.sourceModel)) {
      errors.push({
        ruleId: rule.id,
        field: 'sourceModel',
        message: 'Duplicate source model rules are not allowed',
      })
    } else {
      seenModels.add(rule.sourceModel)
    }

    const sourceWindow = Number(rule.sourceContextWindowTokens)
    if (
      !isIntegerText(rule.sourceContextWindowTokens) ||
      !isPositiveSafeInteger(sourceWindow)
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'sourceContextWindowTokens',
        message: 'Source context window must be a positive integer',
      })
    }

    const threshold = Number(rule.thresholdPercent)
    if (
      !isIntegerText(rule.thresholdPercent) ||
      !Number.isInteger(threshold) ||
      threshold < 1 ||
      threshold > 100
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'thresholdPercent',
        message: 'Threshold percent must be between 1 and 100',
      })
    }

    if (
      !rule.fallbackModel.trim() ||
      rule.fallbackModel !== rule.fallbackModel.trim() ||
      rule.fallbackModel.length > 255 ||
      rule.fallbackModel === rule.sourceModel
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'fallbackModel',
        message: 'Fallback model must differ from the source model',
      })
    }

    const fallbackWindow = Number(rule.fallbackContextWindowTokens)
    if (
      !isIntegerText(rule.fallbackContextWindowTokens) ||
      !isPositiveSafeInteger(fallbackWindow)
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'fallbackContextWindowTokens',
        message: 'Fallback context window must be a positive integer',
      })
    }

    if (
      rule.routeMode !== 'same_channel' &&
      rule.routeMode !== 'cross_channel'
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'routeMode',
        message: 'Select a valid route mode',
      })
    }

    if (
      rule.targetChannelIds.some((id) => !isPositiveSafeInteger(id)) ||
      new Set(rule.targetChannelIds).size !== rule.targetChannelIds.length
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'targetChannelIds',
        message: 'Target channel IDs must be unique positive integers',
      })
    }
    if (
      rule.routeMode === 'cross_channel' &&
      rule.targetMode === 'limited' &&
      rule.targetChannelIds.length === 0
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'targetChannelIds',
        message: 'Select at least one target channel',
      })
    }
  }

  return errors
}

/**
 * Serializes valid drafts to the existing backend map structure.
 */
export function serializeContextFallbackDrafts(
  rules: ContextFallbackRuleDraft[]
): string {
  const validationErrors = validateContextFallbackDrafts(rules)
  if (validationErrors.length > 0) {
    throw new Error(validationErrors[0].message)
  }

  const result: Record<string, ModelContextFallback & Record<string, unknown>> =
    {}
  for (const rule of rules) {
    const serialized: ModelContextFallback & Record<string, unknown> = {
      ...rule.extra,
      source_context_window_tokens: Number(rule.sourceContextWindowTokens),
      threshold_percent: Number(rule.thresholdPercent),
      fallback_model: rule.fallbackModel,
      fallback_context_window_tokens: Number(rule.fallbackContextWindowTokens),
      route_mode: rule.routeMode,
    }
    if (rule.routeMode === 'cross_channel' && rule.targetMode === 'limited') {
      serialized.target_channel_ids = [...rule.targetChannelIds]
    }
    result[rule.sourceModel] = serialized
  }

  return rules.length === 0 ? '' : JSON.stringify(result, null, 2)
}

/**
 * Computes the integer trigger token count using the backend formula.
 */
export function calculateContextFallbackTriggerTokens(
  sourceContextWindowTokens: string,
  thresholdPercent: string
): number | null {
  const source = Number(sourceContextWindowTokens)
  const percent = Number(thresholdPercent)
  if (!isPositiveSafeInteger(source) || !Number.isInteger(percent)) return null

  return (
    Math.floor(source / 100) * percent +
    Math.floor(((source % 100) * percent) / 100)
  )
}

/**
 * Creates the visual defaults for a newly added rule.
 */
export function createEmptyContextFallbackRule(
  id: string
): ContextFallbackRuleDraft {
  return {
    id,
    sourceModel: '',
    sourceContextWindowTokens: '',
    thresholdPercent: String(DEFAULT_CONTEXT_FALLBACK_THRESHOLD),
    fallbackModel: '',
    fallbackContextWindowTokens: '',
    routeMode: 'cross_channel',
    targetMode: 'auto',
    targetChannelIds: [],
    extra: {},
  }
}
