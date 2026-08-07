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
import assert from 'node:assert/strict';
import test from 'node:test';

import {
  calculateContextFallbackTriggerTokens,
  parseModelContextFallbacks,
  serializeModelContextFallbacks,
  validateModelContextFallbackDrafts,
} from './modelContextFallback.js';

const validRule = {
  source_context_window_tokens: 262144,
  fallback_model: 'fallback-model',
  fallback_context_window_tokens: 1048576,
  route_mode: 'cross_channel',
  target_channel_ids: [8, 3],
  future_field: true,
};

test('上下文兜底规则可视化草稿保持现有后端契约', () => {
  const parsed = parseModelContextFallbacks(
    JSON.stringify({ 'source-model': validRule }),
  );
  assert.equal(parsed.error, '');
  assert.equal(parsed.rules[0].thresholdPercent, '90');
  assert.deepEqual(parsed.rules[0].targetChannelIds, [8, 3]);
  assert.equal(calculateContextFallbackTriggerTokens('262144', '90'), 235929);

  const serialized = JSON.parse(serializeModelContextFallbacks(parsed.rules));
  assert.deepEqual(serialized['source-model'].target_channel_ids, [8, 3]);
  assert.equal(serialized['source-model'].future_field, true);
});

test('自动路由省略目标 ID，重复源模型与空限定渠道会报错', () => {
  const parsed = parseModelContextFallbacks(
    JSON.stringify({
      'source-model': { ...validRule, target_channel_ids: [] },
    }),
  );
  const serialized = JSON.parse(serializeModelContextFallbacks(parsed.rules));
  assert.equal(serialized['source-model'].target_channel_ids, undefined);

  const duplicate = {
    ...parsed.rules[0],
    id: 'duplicate',
    targetMode: 'limited',
    targetChannelIds: [],
  };
  const errors = validateModelContextFallbackDrafts([
    parsed.rules[0],
    duplicate,
  ]);
  assert.ok(errors.some((error) => error.message === '源模型规则不可重复'));
  assert.ok(errors.some((error) => error.message === '请至少选择一个目标渠道'));
  assert.equal(
    parseModelContextFallbacks('{').error,
    '上下文兜底规则必须是有效的 JSON',
  );
});
