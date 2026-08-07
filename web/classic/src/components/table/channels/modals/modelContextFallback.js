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

export const DEFAULT_CONTEXT_FALLBACK_THRESHOLD = 90;
export const MAX_CONTEXT_FALLBACK_RULES = 256;

const KNOWN_FIELDS = new Set([
  'source_context_window_tokens',
  'threshold_percent',
  'fallback_model',
  'fallback_context_window_tokens',
  'route_mode',
  'target_channel_ids',
]);

const isRecord = (value) =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const isPositiveSafeInteger = (value) =>
  Number.isSafeInteger(value) && value > 0;

/**
 * 校验后端使用的规则对象结构。
 */
export function validateModelContextFallbacks(rules) {
  if (!isRecord(rules)) return '上下文兜底规则必须是 JSON 对象';
  const entries = Object.entries(rules);
  if (entries.length > MAX_CONTEXT_FALLBACK_RULES) {
    return '上下文兜底规则最多配置 256 项';
  }
  for (const [sourceModel, rule] of entries) {
    if (
      !sourceModel.trim() ||
      sourceModel !== sourceModel.trim() ||
      sourceModel.length > 255 ||
      !isRecord(rule)
    ) {
      return '源模型配置无效';
    }
    if (!isPositiveSafeInteger(rule.source_context_window_tokens)) {
      return '源模型上下文窗口必须是正整数';
    }
    if (
      rule.threshold_percent !== undefined &&
      (!Number.isInteger(rule.threshold_percent) ||
        rule.threshold_percent < 1 ||
        rule.threshold_percent > 100)
    ) {
      return '触发阈值必须在 1 到 100 之间';
    }
    if (
      typeof rule.fallback_model !== 'string' ||
      !rule.fallback_model.trim() ||
      rule.fallback_model !== rule.fallback_model.trim() ||
      rule.fallback_model.length > 255 ||
      rule.fallback_model === sourceModel
    ) {
      return '兜底模型配置无效';
    }
    if (!isPositiveSafeInteger(rule.fallback_context_window_tokens)) {
      return '兜底模型上下文窗口必须是正整数';
    }
    if (!['same_channel', 'cross_channel'].includes(rule.route_mode)) {
      return '路由模式配置无效';
    }
    const targetIds = rule.target_channel_ids || [];
    if (
      !Array.isArray(targetIds) ||
      targetIds.some((id) => !isPositiveSafeInteger(id)) ||
      new Set(targetIds).size !== targetIds.length ||
      (rule.route_mode === 'same_channel' && targetIds.length > 0)
    ) {
      return '目标渠道 ID 配置无效';
    }
  }
  return '';
}

/**
 * 将已有 JSON 解析成可视化编辑草稿。
 */
export function parseModelContextFallbacks(value) {
  if (!value?.trim()) return { rules: [], error: '' };
  let parsed;
  try {
    parsed = JSON.parse(value);
  } catch {
    return { rules: [], error: '上下文兜底规则必须是有效的 JSON' };
  }
  const error = validateModelContextFallbacks(parsed);
  if (error) return { rules: [], error };

  return {
    error: '',
    rules: Object.entries(parsed).map(([sourceModel, rule], index) => {
      const targetIds = rule.target_channel_ids || [];
      return {
        id: `context-fallback-${index + 1}`,
        sourceModel,
        sourceContextWindowTokens: String(rule.source_context_window_tokens),
        thresholdPercent: String(
          rule.threshold_percent ?? DEFAULT_CONTEXT_FALLBACK_THRESHOLD,
        ),
        fallbackModel: rule.fallback_model,
        fallbackContextWindowTokens: String(
          rule.fallback_context_window_tokens,
        ),
        routeMode: rule.route_mode,
        targetMode: targetIds.length > 0 ? 'limited' : 'auto',
        targetChannelIds: [...targetIds],
        extra: Object.fromEntries(
          Object.entries(rule).filter(([key]) => !KNOWN_FIELDS.has(key)),
        ),
      };
    }),
  };
}

/**
 * 校验可视化编辑草稿。
 */
export function validateModelContextFallbackDrafts(rules) {
  if (rules.length > MAX_CONTEXT_FALLBACK_RULES) {
    return [{ message: '上下文兜底规则最多配置 256 项' }];
  }
  const errors = [];
  const seen = new Set();
  for (const rule of rules) {
    if (
      !rule.sourceModel.trim() ||
      rule.sourceModel !== rule.sourceModel.trim() ||
      rule.sourceModel.length > 255
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'sourceModel',
        message: '请输入有效且首尾无空格的源模型名称',
      });
    } else if (seen.has(rule.sourceModel)) {
      errors.push({
        ruleId: rule.id,
        field: 'sourceModel',
        message: '源模型规则不可重复',
      });
    } else {
      seen.add(rule.sourceModel);
    }

    const sourceWindow = Number(rule.sourceContextWindowTokens);
    if (
      !/^\d+$/.test(rule.sourceContextWindowTokens) ||
      !isPositiveSafeInteger(sourceWindow)
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'sourceContextWindowTokens',
        message: '源模型上下文窗口必须是正整数',
      });
    }
    const threshold = Number(rule.thresholdPercent);
    if (
      !/^\d+$/.test(rule.thresholdPercent) ||
      !Number.isInteger(threshold) ||
      threshold < 1 ||
      threshold > 100
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'thresholdPercent',
        message: '触发阈值必须在 1 到 100 之间',
      });
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
        message: '兜底模型必须有效且与源模型不同',
      });
    }
    const fallbackWindow = Number(rule.fallbackContextWindowTokens);
    if (
      !/^\d+$/.test(rule.fallbackContextWindowTokens) ||
      !isPositiveSafeInteger(fallbackWindow)
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'fallbackContextWindowTokens',
        message: '兜底模型上下文窗口必须是正整数',
      });
    }
    if (
      rule.targetChannelIds.some((id) => !isPositiveSafeInteger(id)) ||
      new Set(rule.targetChannelIds).size !== rule.targetChannelIds.length
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'targetChannelIds',
        message: '目标渠道 ID 必须是互不重复的正整数',
      });
    }
    if (
      rule.routeMode === 'cross_channel' &&
      rule.targetMode === 'limited' &&
      rule.targetChannelIds.length === 0
    ) {
      errors.push({
        ruleId: rule.id,
        field: 'targetChannelIds',
        message: '请至少选择一个目标渠道',
      });
    }
  }
  return errors;
}

/**
 * 将有效草稿序列化成后端现有结构。
 */
export function serializeModelContextFallbacks(rules) {
  const errors = validateModelContextFallbackDrafts(rules);
  if (errors.length > 0) throw new Error(errors[0].message);
  const result = {};
  for (const rule of rules) {
    const serialized = {
      ...rule.extra,
      source_context_window_tokens: Number(rule.sourceContextWindowTokens),
      threshold_percent: Number(rule.thresholdPercent),
      fallback_model: rule.fallbackModel,
      fallback_context_window_tokens: Number(rule.fallbackContextWindowTokens),
      route_mode: rule.routeMode,
    };
    if (rule.routeMode === 'cross_channel' && rule.targetMode === 'limited') {
      serialized.target_channel_ids = [...rule.targetChannelIds];
    }
    result[rule.sourceModel] = serialized;
  }
  return rules.length === 0 ? '' : JSON.stringify(result, null, 2);
}

/**
 * 按后端整数算法计算触发 Token。
 */
export function calculateContextFallbackTriggerTokens(
  sourceValue,
  percentValue,
) {
  const source = Number(sourceValue);
  const percent = Number(percentValue);
  if (!isPositiveSafeInteger(source) || !Number.isInteger(percent)) return null;
  return (
    Math.floor(source / 100) * percent +
    Math.floor(((source % 100) * percent) / 100)
  );
}

/**
 * 创建一条新规则的默认草稿。
 */
export function createEmptyModelContextFallback(id) {
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
  };
}
