/*
Copyright (C) 2025 QuantumNous

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
];

export const PARAM_OVERRIDE_CONDITION_SOURCE_VALUES = [
  'auto',
  'body',
  'semantic',
  'context',
];

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
];

export const PARAM_OVERRIDE_CONDITION_MODE_VALUES = [
  'full',
  'prefix',
  'suffix',
  'contains',
  'gt',
  'gte',
  'lt',
  'lte',
];

const PHASE_VALUE_SET = new Set(PARAM_OVERRIDE_PHASE_VALUES);
const OPERATION_MODE_VALUE_SET = new Set(PARAM_OVERRIDE_OPERATION_MODE_VALUES);
const CONDITION_MODE_VALUE_SET = new Set(PARAM_OVERRIDE_CONDITION_MODE_VALUES);
const WIRE_CONDITION_SOURCE_VALUE_SET = new Set([
  'body',
  'semantic',
  'context',
]);
const TOP_LEVEL_FIELD_SET = new Set(['operations']);
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
]);
const CONDITION_FIELD_SET = new Set([
  'source',
  'path',
  'mode',
  'value',
  'invert',
  'pass_missing_key',
]);
const RETURN_ERROR_FIELD_SET = new Set([
  'message',
  'msg',
  'status_code',
  'status',
  'code',
  'type',
  'skip_retry',
]);

const isRecord = (value) =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

/**
 * 判断 operations 文档能否在可视化模式中无损编辑。
 */
export function getParamOverrideVisualCompatibilityIssue(value) {
  if (!Array.isArray(value.operations)) return 'invalid_operations';
  if (Object.keys(value).some((key) => !TOP_LEVEL_FIELD_SET.has(key))) {
    return 'unsupported_top_level_field';
  }

  for (const operation of value.operations) {
    if (!isRecord(operation)) return 'invalid_operations';
    if (Object.keys(operation).some((key) => !OPERATION_FIELD_SET.has(key))) {
      return 'unsupported_operation_field';
    }
    if (
      (operation.id !== undefined && typeof operation.id !== 'string') ||
      (operation.description !== undefined &&
        typeof operation.description !== 'string') ||
      (operation.path !== undefined && typeof operation.path !== 'string') ||
      (operation.from !== undefined && typeof operation.from !== 'string') ||
      (operation.to !== undefined && typeof operation.to !== 'string') ||
      (operation.keep_origin !== undefined &&
        typeof operation.keep_origin !== 'boolean')
    ) {
      return 'invalid_operation_field';
    }

    const phase = operation.phase ?? 'request';
    if (typeof phase !== 'string' || !PHASE_VALUE_SET.has(phase)) {
      return 'unsupported_phase';
    }

    if (
      typeof operation.mode !== 'string' ||
      !OPERATION_MODE_VALUE_SET.has(operation.mode)
    ) {
      return 'unsupported_operation_mode';
    }

    if (operation.logic !== undefined) {
      if (
        typeof operation.logic !== 'string' ||
        !['AND', 'OR'].includes(operation.logic.trim().toUpperCase())
      ) {
        return 'unsupported_condition_logic';
      }
    }

    if (
      operation.mode === 'return_error' &&
      isRecord(operation.value) &&
      Object.keys(operation.value).some(
        (key) => !RETURN_ERROR_FIELD_SET.has(key),
      )
    ) {
      return 'unsupported_return_error_field';
    }

    if (operation.conditions === undefined) continue;
    if (!Array.isArray(operation.conditions)) return 'invalid_conditions';

    for (const condition of operation.conditions) {
      if (!isRecord(condition)) return 'invalid_conditions';
      if (Object.keys(condition).some((key) => !CONDITION_FIELD_SET.has(key))) {
        return 'unsupported_condition_field';
      }
      if (
        (condition.path !== undefined && typeof condition.path !== 'string') ||
        (condition.invert !== undefined &&
          typeof condition.invert !== 'boolean') ||
        (condition.pass_missing_key !== undefined &&
          typeof condition.pass_missing_key !== 'boolean')
      ) {
        return 'invalid_condition_field';
      }

      const mode = condition.mode ?? 'full';
      if (typeof mode !== 'string' || !CONDITION_MODE_VALUE_SET.has(mode)) {
        return 'unsupported_condition_mode';
      }

      if (
        condition.source !== undefined &&
        (typeof condition.source !== 'string' ||
          !WIRE_CONDITION_SOURCE_VALUE_SET.has(condition.source))
      ) {
        return 'unsupported_condition_source';
      }
      if (condition.source === 'semantic' && phase !== 'response') {
        return 'semantic_source_requires_response';
      }
    }
  }

  return null;
}

/**
 * 解析编辑器输入，并为不认识的 schema 保留原始文本。
 */
export function classifyParamOverrideEditorDocument(rawValue) {
  const sourceText = typeof rawValue === 'string' ? rawValue : '';
  const trimmed = sourceText.trim();
  if (!trimmed) return { kind: 'empty', sourceText };

  let parsed;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return { kind: 'invalid', sourceText };
  }

  if (!isRecord(parsed)) return { kind: 'invalid', sourceText };
  if (!Object.hasOwn(parsed, 'operations')) {
    return { kind: 'legacy', sourceText, parsed };
  }

  const issue = getParamOverrideVisualCompatibilityIssue(parsed);
  if (issue) return { kind: 'unsupported', sourceText, parsed, issue };
  return { kind: 'operations', sourceText, parsed };
}

/**
 * 将 wire 中省略的 source 显式映射为编辑器的自动模式。
 */
export function normalizeParamOverrideConditionSource(source) {
  if (source === undefined) return 'auto';
  if (source === 'body' || source === 'semantic' || source === 'context') {
    return source;
  }
  throw new Error(`unsupported parameter override condition source: ${source}`);
}

/**
 * 自动模式沿用历史查找顺序，因此序列化时省略 source。
 */
export function serializeParamOverrideConditionSource(source) {
  return source === 'auto' ? undefined : source;
}

/**
 * request 是向后兼容的默认阶段，因此序列化时省略 phase。
 */
export function serializeParamOverridePhase(phase) {
  return phase === 'request' ? undefined : phase;
}

/**
 * 返回各阶段 return_error 的状态码与重试策略。
 */
export function getParamOverrideReturnErrorPolicy(phase) {
  if (phase === 'response') {
    return {
      defaultStatusCode: 403,
      minStatusCode: 400,
      maxStatusCode: 599,
      retryLocked: true,
    };
  }
  return {
    defaultStatusCode: 400,
    minStatusCode: 100,
    maxStatusCode: 599,
    retryLocked: false,
  };
}

/**
 * 校验响应后阶段的操作白名单与同阶段无条件规则顺序。
 */
export function getParamOverridePhaseValidationIssue(operations) {
  for (let index = 0; index < operations.length; index++) {
    const operation = operations[index];
    if (operation.phase === 'request') continue;

    if (operation.mode !== 'return_error') {
      return {
        kind: 'unsupported_mode',
        line: index + 1,
        phase: operation.phase,
      };
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
      };
    }
  }

  return null;
}

/**
 * 校验可视化草稿中的条件路径，避免未完成条件在序列化时被静默丢弃。
 */
export function getParamOverrideConditionValidationIssue(operations) {
  for (
    let operationIndex = 0;
    operationIndex < operations.length;
    operationIndex++
  ) {
    const conditions = operations[operationIndex].conditions || [];
    for (
      let conditionIndex = 0;
      conditionIndex < conditions.length;
      conditionIndex++
    ) {
      if (!String(conditions[conditionIndex].path || '').trim()) {
        return {
          kind: 'missing_condition_path',
          line: operationIndex + 1,
          condition: conditionIndex + 1,
        };
      }
    }
  }

  return null;
}

/**
 * 判断操作草稿是否完全空白；只要已添加条件，就必须进入保存校验。
 */
export function isParamOverrideOperationBlank(operation) {
  return (
    operation.mode === 'set' &&
    !String(operation.path || '').trim() &&
    !String(operation.from || '').trim() &&
    !String(operation.to || '').trim() &&
    !String(operation.value_text ?? '').trim() &&
    operation.keep_origin !== true &&
    (operation.conditions || []).length === 0
  );
}

/**
 * 按编辑器历史约定解析 JSON 值或普通文本。
 */
export function parseParamOverrideLooseValue(valueText) {
  const raw = String(valueText ?? '').trim();
  if (raw === '') return '';
  try {
    return JSON.parse(raw);
  } catch {
    return raw;
  }
}

/**
 * 将简单错误消息编码为 JSON 字符串，防止数字、布尔值等被宽松解析为非字符串。
 */
export function buildParamOverrideSimpleReturnErrorValueText(
  valueText,
  fallbackMessage = 'Request rejected',
) {
  const message =
    String(valueText ?? '').trim() || String(fallbackMessage ?? '').trim();
  return message ? JSON.stringify(message) : '';
}

/**
 * 序列化 return_error 值；仅普通对象保留结构，其余输入均按消息字符串处理。
 */
export function parseParamOverrideReturnErrorValue(valueText) {
  const raw = String(valueText ?? '').trim();
  if (!raw) return '';

  try {
    const parsed = JSON.parse(raw);
    if (isRecord(parsed)) return parsed;
    if (typeof parsed === 'string') return parsed;
  } catch {
    // 普通文本直接作为错误消息。
  }

  return raw;
}

/**
 * 序列化条件，并保留显式选择的数据来源。
 */
export function buildParamOverrideConditionPayload(condition) {
  const path = condition.path.trim();
  const payload = {
    path,
    mode: condition.mode || 'full',
    value: parseParamOverrideLooseValue(condition.value_text),
  };
  const source = serializeParamOverrideConditionSource(condition.source);
  if (source) payload.source = source;
  if (condition.invert) payload.invert = true;
  if (condition.pass_missing_key) payload.pass_missing_key = true;
  return payload;
}

/**
 * 构造规则复制输入，避免丢失 phase 与条件 source。
 */
export function buildDuplicateParamOverrideOperation(operation) {
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
  };
}
