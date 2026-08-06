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
import {
  Check,
  Copy,
  FlaskConical,
  Loader2,
  MessageSquareText,
  Pencil,
  Plus,
  Trash2,
  X,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Response } from '@/components/ai-elements/response'
import { MultiSelect } from '@/components/multi-select'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import { testChannelPromptEffect } from '../api'

const DEFAULT_TEST_USER_PROMPT = '你好'

type ModelSystemPromptEditorProps = {
  value: Record<string, string>
  onChange: (value: Record<string, string>) => void
  models: string[]
  channelId?: number
  passThroughBodyEnabled?: boolean
  disabled?: boolean
}

export function ModelSystemPromptEditor(props: ModelSystemPromptEditorProps) {
  const { t } = useTranslation()
  const [editorOpen, setEditorOpen] = useState(false)
  const [editingModel, setEditingModel] = useState<string | null>(null)
  const [selectedModels, setSelectedModels] = useState<string[]>([])
  const [prompt, setPrompt] = useState('')
  const [submitted, setSubmitted] = useState(false)
  const [testingModel, setTestingModel] = useState<string | null>(null)
  const [testUserPrompt, setTestUserPrompt] = useState(DEFAULT_TEST_USER_PROMPT)
  const [testResponse, setTestResponse] = useState('')
  const [testError, setTestError] = useState('')
  const [testResponseTime, setTestResponseTime] = useState<number | null>(null)
  const [isTesting, setIsTesting] = useState(false)
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })

  const entries = useMemo(
    () =>
      Object.entries(props.value).sort(([left], [right]) =>
        left.localeCompare(right)
      ),
    [props.value]
  )
  const configuredModels = useMemo(
    () => new Set(Object.keys(props.value)),
    [props.value]
  )
  const modelSet = useMemo(() => new Set(props.models), [props.models])
  const missingModels = useMemo(
    () => Object.keys(props.value).filter((model) => !modelSet.has(model)),
    [modelSet, props.value]
  )
  const availableModelOptions = useMemo(
    () =>
      props.models
        .filter((model) => !configuredModels.has(model))
        .map((model) => ({ label: model, value: model })),
    [configuredModels, props.models]
  )
  let promptTestTooltip = t('Test system prompt effect')
  if (!props.channelId) {
    promptTestTooltip = t('Save the channel before testing')
  } else if (props.passThroughBodyEnabled) {
    promptTestTooltip = t(
      'Request body passthrough is enabled, so system prompts are not injected.'
    )
  }

  const openCreateDialog = () => {
    setEditingModel(null)
    setSelectedModels([])
    setPrompt('')
    setSubmitted(false)
    setEditorOpen(true)
  }

  const openEditDialog = (model: string, currentPrompt: string) => {
    setTestingModel(null)
    setEditingModel(model)
    setSelectedModels([model])
    setPrompt(currentPrompt)
    setSubmitted(false)
    setEditorOpen(true)
  }

  const handleSave = () => {
    setSubmitted(true)
    if (selectedModels.length === 0 || !prompt.trim()) return

    const next = { ...props.value }
    for (const model of selectedModels) {
      next[model] = prompt
    }
    props.onChange(next)
    setEditorOpen(false)
  }

  const handleDelete = (model: string) => {
    const next = { ...props.value }
    delete next[model]
    props.onChange(next)
    if (testingModel === model) setTestingModel(null)
  }

  const openPromptTest = (model: string) => {
    setEditorOpen(false)
    setTestingModel(model)
    setTestResponse('')
    setTestError('')
    setTestResponseTime(null)
  }

  const handlePromptTest = async (model: string, systemPrompt: string) => {
    if (!props.channelId || !testUserPrompt.trim() || isTesting) return

    setIsTesting(true)
    setTestError('')
    try {
      const response = await testChannelPromptEffect(props.channelId, {
        model,
        system_prompt: systemPrompt,
        user_prompt: testUserPrompt.trim(),
      })
      const content = response.data?.content?.trim()
      if (!response.success || !content) {
        setTestError(response.message || t('Test failed'))
        setTestResponse('')
        setTestResponseTime(response.time ?? null)
        return
      }
      setTestResponse(content)
      setTestResponseTime(response.time ?? null)
    } catch (error: unknown) {
      const apiError = error as { response?: { data?: { message?: string } } }
      setTestError(apiError.response?.data?.message || t('Test failed'))
      setTestResponse('')
      setTestResponseTime(null)
    } finally {
      setIsTesting(false)
    }
  }

  const handleRemoveMissing = () => {
    const next = { ...props.value }
    for (const model of missingModels) {
      delete next[model]
    }
    props.onChange(next)
  }

  return (
    <div className='space-y-3'>
      {missingModels.length > 0 && (
        <Alert className='border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-50'>
          <AlertDescription className='flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between'>
            <span>
              {t(
                'System prompt configurations reference models that are no longer published'
              )}
              : {missingModels.join(', ')}
            </span>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={handleRemoveMissing}
              disabled={props.disabled}
            >
              {t('Remove unavailable configurations')}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {entries.length === 0 ? (
        <div className='border-border/70 bg-muted/10 flex min-h-32 flex-col items-center justify-center gap-3 rounded-lg border border-dashed px-4 py-6 text-center'>
          <div className='bg-muted text-muted-foreground flex size-9 items-center justify-center rounded-full'>
            <MessageSquareText className='h-4 w-4' aria-hidden='true' />
          </div>
          <div className='space-y-1'>
            <p className='text-sm font-medium'>
              {t('No model-specific system prompts configured')}
            </p>
            <p className='text-muted-foreground max-w-md text-xs leading-relaxed'>
              {t(
                'Add a prompt for one or more published models without writing parameter override JSON.'
              )}
            </p>
          </div>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={openCreateDialog}
            disabled={props.disabled || props.models.length === 0}
          >
            <Plus className='mr-2 h-4 w-4' aria-hidden='true' />
            {t('Add model system prompt')}
          </Button>
          {props.models.length === 0 && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'Add channel models before configuring model-specific prompts.'
              )}
            </p>
          )}
        </div>
      ) : (
        <div className='space-y-2'>
          <div className='hidden grid-cols-[minmax(10rem,0.8fr)_minmax(0,2fr)_auto] gap-3 px-3 text-xs font-medium sm:grid'>
            <span>{t('Model')}</span>
            <span>{t('System Prompt')}</span>
            <span className='w-28' />
          </div>
          {entries.map(([model, value]) => (
            <div
              key={model}
              className='border-border/70 bg-card grid gap-3 rounded-lg border p-3 sm:grid-cols-[minmax(10rem,0.8fr)_minmax(0,2fr)_auto] sm:items-center'
            >
              <div className='min-w-0'>
                <Badge
                  variant={modelSet.has(model) ? 'secondary' : 'outline'}
                  className='max-w-full font-mono'
                >
                  <span className='truncate'>{model}</span>
                </Badge>
              </div>
              <p className='text-muted-foreground line-clamp-2 min-w-0 text-sm leading-relaxed break-words whitespace-pre-wrap'>
                {value}
              </p>
              <div className='flex justify-end gap-1'>
                <Tooltip>
                  <TooltipTrigger render={<span className='inline-flex' />}>
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon-sm'
                      onClick={() => openPromptTest(model)}
                      disabled={
                        props.disabled ||
                        !props.channelId ||
                        props.passThroughBodyEnabled ||
                        isTesting
                      }
                      aria-label={t('Test system prompt for {{model}}', {
                        model,
                      })}
                    >
                      <FlaskConical className='h-4 w-4' aria-hidden='true' />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>
                    <p>{promptTestTooltip}</p>
                  </TooltipContent>
                </Tooltip>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  onClick={() => openEditDialog(model, value)}
                  disabled={props.disabled}
                  aria-label={t('Edit system prompt for {{model}}', { model })}
                >
                  <Pencil className='h-4 w-4' aria-hidden='true' />
                </Button>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  onClick={() => handleDelete(model)}
                  disabled={props.disabled}
                  aria-label={t('Delete system prompt for {{model}}', {
                    model,
                  })}
                >
                  <Trash2 className='h-4 w-4' aria-hidden='true' />
                </Button>
              </div>

              {testingModel === model && (
                <div className='border-border/70 bg-muted/15 space-y-4 rounded-lg border p-4 sm:col-span-3'>
                  <div className='flex items-start justify-between gap-4'>
                    <div className='space-y-1'>
                      <p className='flex items-center gap-2 text-sm font-medium'>
                        <FlaskConical
                          className='text-primary h-4 w-4'
                          aria-hidden='true'
                        />
                        {t('Test system prompt effect')}
                      </p>
                      <p className='text-muted-foreground text-xs leading-relaxed'>
                        {t(
                          'This test uses the current system prompt without saving it. Other channel settings use the latest saved configuration.'
                        )}
                      </p>
                      <p className='text-muted-foreground text-xs leading-relaxed'>
                        {t(
                          'Testing makes a real upstream request and may incur provider charges.'
                        )}
                      </p>
                    </div>
                    <Button
                      type='button'
                      variant='ghost'
                      size='icon-sm'
                      onClick={() => setTestingModel(null)}
                      disabled={isTesting}
                      aria-label={t('Close')}
                    >
                      <X className='h-4 w-4' aria-hidden='true' />
                    </Button>
                  </div>

                  <div className='space-y-2'>
                    <Label htmlFor='system-prompt-test-user-prompt'>
                      {t('User Prompt')}
                    </Label>
                    <Textarea
                      id='system-prompt-test-user-prompt'
                      value={testUserPrompt}
                      onChange={(event) =>
                        setTestUserPrompt(event.target.value)
                      }
                      onKeyDown={(event) => {
                        if (
                          event.key === 'Enter' &&
                          (event.metaKey || event.ctrlKey)
                        ) {
                          event.preventDefault()
                          void handlePromptTest(model, value)
                        }
                      }}
                      rows={3}
                      maxLength={16 * 1024}
                      disabled={isTesting}
                      placeholder={t('Enter a user prompt to test')}
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t('Press Ctrl or Command + Enter to send')}
                    </p>
                  </div>

                  <div className='flex justify-end'>
                    <Button
                      type='button'
                      onClick={() => void handlePromptTest(model, value)}
                      disabled={!testUserPrompt.trim() || isTesting}
                    >
                      {isTesting ? (
                        <Loader2
                          className='mr-2 h-4 w-4 animate-spin'
                          aria-hidden='true'
                        />
                      ) : (
                        <FlaskConical
                          className='mr-2 h-4 w-4'
                          aria-hidden='true'
                        />
                      )}
                      {isTesting ? t('Testing...') : t('Send Test')}
                    </Button>
                  </div>

                  <div aria-live='polite'>
                    {testError && (
                      <Alert variant='destructive'>
                        <AlertDescription className='break-words'>
                          {testError}
                        </AlertDescription>
                      </Alert>
                    )}

                    {testResponse && (
                      <div className='bg-background space-y-3 rounded-lg border p-4'>
                        <div className='flex items-center justify-between gap-3'>
                          <div>
                            <p className='text-sm font-medium'>
                              {t('Model Response')}
                            </p>
                            {testResponseTime !== null && (
                              <p className='text-muted-foreground text-xs'>
                                {t('Response time: {{seconds}} seconds', {
                                  seconds: testResponseTime.toFixed(2),
                                })}
                              </p>
                            )}
                          </div>
                          <Button
                            type='button'
                            variant='ghost'
                            size='icon-sm'
                            onClick={() => void copyToClipboard(testResponse)}
                            aria-label={t('Copy')}
                          >
                            {copiedText === testResponse ? (
                              <Check
                                className='h-4 w-4 text-green-600'
                                aria-hidden='true'
                              />
                            ) : (
                              <Copy className='h-4 w-4' aria-hidden='true' />
                            )}
                          </Button>
                        </div>
                        <div className='bg-muted/20 max-h-96 overflow-y-auto rounded-md p-3 text-sm leading-relaxed'>
                          <Response>{testResponse}</Response>
                        </div>
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          ))}
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='w-full'
            onClick={openCreateDialog}
            disabled={
              props.disabled ||
              props.models.length === 0 ||
              availableModelOptions.length === 0
            }
          >
            <Plus className='mr-2 h-4 w-4' aria-hidden='true' />
            {availableModelOptions.length === 0
              ? t('All published models are configured')
              : t('Add model system prompt')}
          </Button>
        </div>
      )}

      {editorOpen && (
        <div className='border-border/70 bg-muted/10 space-y-5 rounded-lg border p-4'>
          <div className='space-y-1'>
            <p className='text-sm font-medium'>
              {editingModel
                ? t('Edit model system prompt')
                : t('Add model system prompt')}
            </p>
            <p className='text-muted-foreground text-xs leading-relaxed'>
              {t(
                'The configured prompt is prepended to the client system prompt for matching models.'
              )}
            </p>
          </div>

          <div className='space-y-2'>
            <Label>{t('Applicable Models *')}</Label>
            {editingModel ? (
              <div className='border-input bg-muted/30 rounded-md border px-3 py-2'>
                <Badge variant='secondary' className='font-mono'>
                  {editingModel}
                </Badge>
              </div>
            ) : (
              <MultiSelect
                options={availableModelOptions}
                selected={selectedModels}
                onChange={setSelectedModels}
                placeholder={t('Select one or more published models')}
                disabled={props.disabled}
                maxVisibleChips={6}
              />
            )}
            {submitted && selectedModels.length === 0 && (
              <p className='text-destructive text-xs'>
                {t('Select at least one model')}
              </p>
            )}
          </div>

          <div className='space-y-2'>
            <Label htmlFor='model-system-prompt'>{t('System Prompt *')}</Label>
            <Textarea
              id='model-system-prompt'
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              rows={8}
              className='font-mono text-sm leading-relaxed'
              placeholder={t(
                'Enter the system prompt to prepend for the selected models'
              )}
              disabled={props.disabled}
              aria-invalid={submitted && !prompt.trim()}
            />
            {submitted && !prompt.trim() && (
              <p className='text-destructive text-xs'>
                {t('System prompt is required')}
              </p>
            )}
          </div>

          <div className='bg-muted/40 rounded-lg border p-3'>
            <p className='text-xs font-medium'>{t('Effective prompt order')}</p>
            <p className='text-muted-foreground mt-1 text-xs leading-relaxed'>
              {t('Model-specific prompt')} → {t('Client system prompt')}
            </p>
            <p className='text-muted-foreground mt-1 text-xs leading-relaxed'>
              {t(
                'When the client provides no system prompt, this prompt is injected directly.'
              )}
            </p>
          </div>

          <div className='flex justify-end gap-2'>
            <Button
              type='button'
              variant='outline'
              onClick={() => setEditorOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button type='button' onClick={handleSave}>
              {editingModel ? t('Save') : t('Add')}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
