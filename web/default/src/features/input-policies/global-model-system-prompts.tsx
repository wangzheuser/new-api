/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

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
  const entries = Object.entries(value).sort(([a], [b]) => a.localeCompare(b))

  function add() {
    const id = model.trim()
    if (!id || !prompt.trim()) return
    onChange({ ...value, [id]: prompt })
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
          <div className='flex items-center justify-between gap-2'>
            <span className='font-mono text-sm break-all'>{id}</span>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => {
                const next = { ...value }
                delete next[id]
                onChange(next)
              }}
            >
              {t('Remove')}
            </Button>
          </div>
          <p className='text-muted-foreground whitespace-pre-wrap text-sm'>
            {text}
          </p>
        </div>
      ))}
      <div className='space-y-2 rounded-md border p-3'>
        <Input
          value={model}
          onChange={(e) => setModel(e.target.value)}
          placeholder={t('Model name')}
        />
        <Textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          placeholder={t('System Prompt')}
          rows={4}
        />
        <Button type='button' variant='outline' onClick={add}>
          {t('Add')}
        </Button>
      </div>
    </fieldset>
  )
}
