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
import { describe, test } from 'node:test';

import {
  buildDuplicateParamOverrideOperation,
  buildParamOverrideConditionPayload,
  buildParamOverrideSimpleReturnErrorValueText,
  classifyParamOverrideEditorDocument,
  getParamOverrideConditionValidationIssue,
  getParamOverridePhaseValidationIssue,
  getParamOverrideReturnErrorPolicy,
  isParamOverrideOperationBlank,
  normalizeParamOverrideConditionSource,
  parseParamOverrideReturnErrorValue,
  serializeParamOverrideConditionSource,
  serializeParamOverridePhase,
} from './paramOverrideEditor.js';

describe('参数覆盖可视化兼容性', () => {
  test('识别三个阶段与所有正式条件来源', () => {
    const document = classifyParamOverrideEditorDocument(
      JSON.stringify({
        operations: [
          { mode: 'set', path: 'temperature', value: 0.7 },
          {
            phase: 'response',
            mode: 'return_error',
            value: 'rejected',
            conditions: [
              {
                source: 'body',
                path: 'choices.0.finish_reason',
                mode: 'full',
                value: 'content_filter',
              },
              {
                source: 'semantic',
                path: 'response.rejection_state',
                mode: 'full',
                value: 'rejected',
              },
              {
                source: 'context',
                path: 'upstream.http_status',
                mode: 'gte',
                value: 400,
              },
            ],
          },
          { phase: 'final_error', mode: 'return_error', value: 'failed' },
        ],
      }),
    );

    assert.equal(document.kind, 'operations');
    assert.equal(document.issue, undefined);
  });

  const unknownSchemaCases = [
    ['phase', { phase: 'future_response', mode: 'return_error' }],
    ['operation mode', { mode: 'future_transform' }],
    [
      'condition mode',
      {
        mode: 'set',
        conditions: [{ path: 'model', mode: 'future_match', value: 'gpt' }],
      },
    ],
    [
      'condition source',
      {
        mode: 'set',
        conditions: [
          {
            source: 'future_source',
            path: 'model',
            mode: 'full',
            value: 'gpt',
          },
        ],
      },
    ],
  ];

  for (const [label, operation] of unknownSchemaCases) {
    test(`未知 ${label} 保留原始 JSON`, () => {
      const sourceText = `  ${JSON.stringify({ operations: [operation] })}\n`;
      const document = classifyParamOverrideEditorDocument(sourceText);

      assert.equal(document.kind, 'unsupported');
      assert.equal(document.sourceText, sourceText);
    });
  }

  const unknownFieldCases = [
    ['顶层', { operations: [], schema_version: 2 }],
    [
      '操作',
      { operations: [{ mode: 'set', path: 'x', value: 1, retryable: true }] },
    ],
    [
      '条件',
      {
        operations: [
          {
            mode: 'set',
            path: 'x',
            value: 1,
            conditions: [
              { path: 'model', mode: 'full', value: 'gpt', future: true },
            ],
          },
        ],
      },
    ],
    [
      'return_error 值',
      {
        operations: [
          {
            phase: 'response',
            mode: 'return_error',
            value: { message: 'blocked', response_format: 'future' },
          },
        ],
      },
    ],
  ];

  for (const [label, documentValue] of unknownFieldCases) {
    test(`未知${label}字段保留原始 JSON`, () => {
      const sourceText = `\n${JSON.stringify(documentValue)}\n`;
      const document = classifyParamOverrideEditorDocument(sourceText);

      assert.equal(document.kind, 'unsupported');
      assert.equal(document.sourceText, sourceText);
    });
  }

  test('非 response 阶段的 semantic 条件进入 Raw JSON', () => {
    const document = classifyParamOverrideEditorDocument(
      JSON.stringify({
        operations: [
          {
            phase: 'final_error',
            mode: 'return_error',
            value: 'failed',
            conditions: [
              {
                source: 'semantic',
                path: 'response.primary_outcome',
                mode: 'full',
                value: 'failed',
              },
            ],
          },
        ],
      }),
    );

    assert.equal(document.kind, 'unsupported');
    assert.equal(document.issue, 'semantic_source_requires_response');
  });
});

describe('参数覆盖条件来源', () => {
  test('auto 使用历史省略格式，显式来源正常序列化', () => {
    assert.equal(normalizeParamOverrideConditionSource(undefined), 'auto');
    assert.equal(serializeParamOverrideConditionSource('auto'), undefined);
    assert.equal(serializeParamOverrideConditionSource('semantic'), 'semantic');
    assert.equal(serializeParamOverridePhase('request'), undefined);
    assert.equal(serializeParamOverridePhase('response'), 'response');

    const condition = {
      source: 'body',
      path: 'choices.0.finish_reason',
      mode: 'full',
      value_text: 'content_filter',
      invert: false,
      pass_missing_key: false,
    };
    assert.deepEqual(buildParamOverrideConditionPayload(condition), {
      source: 'body',
      path: 'choices.0.finish_reason',
      mode: 'full',
      value: 'content_filter',
    });
    assert.equal(
      buildParamOverrideConditionPayload({ ...condition, source: 'auto' })
        .source,
      undefined,
    );
  });

  test('复制规则保留 phase 与 source', () => {
    const duplicate = buildDuplicateParamOverrideOperation({
      phase: 'response',
      description: 'map rejected response',
      path: '',
      mode: 'return_error',
      from: '',
      to: '',
      value_text: '{"message":"rejected"}',
      keep_origin: false,
      logic: 'AND',
      conditions: [
        {
          source: 'semantic',
          path: 'response.rejection_state',
          mode: 'full',
          value_text: 'rejected',
          invert: false,
          pass_missing_key: false,
        },
      ],
    });

    assert.equal(duplicate.phase, 'response');
    assert.equal(duplicate.conditions[0].source, 'semantic');
  });

  test('空路径条件保留草稿并返回明确校验错误', () => {
    const condition = {
      source: 'semantic',
      path: '',
      mode: 'full',
      value_text: 'all',
      invert: false,
      pass_missing_key: false,
    };

    assert.deepEqual(buildParamOverrideConditionPayload(condition), {
      source: 'semantic',
      path: '',
      mode: 'full',
      value: 'all',
    });
    assert.deepEqual(
      getParamOverrideConditionValidationIssue([
        {
          phase: 'response',
          mode: 'return_error',
          conditions: [condition],
        },
      ]),
      { kind: 'missing_condition_path', line: 1, condition: 1 },
    );
  });

  test('仅修改条件来源的未完成规则不会被当作空白规则', () => {
    const operation = {
      phase: 'request',
      mode: 'set',
      path: '',
      from: '',
      to: '',
      value_text: '',
      keep_origin: false,
      conditions: [
        {
          source: 'body',
          path: '',
          mode: 'full',
          value_text: '',
          invert: false,
          pass_missing_key: false,
        },
      ],
    };

    assert.equal(isParamOverrideOperationBlank(operation), false);
    assert.equal(
      getParamOverrideConditionValidationIssue([operation]).kind,
      'missing_condition_path',
    );
  });
});

describe('响应后阶段约束', () => {
  const createOperation = (phase, mode, conditionCount = 1) => ({
    phase,
    mode,
    conditions: Array.from({ length: conditionCount }, () => ({})),
  });

  test('仅 return_error 可用于 response 与 final_error', () => {
    assert.equal(
      getParamOverridePhaseValidationIssue([
        createOperation('request', 'set'),
        createOperation('response', 'return_error'),
        createOperation('final_error', 'return_error'),
      ]),
      null,
    );
    assert.deepEqual(
      getParamOverridePhaseValidationIssue([
        createOperation('response', 'set'),
      ]),
      { kind: 'unsupported_mode', line: 1, phase: 'response' },
    );
  });

  test('无条件规则必须是相同阶段最后一条', () => {
    assert.deepEqual(
      getParamOverridePhaseValidationIssue([
        createOperation('response', 'return_error', 0),
        createOperation('request', 'set'),
        createOperation('response', 'return_error'),
      ]),
      { kind: 'unconditional_not_last', line: 1, phase: 'response' },
    );
  });

  test('响应阶段使用独立状态码范围并锁定重试', () => {
    assert.deepEqual(getParamOverrideReturnErrorPolicy('response'), {
      defaultStatusCode: 403,
      minStatusCode: 400,
      maxStatusCode: 599,
      retryLocked: true,
    });
    assert.deepEqual(getParamOverrideReturnErrorPolicy('request'), {
      defaultStatusCode: 400,
      minStatusCode: 100,
      maxStatusCode: 599,
      retryLocked: false,
    });
    assert.deepEqual(getParamOverrideReturnErrorPolicy('final_error'), {
      defaultStatusCode: 400,
      minStatusCode: 100,
      maxStatusCode: 599,
      retryLocked: false,
    });
  });

  test('简单错误消息始终序列化为字符串', () => {
    for (const value of [
      '0.7',
      'null',
      'true',
      '["blocked"]',
      '{"message":"literal text"}',
    ]) {
      const valueText = buildParamOverrideSimpleReturnErrorValueText(value);
      const serialized = parseParamOverrideReturnErrorValue(valueText);

      assert.equal(typeof serialized, 'string');
      assert.equal(serialized, value);
    }

    assert.equal(
      JSON.parse(buildParamOverrideSimpleReturnErrorValueText('')),
      'Request rejected',
    );
    assert.equal(buildParamOverrideSimpleReturnErrorValueText('', ''), '');
    assert.deepEqual(
      parseParamOverrideReturnErrorValue(
        '{"message":"blocked","status_code":403}',
      ),
      { message: 'blocked', status_code: 403 },
    );
  });
});
