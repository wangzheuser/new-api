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
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Banner,
  Button,
  Card,
  Input,
  InputNumber,
  Radio,
  RadioGroup,
  Select,
  Slider,
  TabPane,
  Tabs,
  Tag,
  TextArea,
  Typography,
} from '@douyinfe/semi-ui';
import {
  IconAlertTriangle,
  IconChevronDown,
  IconChevronUp,
  IconCode,
  IconDelete,
  IconPlus,
  IconSearch,
} from '@douyinfe/semi-icons';

import { API } from '../../../../helpers';
import {
  calculateContextFallbackTriggerTokens,
  createEmptyModelContextFallback,
  MAX_CONTEXT_FALLBACK_RULES,
  parseModelContextFallbacks,
  serializeModelContextFallbacks,
  validateModelContextFallbackDrafts,
} from './modelContextFallback';

const { Text } = Typography;

const splitCommaList = (value = '') =>
  String(value)
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean);

const groupsAreCompatible = (sourceGroups, targetGroup) => {
  const targets = splitCommaList(targetGroup);
  return (
    sourceGroups.includes('all') ||
    targets.includes('all') ||
    targets.some((group) => sourceGroups.includes(group))
  );
};

const channelSupportsModel = (channel, model) =>
  splitCommaList(channel.models).includes(model);

const getFieldError = (errors, field) =>
  errors.find((error) => error.field === field);

const toChannel = (channel, id) => ({
  id: Number(channel?.id || id),
  name: channel?.name || '',
  group: channel?.group || '',
  models: channel?.models || '',
  status: Number(channel?.status || 0),
  missing: !channel,
});

const RuleCard = ({
  rule,
  errors,
  expanded,
  disabled,
  sourceModels,
  fallbackModels,
  currentChannelId,
  currentGroups,
  onChange,
  onDelete,
  onToggle,
}) => {
  const { t } = useTranslation();
  const [search, setSearch] = useState('');
  const [searching, setSearching] = useState(false);
  const [candidates, setCandidates] = useState([]);
  const [selectedChannels, setSelectedChannels] = useState([]);
  const targetEditorVisible =
    expanded &&
    rule.routeMode === 'cross_channel' &&
    rule.targetMode === 'limited';
  const triggerTokens = calculateContextFallbackTriggerTokens(
    rule.sourceContextWindowTokens,
    rule.thresholdPercent,
  );
  const sourceOptions = useMemo(
    () =>
      Array.from(new Set([...sourceModels, rule.sourceModel]))
        .filter(Boolean)
        .map((model) => ({ label: model, value: model })),
    [rule.sourceModel, sourceModels],
  );
  const fallbackOptions = useMemo(
    () =>
      Array.from(new Set([...fallbackModels, rule.fallbackModel]))
        .filter(Boolean)
        .map((model) => ({ label: model, value: model })),
    [fallbackModels, rule.fallbackModel],
  );
  const sourceModelSet = useMemo(() => new Set(sourceModels), [sourceModels]);
  const fallbackModelSet = useMemo(
    () => new Set(fallbackModels),
    [fallbackModels],
  );

  useEffect(() => {
    if (!targetEditorVisible || !rule.fallbackModel.trim()) {
      setCandidates([]);
      return;
    }
    let cancelled = false;
    const timer = window.setTimeout(async () => {
      setSearching(true);
      try {
        const response = await API.get('/api/channel/search', {
          params: {
            keyword: search || undefined,
            model: rule.fallbackModel,
            status: 'enabled',
            p: 1,
            page_size: 50,
          },
        });
        const items = response?.data?.data?.items || [];
        if (!cancelled) {
          setCandidates(
            items
              .map((channel) => toChannel(channel))
              .filter(
                (channel) =>
                  channel.id !== currentChannelId &&
                  channelSupportsModel(channel, rule.fallbackModel),
              ),
          );
        }
      } catch {
        if (!cancelled) setCandidates([]);
      } finally {
        if (!cancelled) setSearching(false);
      }
    }, 300);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [currentChannelId, rule.fallbackModel, search, targetEditorVisible]);

  useEffect(() => {
    if (!targetEditorVisible || rule.targetChannelIds.length === 0) {
      setSelectedChannels([]);
      return;
    }
    let cancelled = false;
    Promise.all(
      rule.targetChannelIds.map(async (id) => {
        try {
          const response = await API.get(`/api/channel/${id}`);
          return toChannel(response?.data?.data, id);
        } catch {
          return toChannel(null, id);
        }
      }),
    ).then((channels) => {
      if (!cancelled) setSelectedChannels(channels);
    });
    return () => {
      cancelled = true;
    };
  }, [rule.targetChannelIds, targetEditorVisible]);

  const updateRule = (values, clearTargets = false) => {
    onChange({
      ...rule,
      ...values,
      targetChannelIds: clearTargets ? [] : rule.targetChannelIds,
    });
  };

  const reorderTarget = (index, direction) => {
    const targetIndex = index + direction;
    if (targetIndex < 0 || targetIndex >= rule.targetChannelIds.length) return;
    const ids = [...rule.targetChannelIds];
    const current = ids[index];
    ids[index] = ids[targetIndex];
    ids[targetIndex] = current;
    onChange({ ...rule, targetChannelIds: ids });
  };

  const removeTarget = (id) => {
    onChange({
      ...rule,
      targetChannelIds: rule.targetChannelIds.filter(
        (targetId) => targetId !== id,
      ),
    });
  };

  const summaryHeader = (
    <div className='flex items-center gap-2 min-w-0'>
      <span className='truncate font-medium'>
        {rule.sourceModel || t('源模型')}
      </span>
      <span className='text-semi-color-text-2'>→</span>
      <span className='truncate font-medium'>
        {rule.fallbackModel || t('兜底模型')}
      </span>
      <Tag size='small' color='blue'>
        {rule.thresholdPercent || '—'}%
      </Tag>
      <Tag size='small'>
        {rule.routeMode === 'same_channel' ? t('同渠道') : t('跨渠道')}
      </Tag>
      {rule.routeMode === 'cross_channel' && (
        <Tag size='small'>
          {rule.targetMode === 'auto'
            ? t('按分组自动选择')
            : `#${rule.targetChannelIds.length}`}
        </Tag>
      )}
      {Object.keys(rule.extra).length > 0 && (
        <Tag size='small' color='amber'>
          {t('包含高级字段')}
        </Tag>
      )}
      {errors.length > 0 && (
        <IconAlertTriangle className='text-red-500 shrink-0' />
      )}
    </div>
  );

  return (
    <Card
      className='!rounded-xl'
      style={{
        borderColor:
          errors.length > 0
            ? 'var(--semi-color-danger)'
            : 'var(--semi-color-border)',
      }}
      header={
        <button
          type='button'
          className='w-full text-left bg-transparent border-0 p-0 cursor-pointer'
          aria-expanded={expanded}
          disabled={disabled}
          onClick={onToggle}
        >
          {summaryHeader}
        </button>
      }
      headerExtraContent={
        <Button
          theme='borderless'
          type='danger'
          size='small'
          icon={<IconDelete />}
          disabled={disabled}
          aria-label={t('删除兜底规则')}
          onClick={(event) => {
            event.stopPropagation();
            onDelete();
          }}
        />
      }
      bodyStyle={{ padding: expanded ? 16 : 0 }}
    >
      {expanded && (
        <div className='space-y-4'>
          <div className='grid grid-cols-1 xl:grid-cols-2 gap-5'>
            <section className='space-y-4'>
              <div>
                <Text strong>{t('触发条件')}</Text>
                <Text type='tertiary' size='small' className='block mt-1'>
                  {t('定义源模型何时切换到兜底模型。')}
                </Text>
              </div>

              <div>
                <Text className='block mb-2'>{t('源模型')}</Text>
                <Select
                  id={`context-fallback-${rule.id}-sourceModel`}
                  value={rule.sourceModel || undefined}
                  optionList={sourceOptions}
                  filter
                  allowCreate
                  disabled={disabled}
                  placeholder={t('选择或输入源模型')}
                  style={{ width: '100%' }}
                  onChange={(value) =>
                    updateRule({ sourceModel: String(value || '') })
                  }
                />
                <Text
                  type={
                    getFieldError(errors, 'sourceModel') ? 'danger' : 'tertiary'
                  }
                  size='small'
                  className='block mt-1'
                >
                  {getFieldError(errors, 'sourceModel')
                    ? t(getFieldError(errors, 'sourceModel').message)
                    : !sourceModelSet.has(rule.sourceModel) && rule.sourceModel
                      ? t('自定义或未发布模型')
                      : t('请求使用该模型时将评估此规则。')}
                </Text>
              </div>

              <div>
                <Text className='block mb-2'>{t('源模型上下文窗口')}</Text>
                <InputNumber
                  id={`context-fallback-${rule.id}-sourceContextWindowTokens`}
                  value={rule.sourceContextWindowTokens || undefined}
                  min={1}
                  step={1}
                  disabled={disabled}
                  style={{ width: '100%' }}
                  placeholder='262144'
                  onNumberChange={(value) =>
                    updateRule({
                      sourceContextWindowTokens:
                        value === undefined ? '' : String(value),
                    })
                  }
                />
                <Text
                  type={
                    getFieldError(errors, 'sourceContextWindowTokens')
                      ? 'danger'
                      : 'tertiary'
                  }
                  size='small'
                  className='block mt-1'
                >
                  {t(
                    getFieldError(errors, 'sourceContextWindowTokens')
                      ?.message || '源模型支持的最大上下文 Token。',
                  )}
                </Text>
              </div>

              <div>
                <div className='flex items-center justify-between mb-2'>
                  <Text>{t('触发阈值')}</Text>
                  <InputNumber
                    id={`context-fallback-${rule.id}-thresholdPercent`}
                    value={rule.thresholdPercent || undefined}
                    min={1}
                    max={100}
                    step={1}
                    suffix='%'
                    disabled={disabled}
                    style={{ width: 96 }}
                    onNumberChange={(value) =>
                      updateRule({
                        thresholdPercent:
                          value === undefined ? '' : String(value),
                      })
                    }
                  />
                </div>
                <Slider
                  min={1}
                  max={100}
                  step={1}
                  value={Number(rule.thresholdPercent) || 90}
                  disabled={disabled}
                  aria-label={t('触发阈值百分比')}
                  onChange={(value) =>
                    updateRule({ thresholdPercent: String(value) })
                  }
                />
                <Text
                  type={
                    getFieldError(errors, 'thresholdPercent')
                      ? 'danger'
                      : 'tertiary'
                  }
                  size='small'
                  className='block mt-1'
                >
                  {getFieldError(errors, 'thresholdPercent')
                    ? t(getFieldError(errors, 'thresholdPercent').message)
                    : triggerTokens === null
                      ? t('填写有效窗口与阈值后显示触发 Token。')
                      : t('达到 {{tokens}} Token 时触发兜底。', {
                          tokens: triggerTokens.toLocaleString(),
                        })}
                </Text>
              </div>
            </section>

            <section className='space-y-4'>
              <div>
                <Text strong>{t('兜底目标')}</Text>
                <Text type='tertiary' size='small' className='block mt-1'>
                  {t('选择单次兜底使用的模型与渠道策略。')}
                </Text>
              </div>

              <div>
                <Text className='block mb-2'>{t('兜底模型')}</Text>
                <Select
                  id={`context-fallback-${rule.id}-fallbackModel`}
                  value={rule.fallbackModel || undefined}
                  optionList={fallbackOptions}
                  filter
                  allowCreate
                  disabled={disabled}
                  placeholder={t('选择或输入兜底模型')}
                  style={{ width: '100%' }}
                  onChange={(value) =>
                    updateRule({ fallbackModel: String(value || '') })
                  }
                />
                <Text
                  type={
                    getFieldError(errors, 'fallbackModel')
                      ? 'danger'
                      : 'tertiary'
                  }
                  size='small'
                  className='block mt-1'
                >
                  {getFieldError(errors, 'fallbackModel')
                    ? t(getFieldError(errors, 'fallbackModel').message)
                    : !fallbackModelSet.has(rule.fallbackModel) &&
                        rule.fallbackModel
                      ? t('自定义或未发布模型')
                      : t('请求仍按原始模型计费。')}
                </Text>
              </div>

              <div>
                <Text className='block mb-2'>{t('兜底模型上下文窗口')}</Text>
                <InputNumber
                  id={`context-fallback-${rule.id}-fallbackContextWindowTokens`}
                  value={rule.fallbackContextWindowTokens || undefined}
                  min={1}
                  step={1}
                  disabled={disabled}
                  style={{ width: '100%' }}
                  placeholder='1048576'
                  onNumberChange={(value) =>
                    updateRule({
                      fallbackContextWindowTokens:
                        value === undefined ? '' : String(value),
                    })
                  }
                />
                <Text
                  type={
                    getFieldError(errors, 'fallbackContextWindowTokens')
                      ? 'danger'
                      : 'tertiary'
                  }
                  size='small'
                  className='block mt-1'
                >
                  {t(
                    getFieldError(errors, 'fallbackContextWindowTokens')
                      ?.message || '兜底模型支持的最大上下文 Token。',
                  )}
                </Text>
              </div>

              {triggerTokens !== null &&
                Number(rule.fallbackContextWindowTokens) < triggerTokens && (
                  <Banner
                    type='warning'
                    description={t(
                      '兜底上下文窗口小于当前计算出的触发 Token。',
                    )}
                  />
                )}

              <div>
                <Text className='block mb-2'>{t('路由模式')}</Text>
                <RadioGroup
                  type='card'
                  direction='horizontal'
                  name={`${rule.id}-route-mode`}
                  value={rule.routeMode}
                  disabled={disabled}
                  aria-label={t('路由模式')}
                  onChange={(event) => {
                    const routeMode = event.target.value;
                    updateRule(
                      {
                        routeMode,
                        targetMode:
                          routeMode === 'same_channel'
                            ? 'auto'
                            : rule.targetMode,
                      },
                      routeMode === 'same_channel',
                    );
                  }}
                >
                  <Radio value='same_channel'>{t('同渠道')}</Radio>
                  <Radio value='cross_channel'>{t('跨渠道')}</Radio>
                </RadioGroup>
              </div>

              {rule.routeMode === 'cross_channel' && (
                <div>
                  <Text className='block mb-2'>{t('目标渠道策略')}</Text>
                  <RadioGroup
                    type='card'
                    name={`${rule.id}-target-mode`}
                    value={rule.targetMode}
                    disabled={disabled}
                    aria-label={t('目标渠道策略')}
                    onChange={(event) => {
                      const targetMode = event.target.value;
                      updateRule({ targetMode }, targetMode === 'auto');
                    }}
                  >
                    <Radio
                      value='auto'
                      extra={t('在兼容分组内自动选择可用渠道')}
                    >
                      {t('按分组自动选择')}
                    </Radio>
                    <Radio
                      value='limited'
                      extra={t('只按顺序尝试下方选中的渠道')}
                    >
                      {t('限定候选渠道')}
                    </Radio>
                  </RadioGroup>
                </div>
              )}
            </section>
          </div>

          {targetEditorVisible && (
            <div
              className='space-y-3 rounded-lg p-4'
              style={{ background: 'var(--semi-color-fill-0)' }}
            >
              <div>
                <Text strong>{t('候选渠道')}</Text>
                <Text type='tertiary' size='small' className='block mt-1'>
                  {t('高级自定义路径等最终资格将在请求时复核。')}
                </Text>
              </div>

              {selectedChannels.length > 0 && (
                <div className='space-y-2'>
                  {selectedChannels.map((channel, index) => {
                    const isSelf = channel.id === currentChannelId;
                    const disabledChannel = channel.status !== 1;
                    const missingModel =
                      !channel.missing &&
                      !channelSupportsModel(channel, rule.fallbackModel);
                    const incompatible =
                      !channel.missing &&
                      !groupsAreCompatible(currentGroups, channel.group);
                    const warning =
                      channel.missing ||
                      isSelf ||
                      disabledChannel ||
                      missingModel ||
                      incompatible;
                    return (
                      <div
                        key={channel.id}
                        className='flex items-center gap-2 rounded-lg border p-2'
                        style={{
                          borderColor: warning
                            ? 'var(--semi-color-warning)'
                            : 'var(--semi-color-border)',
                        }}
                      >
                        <Tag size='small'>{index + 1}</Tag>
                        <div className='min-w-0 flex-1'>
                          <Text className='block truncate'>
                            {channel.name || t('不可用渠道')} #{channel.id}
                          </Text>
                          <Text
                            type='tertiary'
                            size='small'
                            className='block truncate'
                          >
                            {channel.group || t('未知分组')}
                          </Text>
                        </div>
                        {warning && (
                          <Tag size='small' color='amber'>
                            {channel.missing
                              ? t('不可用')
                              : isSelf
                                ? t('运行时忽略')
                                : disabledChannel
                                  ? t('已停用')
                                  : missingModel
                                    ? t('不支持兜底模型')
                                    : t('分组不兼容')}
                          </Tag>
                        )}
                        <Button
                          theme='borderless'
                          size='small'
                          icon={<IconChevronUp />}
                          disabled={disabled || index === 0}
                          aria-label={t('上移渠道')}
                          onClick={() => reorderTarget(index, -1)}
                        />
                        <Button
                          theme='borderless'
                          size='small'
                          icon={<IconChevronDown />}
                          disabled={
                            disabled || index === selectedChannels.length - 1
                          }
                          aria-label={t('下移渠道')}
                          onClick={() => reorderTarget(index, 1)}
                        />
                        <Button
                          theme='borderless'
                          type='danger'
                          size='small'
                          icon={<IconDelete />}
                          disabled={disabled}
                          aria-label={t('移除目标渠道')}
                          onClick={() => removeTarget(channel.id)}
                        />
                      </div>
                    );
                  })}
                </div>
              )}

              <Input
                value={search}
                prefix={<IconSearch />}
                disabled={disabled || !rule.fallbackModel.trim()}
                placeholder={t('按名称或 ID 搜索渠道')}
                onChange={setSearch}
              />
              {getFieldError(errors, 'targetChannelIds') && (
                <Text type='danger' size='small' className='block'>
                  {t(getFieldError(errors, 'targetChannelIds').message)}
                </Text>
              )}
              <div className='space-y-1 max-h-56 overflow-y-auto'>
                {searching && (
                  <Text type='tertiary' className='block text-center py-3'>
                    {t('正在搜索渠道...')}
                  </Text>
                )}
                {!searching &&
                  candidates.map((channel) => {
                    const selected = rule.targetChannelIds.includes(channel.id);
                    const compatible = groupsAreCompatible(
                      currentGroups,
                      channel.group,
                    );
                    return (
                      <div
                        key={channel.id}
                        className='flex items-center gap-2 rounded-lg px-2 py-2'
                      >
                        <div className='min-w-0 flex-1'>
                          <Text className='block truncate'>
                            {channel.name} #{channel.id}
                          </Text>
                          <Text
                            type='tertiary'
                            size='small'
                            className='block truncate'
                          >
                            {channel.group}
                          </Text>
                        </div>
                        <Tag
                          size='small'
                          color={compatible ? 'green' : 'amber'}
                        >
                          {compatible ? t('分组兼容') : t('分组不兼容')}
                        </Tag>
                        <Button
                          size='small'
                          theme='light'
                          disabled={disabled || selected || !compatible}
                          onClick={() =>
                            onChange({
                              ...rule,
                              targetChannelIds: [
                                ...rule.targetChannelIds,
                                channel.id,
                              ],
                            })
                          }
                        >
                          {selected ? t('已选择') : t('选择')}
                        </Button>
                      </div>
                    );
                  })}
                {!searching &&
                  candidates.length === 0 &&
                  rule.fallbackModel && (
                    <Text type='tertiary' className='block text-center py-3'>
                      {t('未找到可用渠道')}
                    </Text>
                  )}
              </div>
            </div>
          )}

          <Text type='tertiary' size='small' className='block'>
            <Text strong size='small'>
              {t('规则摘要')}：
            </Text>
            {t(
              '当 {{sourceModel}} 达到 {{percent}}% 阈值时，单次切换到 {{fallbackModel}}，并使用{{strategy}}。',
              {
                sourceModel: rule.sourceModel || t('未设置源模型'),
                percent: rule.thresholdPercent || '—',
                fallbackModel: rule.fallbackModel || t('未设置兜底模型'),
                strategy:
                  rule.routeMode === 'same_channel'
                    ? t('当前渠道')
                    : rule.targetMode === 'auto'
                      ? t('分组自动路由')
                      : t('所选渠道顺序'),
              },
            )}
          </Text>
        </div>
      )}
    </Card>
  );
};

const ModelContextFallbackEditor = ({
  value = '',
  onChange,
  sourceModels = [],
  fallbackModels = [],
  currentChannelId,
  currentGroups = [],
  disabled = false,
  onValidityChange,
}) => {
  const { t } = useTranslation();
  const initialRef = useRef(parseModelContextFallbacks(value));
  const [mode, setMode] = useState(
    initialRef.current.error ? 'json' : 'visual',
  );
  const [rules, setRules] = useState(initialRef.current.rules);
  const [jsonValue, setJsonValue] = useState(value);
  const [jsonError, setJsonError] = useState(initialRef.current.error);
  const [expandedRuleId, setExpandedRuleId] = useState(
    initialRef.current.rules[0]?.id || null,
  );
  const nextRuleIdRef = useRef(initialRef.current.rules.length);
  const lastEmittedValueRef = useRef(value);
  const onValidityChangeRef = useRef(onValidityChange);
  const visualErrors = useMemo(
    () => validateModelContextFallbackDrafts(rules),
    [rules],
  );
  const activeError = mode === 'json' ? jsonError : visualErrors[0]?.message;

  useEffect(() => {
    onValidityChangeRef.current = onValidityChange;
  }, [onValidityChange]);

  useEffect(() => {
    onValidityChangeRef.current?.(activeError || null);
  }, [activeError]);

  useEffect(() => {
    if (value === lastEmittedValueRef.current) return;
    lastEmittedValueRef.current = value;
    const parsed = parseModelContextFallbacks(value);
    setJsonValue(value);
    setJsonError(parsed.error);
    if (parsed.error) {
      setMode('json');
      return;
    }
    setRules(parsed.rules);
    nextRuleIdRef.current = parsed.rules.length;
    setExpandedRuleId(parsed.rules[0]?.id || null);
  }, [value]);

  const emitValue = (nextValue) => {
    lastEmittedValueRef.current = nextValue;
    onChange(nextValue);
  };

  const focusError = (error) => {
    if (!error?.ruleId) return;
    setExpandedRuleId(error.ruleId);
    window.requestAnimationFrame(() => {
      document
        .getElementById(`context-fallback-${error.ruleId}-${error.field}`)
        ?.focus();
    });
  };

  const syncRules = (nextRules) => {
    setRules(nextRules);
    const errors = validateModelContextFallbackDrafts(nextRules);
    if (errors.length > 0) return;
    const serialized = serializeModelContextFallbacks(nextRules);
    setJsonValue(serialized);
    setJsonError('');
    emitValue(serialized);
  };

  const addRule = () => {
    if (rules.length >= MAX_CONTEXT_FALLBACK_RULES) return;
    nextRuleIdRef.current += 1;
    const rule = createEmptyModelContextFallback(
      `context-fallback-new-${nextRuleIdRef.current}`,
    );
    setExpandedRuleId(rule.id);
    syncRules([...rules, rule]);
    window.requestAnimationFrame(() => {
      document
        .getElementById(`context-fallback-${rule.id}-sourceModel`)
        ?.focus();
    });
  };

  const changeMode = (nextMode) => {
    if (nextMode === 'json') {
      if (visualErrors.length > 0) {
        focusError(visualErrors[0]);
        return;
      }
      setMode('json');
      return;
    }
    const parsed = parseModelContextFallbacks(jsonValue);
    setJsonError(parsed.error);
    if (parsed.error) return;
    setRules(parsed.rules);
    setExpandedRuleId(parsed.rules[0]?.id || null);
    setMode('visual');
  };

  const changeJson = (nextValue) => {
    setJsonValue(nextValue);
    const parsed = parseModelContextFallbacks(nextValue);
    setJsonError(parsed.error);
    if (parsed.error) return;
    setRules(parsed.rules);
    setExpandedRuleId(parsed.rules[0]?.id || null);
    emitValue(nextValue);
  };

  const formatJson = () => {
    const parsed = parseModelContextFallbacks(jsonValue);
    setJsonError(parsed.error);
    if (parsed.error) return;
    const formatted = jsonValue.trim()
      ? JSON.stringify(JSON.parse(jsonValue), null, 2)
      : '';
    setJsonValue(formatted);
    emitValue(formatted);
  };

  return (
    <div className='space-y-3'>
      <div className='flex items-center justify-between gap-3'>
        <Tabs type='button' activeKey={mode} onChange={changeMode}>
          <TabPane tab={t('可视化')} itemKey='visual' />
          <TabPane tab='JSON' itemKey='json' />
        </Tabs>
        {mode === 'json' && (
          <Button
            size='small'
            theme='light'
            icon={<IconCode />}
            disabled={disabled || Boolean(jsonError)}
            onClick={formatJson}
          >
            {t('格式化 JSON')}
          </Button>
        )}
      </div>

      {activeError && (
        <Banner
          type='danger'
          icon={<IconAlertTriangle />}
          description={t(activeError)}
        />
      )}

      {mode === 'visual' ? (
        rules.length === 0 ? (
          <div
            className='flex min-h-36 flex-col items-center justify-center gap-3 rounded-xl border border-dashed p-6 text-center'
            style={{ borderColor: 'var(--semi-color-border)' }}
          >
            <Text strong>{t('尚未配置模型上下文兜底规则')}</Text>
            <Text type='tertiary' size='small'>
              {t('新增规则后即可通过表单配置，无需手写 JSON。')}
            </Text>
            <Button
              theme='light'
              icon={<IconPlus />}
              disabled={disabled}
              onClick={addRule}
            >
              {t('新增兜底规则')}
            </Button>
          </div>
        ) : (
          <div className='space-y-2'>
            {rules.map((rule) => (
              <RuleCard
                key={rule.id}
                rule={rule}
                errors={visualErrors.filter(
                  (error) => error.ruleId === rule.id,
                )}
                expanded={expandedRuleId === rule.id}
                disabled={disabled}
                sourceModels={sourceModels}
                fallbackModels={fallbackModels}
                currentChannelId={currentChannelId}
                currentGroups={currentGroups}
                onToggle={() =>
                  setExpandedRuleId((current) =>
                    current === rule.id ? null : rule.id,
                  )
                }
                onChange={(nextRule) =>
                  syncRules(
                    rules.map((current) =>
                      current.id === nextRule.id ? nextRule : current,
                    ),
                  )
                }
                onDelete={() =>
                  syncRules(rules.filter((item) => item.id !== rule.id))
                }
              />
            ))}
            <Button
              block
              theme='light'
              icon={<IconPlus />}
              disabled={disabled || rules.length >= MAX_CONTEXT_FALLBACK_RULES}
              onClick={addRule}
            >
              {t('新增兜底规则')}
            </Button>
          </div>
        )
      ) : (
        <div>
          <TextArea
            value={jsonValue}
            autosize={{ minRows: 12, maxRows: 24 }}
            disabled={disabled}
            aria-invalid={Boolean(jsonError)}
            placeholder='{}'
            style={{ fontFamily: 'monospace' }}
            onChange={changeJson}
          />
          <Text
            type={jsonError ? 'danger' : 'tertiary'}
            size='small'
            className='block mt-1'
          >
            {jsonError
              ? t(jsonError)
              : t('切回可视化编辑时会继续保留未知高级字段。')}
          </Text>
        </div>
      )}
    </div>
  );
};

export default ModelContextFallbackEditor;
