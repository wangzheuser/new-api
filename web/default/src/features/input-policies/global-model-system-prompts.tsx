/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { Pencil } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import { updateGlobalModelSystemPrompts } from './policy'

export function GlobalModelSystemPrompts({
  value,
  onChange,
}: {
  value: Record<string, string>
  onChange: (value: Record<string, string>) => void
}) {
  const { t } = useTranslation()
  const [model, setModel] = useState('')
  const [prompt, setPrompt] = useState('')
  const [editingModel, setEditingModel] = useState<string | null>(null)
  const [editName, setEditName] = useState('')
  const [editPrompt, setEditPrompt] = useState('')
  const entries = Object.entries(value).sort(([a], [b]) => a.localeCompare(b))
  const added = updateGlobalModelSystemPrompts(value, null, model, prompt)
  const edited =
    editingModel === null
      ? null
      : updateGlobalModelSystemPrompts(
          value,
          editingModel,
          editName,
          editPrompt
        )

  /** Apply the new entry to the page draft; the outer save persists it. */
  function add() {
    if (!added) return
    onChange(added)
    setModel('')
    setPrompt('')
  }

  return (
    <fieldset className='space-y-3 rounded-lg border p-4'>
      <legend className='px-1 font-medium'>
        {t('Global model system prompts')}
      </legend>
      <p className='text-muted-foreground text-sm'>
        {t('Used when a channel has no model-specific system prompt.')}
      </p>
      {entries.map(([id, text]) => (
        <div key={id} className='space-y-2 rounded-md border p-3'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <span className='min-w-0 flex-1 font-mono text-sm break-all'>
              {id}
            </span>
            <div className='flex gap-2'>
              {editingModel !== id && (
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={() => {
                    setEditingModel(id)
                    setEditName(id)
                    setEditPrompt(text)
                  }}
                >
                  <Pencil className='h-4 w-4' aria-hidden='true' />
                  {t('Edit')}
                </Button>
              )}
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => {
                  const next = { ...value }
                  delete next[id]
                  onChange(next)
                  if (editingModel === id) setEditingModel(null)
                }}
              >
                {t('Remove')}
              </Button>
            </div>
          </div>
          {editingModel === id ? (
            <div className='space-y-2'>
              <Input
                aria-label={t('Model name')}
                maxLength={255}
                value={editName}
                onChange={(e) => setEditName(e.target.value)}
              />
              <Textarea
                aria-label={t('System Prompt')}
                maxLength={65536}
                value={editPrompt}
                onChange={(e) => setEditPrompt(e.target.value)}
                rows={8}
              />
              <div className='flex gap-2'>
                <Button
                  type='button'
                  disabled={!edited}
                  onClick={() => {
                    if (!edited) return
                    onChange(edited)
                    setEditingModel(null)
                  }}
                >
                  {t('Save')}
                </Button>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() => setEditingModel(null)}
                >
                  {t('Cancel')}
                </Button>
              </div>
            </div>
          ) : (
            <p className='text-muted-foreground text-sm whitespace-pre-wrap'>
              {text}
            </p>
          )}
        </div>
      ))}
      <div className='space-y-2 rounded-md border p-3'>
        <Input
          aria-label={t('Model name')}
          maxLength={255}
          value={model}
          onChange={(e) => setModel(e.target.value)}
          placeholder={t('Model name')}
        />
        <Textarea
          aria-label={t('System Prompt')}
          maxLength={65536}
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          placeholder={t('System Prompt')}
          rows={4}
        />
        <Button type='button' variant='outline' disabled={!added} onClick={add}>
          {t('Add')}
        </Button>
      </div>
    </fieldset>
  )
}
