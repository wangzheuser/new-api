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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  buildDuplicateParamOverrideOperation,
  buildParamOverrideConditionPayload,
  classifyParamOverrideEditorDocument,
  getParamOverrideConditionValidationIssue,
  getParamOverridePhaseValidationIssue,
  getParamOverrideReturnErrorPolicy,
  isParamOverrideOperationDraftBlank,
  normalizeParamOverrideConditionSource,
  parseParamOverrideReturnErrorValue,
  serializeParamOverrideSimpleErrorMessage,
  serializeParamOverrideConditionSource,
  serializeParamOverridePhase,
  type ParamOverrideOperationDraft,
} from './param-override-editor'

describe('parameter override visual compatibility', () => {
  test('accepts request, response, and final error phases with known sources', () => {
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
                path: 'status',
                mode: 'full',
                value: 'blocked',
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
      })
    )

    assert.equal(document.kind, 'operations')
    assert.equal(document.issue, undefined)
  })

  const unknownSchemaCases: Array<[string, Record<string, unknown>]> = [
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
  ]

  for (const [label, operation] of unknownSchemaCases) {
    test(`keeps an unknown ${label} document in Raw JSON`, () => {
      const sourceText = `  ${JSON.stringify({ operations: [operation] })}\n`
      const document = classifyParamOverrideEditorDocument(sourceText)

      assert.equal(document.kind, 'unsupported')
      assert.equal(document.sourceText, sourceText)
    })
  }

  test('keeps malformed operation structures in Raw JSON', () => {
    const document = classifyParamOverrideEditorDocument(
      JSON.stringify({ operations: [{ mode: 'set', conditions: {} }] })
    )

    assert.equal(document.kind, 'unsupported')
    assert.equal(document.issue, 'invalid_conditions')
  })

  const unknownFieldCases: Array<[string, Record<string, unknown>]> = [
    ['top-level', { operations: [], schema_version: 2 }],
    [
      'operation',
      { operations: [{ mode: 'set', path: 'x', value: 1, retryable: true }] },
    ],
    [
      'condition',
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
      'return_error value',
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
  ]

  for (const [label, documentValue] of unknownFieldCases) {
    test(`keeps an unknown ${label} field in Raw JSON`, () => {
      const sourceText = `\n${JSON.stringify(documentValue)}\n`
      const document = classifyParamOverrideEditorDocument(sourceText)

      assert.equal(document.kind, 'unsupported')
      assert.equal(document.sourceText, sourceText)
    })
  }

  test('keeps semantic conditions outside response in Raw JSON', () => {
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
      })
    )

    assert.equal(document.kind, 'unsupported')
    assert.equal(document.issue, 'semantic_source_requires_response')
  })
})

describe('parameter override condition sources', () => {
  test('uses Auto for omitted sources and omits Auto when serializing', () => {
    assert.equal(normalizeParamOverrideConditionSource(undefined), 'auto')
    assert.equal(serializeParamOverrideConditionSource('auto'), undefined)
    assert.equal(serializeParamOverrideConditionSource('semantic'), 'semantic')
    assert.equal(serializeParamOverridePhase('request'), undefined)
    assert.equal(serializeParamOverridePhase('response'), 'response')
  })

  test('serializes explicit sources and preserves the legacy Auto omission', () => {
    const condition = {
      id: 'condition-1',
      source: 'body' as const,
      path: 'choices.0.finish_reason',
      mode: 'full',
      value_text: 'content_filter',
      invert: false,
      pass_missing_key: false,
    }

    assert.deepEqual(buildParamOverrideConditionPayload(condition), {
      source: 'body',
      path: 'choices.0.finish_reason',
      mode: 'full',
      value: 'content_filter',
    })
    assert.deepEqual(
      buildParamOverrideConditionPayload({ ...condition, source: 'auto' }),
      {
        path: 'choices.0.finish_reason',
        mode: 'full',
        value: 'content_filter',
      }
    )
  })

  test('duplicates phase and explicit condition sources without emitting Auto', () => {
    const operation: ParamOverrideOperationDraft = {
      id: 'op-1',
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
          id: 'condition-1',
          source: 'semantic',
          path: 'response.rejection_state',
          mode: 'full',
          value_text: 'rejected',
          invert: false,
          pass_missing_key: false,
        },
        {
          id: 'condition-2',
          source: 'auto',
          path: 'model',
          mode: 'prefix',
          value_text: 'gpt-',
          invert: false,
          pass_missing_key: false,
        },
      ],
    }

    const duplicate = buildDuplicateParamOverrideOperation(operation)
    const conditions = duplicate.conditions as Record<string, unknown>[]

    assert.equal(duplicate.phase, 'response')
    assert.equal(conditions[0]?.source, 'semantic')
    assert.equal(conditions[1]?.source, undefined)
  })

  test('retains an incomplete condition so save validation cannot turn it into a catch-all', () => {
    const operation: ParamOverrideOperationDraft = {
      id: 'response-rule',
      phase: 'response',
      description: '',
      path: '',
      mode: 'return_error',
      from: '',
      to: '',
      value_text: 'rejected',
      keep_origin: false,
      logic: 'OR',
      conditions: [
        {
          id: 'unfinished-condition',
          source: 'semantic',
          path: '',
          mode: 'full',
          value_text: 'all',
          invert: false,
          pass_missing_key: false,
        },
      ],
    }

    assert.equal(isParamOverrideOperationDraftBlank(operation), false)
    assert.deepEqual(getParamOverrideConditionValidationIssue([operation]), {
      kind: 'missing_path',
      line: 1,
      condition: 1,
    })
    assert.deepEqual(
      buildParamOverrideConditionPayload(operation.conditions[0]),
      {
        source: 'semantic',
        path: '',
        mode: 'full',
        value: 'all',
      }
    )
  })

  test('treats every added condition as edited even when only defaults are present', () => {
    const operation: ParamOverrideOperationDraft = {
      id: 'blank-rule',
      phase: 'request',
      description: '',
      path: '',
      mode: 'set',
      from: '',
      to: '',
      value_text: '',
      keep_origin: false,
      logic: 'OR',
      conditions: [
        {
          id: 'blank-condition',
          source: 'auto',
          path: '',
          mode: 'full',
          value_text: '',
          invert: false,
          pass_missing_key: false,
        },
      ],
    }

    assert.equal(isParamOverrideOperationDraftBlank(operation), false)
    assert.deepEqual(getParamOverrideConditionValidationIssue([operation]), {
      kind: 'missing_path',
      line: 1,
      condition: 1,
    })
  })
})

describe('parameter override response phase restrictions', () => {
  const createOperation = (
    phase: ParamOverrideOperationDraft['phase'],
    mode: string,
    conditionCount = 1
  ): ParamOverrideOperationDraft => ({
    id: `${phase}-${mode}`,
    phase,
    description: '',
    path: '',
    mode,
    from: '',
    to: '',
    value_text: 'message',
    keep_origin: false,
    logic: 'OR',
    conditions: Array.from({ length: conditionCount }, (_, index) => ({
      id: `condition-${index}`,
      source: 'auto',
      path: 'model',
      mode: 'full',
      value_text: 'gpt',
      invert: false,
      pass_missing_key: false,
    })),
  })

  test('allows every request mode but only return_error after an upstream call', () => {
    assert.equal(
      getParamOverridePhaseValidationIssue([
        createOperation('request', 'set'),
        createOperation('response', 'return_error'),
        createOperation('final_error', 'return_error'),
      ]),
      null
    )
    assert.deepEqual(
      getParamOverridePhaseValidationIssue([
        createOperation('response', 'set'),
      ]),
      { kind: 'unsupported_mode', line: 1, phase: 'response' }
    )
  })

  test('requires an unconditional rule to be last within the same phase', () => {
    assert.deepEqual(
      getParamOverridePhaseValidationIssue([
        createOperation('response', 'return_error', 0),
        createOperation('request', 'set'),
        createOperation('response', 'return_error'),
      ]),
      { kind: 'unconditional_not_last', line: 1, phase: 'response' }
    )
    assert.equal(
      getParamOverridePhaseValidationIssue([
        createOperation('response', 'return_error', 0),
        createOperation('final_error', 'return_error'),
      ]),
      null
    )
  })

  test('uses response-specific return_error defaults and ranges', () => {
    assert.deepEqual(getParamOverrideReturnErrorPolicy('response'), {
      defaultStatusCode: 403,
      minStatusCode: 400,
      maxStatusCode: 599,
      retryLocked: true,
    })
    assert.deepEqual(getParamOverrideReturnErrorPolicy('request'), {
      defaultStatusCode: 400,
      minStatusCode: 100,
      maxStatusCode: 599,
      retryLocked: false,
    })
    assert.deepEqual(getParamOverrideReturnErrorPolicy('final_error'), {
      defaultStatusCode: 400,
      minStatusCode: 100,
      maxStatusCode: 599,
      retryLocked: false,
    })
  })
})

describe('parameter override return_error values', () => {
  test('keeps objects structured and coerces unsupported JSON types to messages', () => {
    assert.deepEqual(parseParamOverrideReturnErrorValue('{"message":"no"}'), {
      message: 'no',
    })

    const scalarCases = ['0.7', 'null', 'true', '["blocked"]']
    for (const source of scalarCases) {
      assert.equal(parseParamOverrideReturnErrorValue(source), source)
    }
    assert.equal(parseParamOverrideReturnErrorValue('"blocked"'), 'blocked')
    assert.equal(
      parseParamOverrideReturnErrorValue('"{\\"message\\":\\"blocked\\"}"'),
      '{"message":"blocked"}'
    )
  })

  test('encodes ordinary operation values as simple string messages for non-request phases', () => {
    const cases = [
      ['0.7', '0.7'],
      ['null', 'null'],
      ['true', 'true'],
      ['["blocked"]', '["blocked"]'],
      ['{"message":"blocked"}', '{"message":"blocked"}'],
    ] as const

    for (const phase of ['response', 'final_error'] as const) {
      for (const [source, expectedMessage] of cases) {
        const valueText = serializeParamOverrideSimpleErrorMessage(source)
        assert.equal(JSON.parse(valueText), expectedMessage, phase)
        assert.equal(
          parseParamOverrideReturnErrorValue(valueText),
          expectedMessage,
          phase
        )
      }
    }
    assert.equal(serializeParamOverrideSimpleErrorMessage(''), '""')
    assert.equal(
      serializeParamOverrideSimpleErrorMessage('', 'Request rejected'),
      '"Request rejected"'
    )
  })

  test('duplicates legacy scalar return_error values as valid string messages', () => {
    const operation: ParamOverrideOperationDraft = {
      id: 'legacy-scalar',
      phase: 'response',
      description: '',
      path: '',
      mode: 'return_error',
      from: '',
      to: '',
      value_text: '0.7',
      keep_origin: false,
      logic: 'OR',
      conditions: [],
    }

    assert.equal(buildDuplicateParamOverrideOperation(operation).value, '0.7')
  })
})
