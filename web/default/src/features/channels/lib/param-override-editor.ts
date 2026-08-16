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

export const PARAM_OVERRIDE_PHASE_VALUES = [
  'request',
  'response',
  'final_error',
] as const

export const PARAM_OVERRIDE_CONDITION_SOURCE_VALUES = [
  'auto',
  'body',
  'semantic',
  'context',
] as const

export const PARAM_OVERRIDE_OPERATION_MODE_VALUES = [
  'set',
  'delete',
  'append',
  'prepend',
  'copy',
  'move',
  'replace',
  'regex_replace',
  'trim_prefix',
  'trim_suffix',
  'ensure_prefix',
  'ensure_suffix',
  'trim_space',
  'to_lower',
  'to_upper',
  'return_error',
  'prune_objects',
  'pass_headers',
  'sync_fields',
  'set_header',
  'delete_header',
  'copy_header',
  'move_header',
] as const

export const PARAM_OVERRIDE_CONDITION_MODE_VALUES = [
  'full',
  'prefix',
  'suffix',
  'contains',
  'gt',
  'gte',
  'lt',
  'lte',
] as const

export type ParamOverridePhase = (typeof PARAM_OVERRIDE_PHASE_VALUES)[number]
export type ParamOverrideConditionSource =
  (typeof PARAM_OVERRIDE_CONDITION_SOURCE_VALUES)[number]

export type ParamOverrideConditionDraft = {
  id: string
  source: ParamOverrideConditionSource
  path: string
  mode: string
  value_text: string
  invert: boolean
  pass_missing_key: boolean
}

export type ParamOverrideOperationDraft = {
  id: string
  wire_id?: string
  phase: ParamOverridePhase
  description: string
  path: string
  mode: string
  from: string
  to: string
  value_text: string
  keep_origin: boolean
  logic: string
  conditions: ParamOverrideConditionDraft[]
}

export type ParamOverrideVisualCompatibilityIssue =
  | 'invalid_operations'
  | 'unsupported_top_level_field'
  | 'unsupported_operation_field'
  | 'invalid_operation_field'
  | 'unsupported_phase'
  | 'unsupported_operation_mode'
  | 'unsupported_condition_logic'
  | 'invalid_conditions'
  | 'unsupported_condition_field'
  | 'invalid_condition_field'
  | 'unsupported_condition_mode'
  | 'unsupported_condition_source'
  | 'semantic_source_requires_response'
  | 'unsupported_return_error_field'

export type ParamOverridePhaseValidationIssue = {
  kind: 'unsupported_mode' | 'unconditional_not_last'
  line: number
  phase: Exclude<ParamOverridePhase, 'request'>
}

export type ParamOverrideConditionValidationIssue = {
  kind: 'missing_path'
  line: number
  condition: number
}

export type ParamOverrideEditorDocument = {
  kind: 'empty' | 'invalid' | 'unsupported' | 'operations' | 'legacy'
  sourceText: string
  parsed?: Record<string, unknown>
  issue?: ParamOverrideVisualCompatibilityIssue
}

const PHASE_VALUE_SET = new Set<string>(PARAM_OVERRIDE_PHASE_VALUES)
const OPERATION_MODE_VALUE_SET = new Set<string>(
  PARAM_OVERRIDE_OPERATION_MODE_VALUES
)
const CONDITION_MODE_VALUE_SET = new Set<string>(
  PARAM_OVERRIDE_CONDITION_MODE_VALUES
)
const WIRE_CONDITION_SOURCE_VALUE_SET = new Set<string>([
  'body',
  'semantic',
  'context',
])
const TOP_LEVEL_FIELD_SET = new Set(['operations'])
const OPERATION_FIELD_SET = new Set([
  'id',
  'description',
  'phase',
  'path',
  'mode',
  'value',
  'keep_origin',
  'from',
  'to',
  'conditions',
  'logic',
])
const CONDITION_FIELD_SET = new Set([
  'source',
  'path',
  'mode',
  'value',
  'invert',
  'pass_missing_key',
])
const RETURN_ERROR_FIELD_SET = new Set([
  'message',
  'msg',
  'status_code',
  'status',
  'code',
  'type',
  'skip_retry',
])

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value)

/**
 * Reports whether an operations document can be edited visually without
 * silently replacing an unknown phase, mode, or condition source.
 */
export function getParamOverrideVisualCompatibilityIssue(
  value: Record<string, unknown>
): ParamOverrideVisualCompatibilityIssue | null {
  if (!Array.isArray(value.operations)) return 'invalid_operations'
  if (Object.keys(value).some((key) => !TOP_LEVEL_FIELD_SET.has(key))) {
    return 'unsupported_top_level_field'
  }

  for (const operationValue of value.operations) {
    if (!isRecord(operationValue)) return 'invalid_operations'
    if (
      Object.keys(operationValue).some((key) => !OPERATION_FIELD_SET.has(key))
    ) {
      return 'unsupported_operation_field'
    }

    if (
      (operationValue.id !== undefined &&
        typeof operationValue.id !== 'string') ||
      (operationValue.description !== undefined &&
        typeof operationValue.description !== 'string') ||
      (operationValue.path !== undefined &&
        typeof operationValue.path !== 'string') ||
      (operationValue.from !== undefined &&
        typeof operationValue.from !== 'string') ||
      (operationValue.to !== undefined &&
        typeof operationValue.to !== 'string') ||
      (operationValue.keep_origin !== undefined &&
        typeof operationValue.keep_origin !== 'boolean')
    ) {
      return 'invalid_operation_field'
    }

    const phase = operationValue.phase ?? 'request'
    if (typeof phase !== 'string' || !PHASE_VALUE_SET.has(phase)) {
      return 'unsupported_phase'
    }

    if (
      typeof operationValue.mode !== 'string' ||
      !OPERATION_MODE_VALUE_SET.has(operationValue.mode)
    ) {
      return 'unsupported_operation_mode'
    }

    if (operationValue.logic !== undefined) {
      if (
        typeof operationValue.logic !== 'string' ||
        !['AND', 'OR'].includes(operationValue.logic.trim().toUpperCase())
      ) {
        return 'unsupported_condition_logic'
      }
    }

    if (
      operationValue.mode === 'return_error' &&
      isRecord(operationValue.value) &&
      Object.keys(operationValue.value).some(
        (key) => !RETURN_ERROR_FIELD_SET.has(key)
      )
    ) {
      return 'unsupported_return_error_field'
    }

    if (operationValue.conditions === undefined) continue
    if (!Array.isArray(operationValue.conditions)) return 'invalid_conditions'

    for (const conditionValue of operationValue.conditions) {
      if (!isRecord(conditionValue)) return 'invalid_conditions'
      if (
        Object.keys(conditionValue).some((key) => !CONDITION_FIELD_SET.has(key))
      ) {
        return 'unsupported_condition_field'
      }
      if (
        (conditionValue.path !== undefined &&
          typeof conditionValue.path !== 'string') ||
        (conditionValue.invert !== undefined &&
          typeof conditionValue.invert !== 'boolean') ||
        (conditionValue.pass_missing_key !== undefined &&
          typeof conditionValue.pass_missing_key !== 'boolean')
      ) {
        return 'invalid_condition_field'
      }

      const conditionMode = conditionValue.mode ?? 'full'
      if (
        typeof conditionMode !== 'string' ||
        !CONDITION_MODE_VALUE_SET.has(conditionMode)
      ) {
        return 'unsupported_condition_mode'
      }

      if (
        conditionValue.source !== undefined &&
        (typeof conditionValue.source !== 'string' ||
          !WIRE_CONDITION_SOURCE_VALUE_SET.has(conditionValue.source))
      ) {
        return 'unsupported_condition_source'
      }
      if (conditionValue.source === 'semantic' && phase !== 'response') {
        return 'semantic_source_requires_response'
      }
    }
  }

  return null
}

/**
 * Classifies raw editor input while retaining the exact source text for Raw
 * JSON fallback when the visual editor does not understand the schema.
 */
export function classifyParamOverrideEditorDocument(
  rawValue: string
): ParamOverrideEditorDocument {
  const sourceText = typeof rawValue === 'string' ? rawValue : ''
  const trimmed = sourceText.trim()
  if (!trimmed) return { kind: 'empty', sourceText }

  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    return { kind: 'invalid', sourceText }
  }

  if (!isRecord(parsed)) return { kind: 'invalid', sourceText }
  if (!Object.hasOwn(parsed, 'operations')) {
    return { kind: 'legacy', sourceText, parsed }
  }

  const issue = getParamOverrideVisualCompatibilityIssue(parsed)
  if (issue) return { kind: 'unsupported', sourceText, parsed, issue }
  return { kind: 'operations', sourceText, parsed }
}

/**
 * Converts the optional wire value into the editor's explicit Auto state.
 */
export function normalizeParamOverrideConditionSource(
  source: unknown
): ParamOverrideConditionSource {
  if (source === undefined) return 'auto'
  if (source === 'body' || source === 'semantic' || source === 'context') {
    return source
  }
  throw new Error(`unsupported parameter override condition source: ${source}`)
}

/**
 * Omits Auto to preserve the legacy body-then-context lookup behavior.
 */
export function serializeParamOverrideConditionSource(
  source: ParamOverrideConditionSource
): Exclude<ParamOverrideConditionSource, 'auto'> | undefined {
  return source === 'auto' ? undefined : source
}

/**
 * Omits request because it is the backward-compatible wire default.
 */
export function serializeParamOverridePhase(
  phase: ParamOverridePhase
): Exclude<ParamOverridePhase, 'request'> | undefined {
  return phase === 'request' ? undefined : phase
}

/**
 * Returns the status-code policy used by a return_error operation in a phase.
 */
export function getParamOverrideReturnErrorPolicy(phase: ParamOverridePhase): {
  defaultStatusCode: number
  minStatusCode: number
  maxStatusCode: number
  retryLocked: boolean
} {
  if (phase === 'response') {
    return {
      defaultStatusCode: 403,
      minStatusCode: 400,
      maxStatusCode: 599,
      retryLocked: true,
    }
  }
  return {
    defaultStatusCode: 400,
    minStatusCode: 100,
    maxStatusCode: 599,
    retryLocked: false,
  }
}

/**
 * Validates the operation restrictions shared by response and final-error
 * phases before the editor serializes a visual draft.
 */
export function getParamOverridePhaseValidationIssue(
  operations: ParamOverrideOperationDraft[]
): ParamOverridePhaseValidationIssue | null {
  for (let index = 0; index < operations.length; index++) {
    const operation = operations[index]
    if (operation.phase === 'request') continue

    if (operation.mode !== 'return_error') {
      return {
        kind: 'unsupported_mode',
        line: index + 1,
        phase: operation.phase,
      }
    }

    if (
      operation.conditions.length === 0 &&
      operations
        .slice(index + 1)
        .some((candidate) => candidate.phase === operation.phase)
    ) {
      return {
        kind: 'unconditional_not_last',
        line: index + 1,
        phase: operation.phase,
      }
    }
  }

  return null
}

/**
 * Finds incomplete conditions before they can be serialized as an
 * unconditional rule.
 */
export function getParamOverrideConditionValidationIssue(
  operations: ParamOverrideOperationDraft[]
): ParamOverrideConditionValidationIssue | null {
  for (
    let operationIndex = 0;
    operationIndex < operations.length;
    operationIndex++
  ) {
    const conditionIndex = operations[operationIndex].conditions.findIndex(
      (condition) => !condition.path.trim()
    )
    if (conditionIndex >= 0) {
      return {
        kind: 'missing_path',
        line: operationIndex + 1,
        condition: conditionIndex + 1,
      }
    }
  }

  return null
}

/**
 * Reports whether an operation still represents the untouched empty editor
 * row. An explicit condition source is user input even before a path is set.
 */
export function isParamOverrideOperationDraftBlank(
  operation: ParamOverrideOperationDraft
): boolean {
  return (
    operation.mode === 'set' &&
    !operation.path.trim() &&
    !operation.from.trim() &&
    !operation.to.trim() &&
    operation.value_text.trim() === '' &&
    !operation.keep_origin &&
    operation.conditions.length === 0
  )
}

/**
 * Parses visual text values using the same loose JSON convention as the
 * existing editor.
 */
export function parseParamOverrideLooseValue(valueText: string): unknown {
  const raw = String(valueText ?? '').trim()
  if (raw === '') return ''
  try {
    return JSON.parse(raw)
  } catch {
    return raw
  }
}

/**
 * Converts return_error editor text to one of the two wire types accepted by
 * the backend. Only JSON objects stay structured; every scalar and array is a
 * message string.
 */
export function parseParamOverrideReturnErrorValue(
  valueText: string
): string | Record<string, unknown> {
  const raw = String(valueText ?? '').trim()
  if (raw === '') return ''
  try {
    const parsed = JSON.parse(raw) as unknown
    if (isRecord(parsed)) return parsed
    if (typeof parsed === 'string') return parsed
  } catch {
    return raw
  }
  return raw
}

/**
 * Encodes a simple return_error message so JSON-looking text cannot later be
 * interpreted as a number, null, boolean, array, or object.
 */
export function serializeParamOverrideSimpleErrorMessage(
  valueText: string,
  fallbackMessage = ''
): string {
  const message = String(valueText ?? '').trim() || fallbackMessage
  return JSON.stringify(message)
}

/**
 * Serializes a visual condition while preserving explicit source selection.
 */
export function buildParamOverrideConditionPayload(
  condition: ParamOverrideConditionDraft
): Record<string, unknown> {
  const path = condition.path.trim()
  const payload: Record<string, unknown> = {
    path,
    mode: condition.mode || 'full',
    value: parseParamOverrideLooseValue(condition.value_text),
  }
  const source = serializeParamOverrideConditionSource(condition.source)
  if (source) payload.source = source
  if (condition.invert) payload.invert = true
  if (condition.pass_missing_key) payload.pass_missing_key = true
  return payload
}

/**
 * Builds the serializable input used to duplicate an operation without losing
 * its application phase or condition lookup sources.
 */
export function buildDuplicateParamOverrideOperation(
  operation: ParamOverrideOperationDraft
): Record<string, unknown> {
  return {
    phase: operation.phase,
    description: operation.description,
    path: operation.path,
    mode: operation.mode,
    value:
      operation.mode === 'return_error'
        ? parseParamOverrideReturnErrorValue(operation.value_text)
        : parseParamOverrideLooseValue(operation.value_text),
    keep_origin: operation.keep_origin,
    from: operation.from,
    to: operation.to,
    logic: operation.logic,
    conditions: operation.conditions.map(buildParamOverrideConditionPayload),
  }
}
