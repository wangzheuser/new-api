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

import type { Channel } from '../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
  transformFormDataToNativeProbeDraft,
  transformFormDataToUpdatePayload,
} from './channel-form'

/**
 * Build the minimum complete channel fixture required by the form transformer.
 */
function createChannel(
  setting: Record<string, unknown>,
  models = 'gpt-4o-mini'
): Channel {
  return {
    id: 1,
    type: 1,
    key: '',
    openai_organization: null,
    test_model: null,
    status: 1,
    name: 'test channel',
    weight: 0,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    base_url: null,
    other: '',
    balance: 0,
    balance_updated_time: 0,
    models,
    group: 'default',
    used_quota: 0,
    model_mapping: null,
    status_code_mapping: null,
    priority: 0,
    auto_ban: 1,
    other_info: '',
    tag: null,
    setting: JSON.stringify(setting),
    param_override: null,
    header_override: null,
    remark: '',
    max_input_tokens: 0,
    channel_info: {
      is_multi_key: false,
      multi_key_size: 0,
      multi_key_polling_index: 0,
      multi_key_mode: 'random',
    },
    settings: '{}',
  }
}

describe('channel system prompt form defaults', () => {
  test('enables prepend for legacy settings with an effective prompt', () => {
    const channelDefault = transformChannelToFormDefaults(
      createChannel({ system_prompt: 'channel prompt' })
    )
    const modelSpecific = transformChannelToFormDefaults(
      createChannel({
        model_system_prompts: {
          'empty-model': '   ',
          'configured-model': 'model prompt',
        },
      })
    )

    assert.equal(channelDefault.system_prompt_override, true)
    assert.equal(modelSpecific.system_prompt_override, true)
  })

  test('preserves an explicitly disabled or enabled prepend setting', () => {
    const disabled = transformChannelToFormDefaults(
      createChannel({
        system_prompt: 'channel prompt',
        system_prompt_override: false,
      })
    )
    const enabled = transformChannelToFormDefaults(
      createChannel({
        system_prompt: 'channel prompt',
        system_prompt_override: true,
      })
    )

    assert.equal(disabled.system_prompt_override, false)
    assert.equal(enabled.system_prompt_override, true)
  })

  test('keeps prepend disabled when legacy settings contain no effective prompt', () => {
    const defaults = transformChannelToFormDefaults(
      createChannel({
        system_prompt: '   ',
        model_system_prompts: { 'empty-model': '' },
      })
    )

    assert.equal(defaults.system_prompt_override, false)
  })

  test('persists the explicit prepend value in the channel setting payload', () => {
    const result = transformFormDataToCreatePayload({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'test channel',
      key: 'test-key',
      models: 'gpt-4o-mini',
      system_prompt: 'channel prompt',
      system_prompt_override: false,
    })
    assert.equal(typeof result.channel.setting, 'string')
    const setting = JSON.parse(String(result.channel.setting))

    assert.equal(setting.system_prompt, 'channel prompt')
    assert.equal(setting.system_prompt_override, false)
  })
})

describe('channel temporary auto-disable overrides', () => {
  test('inherits global settings by default', () => {
    const payload = transformFormDataToCreatePayload({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'test channel',
      key: 'test-key',
      models: 'MODEL_A',
    })
    const settings = JSON.parse(String(payload.channel.settings))

    assert.equal(Object.hasOwn(settings, 'auto_disable_override'), false)
    assert.equal(CHANNEL_FORM_DEFAULT_VALUES.auto_ban, 1)
  })

  test('restores and persists a complete channel override', () => {
    const channel = createChannel({})
    channel.settings = JSON.stringify({
      auto_disable_override: {
        window_minutes: 5,
        min_requests: 20,
        error_rate_percent: 70,
        disable_minutes: 30,
      },
    })

    const defaults = transformChannelToFormDefaults(channel)
    assert.equal(defaults.auto_disable_use_global, false)
    assert.equal(defaults.auto_disable_window_minutes, 5)
    assert.equal(defaults.auto_disable_min_requests, 20)
    assert.equal(defaults.auto_disable_error_rate_percent, 70)
    assert.equal(defaults.auto_disable_disable_minutes, 30)

    const payload = transformFormDataToUpdatePayload(defaults, channel.id)
    const settings = JSON.parse(String(payload.settings))
    assert.deepEqual(settings.auto_disable_override, {
      window_minutes: 5,
      min_requests: 20,
      error_rate_percent: 70,
      disable_minutes: 30,
    })
  })
})

describe('channel multi-key conversion payload', () => {
  test('adds append mode and strategy when converting a single-key channel', () => {
    const payload = transformFormDataToUpdatePayload(
      {
        ...CHANNEL_FORM_DEFAULT_VALUES,
        name: 'test channel',
        key: 'new-key-a\nnew-key-b',
        models: 'MODEL_A',
        multi_key_mode: 'multi_to_single',
        multi_key_type: 'polling',
        key_mode: 'append',
      },
      1,
      false
    )

    assert.equal(payload.key_mode, 'append')
    assert.equal(payload.multi_key_mode, 'polling')
  })

  test('keeps ordinary single-key updates free of multi-key fields', () => {
    const payload = transformFormDataToUpdatePayload(
      {
        ...CHANNEL_FORM_DEFAULT_VALUES,
        name: 'test channel',
        key: 'replacement-key',
        models: 'MODEL_A',
      },
      1,
      false
    )

    assert.equal(payload.key_mode, undefined)
    assert.equal(payload.multi_key_mode, undefined)
  })

  test('keeps the existing multi-key update mode', () => {
    const payload = transformFormDataToUpdatePayload(
      {
        ...CHANNEL_FORM_DEFAULT_VALUES,
        name: 'test channel',
        key: 'replacement-key-a\nreplacement-key-b',
        models: 'MODEL_A',
        key_mode: 'replace',
      },
      1,
      true
    )

    assert.equal(payload.key_mode, 'replace')
    assert.equal(payload.multi_key_mode, undefined)
  })
})

describe('channel protocol policy persistence', () => {
  test('persists and reloads an explicitly empty model override object', () => {
    const protocolPolicy = {
      native: { openai: { non_stream: true, stream: true } },
      model_overrides: {},
      auto_convert: true,
      max_quality: 'fair',
    }
    const payload = transformFormDataToUpdatePayload(
      {
        ...CHANNEL_FORM_DEFAULT_VALUES,
        name: 'test channel',
        models: 'MODEL_A',
        protocol_policy: JSON.stringify(protocolPolicy),
      },
      1
    )
    const setting = JSON.parse(String(payload.setting))
    const defaults = transformChannelToFormDefaults(
      createChannel(setting as Record<string, unknown>)
    )

    assert.deepEqual(setting.protocol_policy, protocolPolicy)
    assert.deepEqual(JSON.parse(defaults.protocol_policy || ''), protocolPolicy)
  })

  test('round-trips normalized mode and reasoning history without field loss', () => {
    const protocolPolicy = {
      native: { openai: { non_stream: true, stream: true } },
      model_overrides: {
        MODEL_A: {
          native: {
            anthropic: {
              non_stream: true,
              stream: true,
              mode: 'normalized',
              reasoning_history: 'strip',
            },
            'openai-response': {
              non_stream: true,
              stream: true,
              mode: 'normalized',
            },
          },
        },
      },
      auto_convert: true,
      max_quality: 'fair',
    }
    const payload = transformFormDataToUpdatePayload(
      {
        ...CHANNEL_FORM_DEFAULT_VALUES,
        name: 'test channel',
        models: 'MODEL_A',
        protocol_policy: JSON.stringify(protocolPolicy),
      },
      1
    )
    const setting = JSON.parse(String(payload.setting))
    const defaults = transformChannelToFormDefaults(
      createChannel(setting as Record<string, unknown>)
    )

    assert.deepEqual(setting.protocol_policy, protocolPolicy)
    assert.deepEqual(JSON.parse(defaults.protocol_policy || ''), protocolPolicy)
  })

  test('rejects unsupported normalized modes and pass-through conflicts', () => {
    const invalidMode = channelFormSchema.safeParse({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'test channel',
      key: 'test-key',
      models: 'MODEL_A',
      protocol_policy: JSON.stringify({
        native: {
          openai: {
            non_stream: true,
            stream: true,
            mode: 'normalized',
          },
        },
        auto_convert: false,
        max_quality: 'fair',
      }),
    })
    assert.equal(invalidMode.success, false)

    const passThroughConflict = channelFormSchema.safeParse({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'test channel',
      key: 'test-key',
      models: 'MODEL_A',
      pass_through_body_enabled: true,
      protocol_policy: JSON.stringify({
        native: {
          anthropic: {
            non_stream: true,
            stream: true,
            mode: 'normalized',
          },
        },
        auto_convert: false,
        max_quality: 'fair',
      }),
    })
    assert.equal(passThroughConflict.success, false)
  })
})

describe('channel model input modality persistence', () => {
  test('reloads channel overrides and preserves adjacent channel settings', () => {
    const defaults = transformChannelToFormDefaults(
      createChannel(
        {
          force_format: true,
          system_prompt: 'keep me',
          model_context_fallbacks: {
            MODEL_A: {
              source_context_window_tokens: 128000,
              fallback_model: 'MODEL_B',
              fallback_context_window_tokens: 256000,
              route_mode: 'same_channel',
            },
          },
          model_input_modalities: {
            VISION_MODEL: ['image', 'text'],
            TEXT_MODEL: ['text'],
          },
        },
        'VISION_MODEL,TEXT_MODEL'
      )
    )

    assert.deepEqual(defaults.model_input_modalities, {
      TEXT_MODEL: ['text'],
      VISION_MODEL: ['text', 'image'],
    })

    const payload = transformFormDataToUpdatePayload(defaults, 1)
    const setting = JSON.parse(String(payload.setting))
    assert.deepEqual(setting.model_input_modalities, {
      TEXT_MODEL: ['text'],
      VISION_MODEL: ['text', 'image'],
    })
    assert.equal(setting.force_format, true)
    assert.equal(setting.system_prompt, 'keep me')
    assert.equal(
      setting.model_context_fallbacks.MODEL_A.fallback_model,
      'MODEL_B'
    )
  })

  test('omits model_input_modalities after the final override is removed', () => {
    const payload = transformFormDataToCreatePayload({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'test channel',
      key: 'test-key',
      models: 'MODEL_A',
      model_input_modalities: {},
    })
    const setting = JSON.parse(String(payload.channel.setting))

    assert.equal(Object.hasOwn(setting, 'model_input_modalities'), false)
  })

  test('prunes declarations for models removed from the channel list', () => {
    const payload = transformFormDataToUpdatePayload(
      {
        ...CHANNEL_FORM_DEFAULT_VALUES,
        name: 'test channel',
        models: 'MODEL_A,model-a',
        model_mapping: '{"MODEL_A":"UPSTREAM_MODEL"}',
        model_input_modalities: {
          MODEL_A: ['image', 'text'],
          'model-a': ['text'],
          UPSTREAM_MODEL: ['text'],
          MODEL_B: ['text'],
        },
      },
      1
    )
    const setting = JSON.parse(String(payload.setting))

    assert.deepEqual(setting.model_input_modalities, {
      MODEL_A: ['text', 'image'],
      'model-a': ['text'],
    })
  })

  test('builds new and existing channel probe drafts from current form values', () => {
    const formData = {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      name: 'draft channel',
      type: 1,
      key: 'draft-key',
      base_url: 'https://draft.example.com/',
      models: 'MODEL_A',
    }

    const createDraft = transformFormDataToNativeProbeDraft(formData)
    assert.equal(createDraft.channel_id, undefined)
    assert.equal(createDraft.channel.key, 'draft-key')
    assert.equal(createDraft.channel.base_url, 'https://draft.example.com')

    const updateDraft = transformFormDataToNativeProbeDraft(
      { ...formData, key: '' },
      42
    )
    assert.equal(updateDraft.channel_id, 42)
    assert.equal(Object.hasOwn(updateDraft.channel, 'key'), false)
    assert.equal(updateDraft.channel.base_url, 'https://draft.example.com')
  })
})
