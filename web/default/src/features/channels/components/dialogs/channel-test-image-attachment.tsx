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
import { ChevronDown, ImagePlus, Loader2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'

import {
  createChannelTestSample,
  readChannelTestImage,
  type ChannelTestAttachment,
} from '../../lib/channel-test-image'

type Props = {
  value: ChannelTestAttachment | null
  disabled: boolean
  onChange: (value: ChannelTestAttachment | null) => void
  onReadingChange: (reading: boolean) => void
  onSamplePrompt: () => void
}

export function ChannelTestImageAttachment(props: Props) {
  const { t } = useTranslation()
  const input = useRef<HTMLInputElement>(null)
  const generation = useRef(0)
  const [open, setOpen] = useState(false)
  const [preview, setPreview] = useState(false)
  const [error, setError] = useState('')
  const [reading, setReading] = useState(false)
  useEffect(
    () => () => {
      generation.current++
    },
    []
  )

  async function load(files: File[]) {
    if (props.disabled || reading) return
    const current = ++generation.current
    setError('')
    setReading(true)
    props.onReadingChange(true)
    try {
      const image = await readChannelTestImage(files)
      if (generation.current === current) props.onChange(image)
    } catch (cause) {
      if (generation.current === current) {
        setError(
          cause instanceof Error ? cause.message : 'Invalid test image data.'
        )
      }
    } finally {
      if (generation.current === current) {
        setReading(false)
        props.onReadingChange(false)
      }
    }
  }

  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      className='rounded-lg border'
    >
      <CollapsibleTrigger
        render={
          <Button
            variant='ghost'
            className='h-auto w-full min-w-0 flex-wrap justify-between gap-2 py-2 text-left whitespace-normal'
          />
        }
      >
        <span className='flex min-w-0 items-center gap-2'>
          <ImagePlus className='size-4' />
          {t('Attach image (optional)')}
        </span>
        <span className='text-muted-foreground ml-auto flex min-w-0 items-center gap-2 text-xs'>
          {props.value
            ? t('1 image attached')
            : t('Text only without an image')}
          <ChevronDown className='size-4' />
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent className='space-y-3 border-t p-3'>
        <input
          ref={input}
          type='file'
          accept='image/png,image/jpeg,image/webp'
          className='hidden'
          aria-label={t('Select test image')}
          disabled={props.disabled || reading}
          onChange={(event) => {
            const files = [...(event.target.files ?? [])]
            event.target.value = ''
            if (files.length) void load(files)
          }}
        />
        <div
          className='rounded-lg border border-dashed p-3'
          role='group'
          aria-label={t('Test image attachment')}
          tabIndex={0}
          onDragOver={(event) => event.preventDefault()}
          onDrop={(event) => {
            event.preventDefault()
            void load([...event.dataTransfer.files])
          }}
          onPaste={(event) => {
            if (event.clipboardData.files.length) {
              event.preventDefault()
              void load([...event.clipboardData.files])
            }
          }}
        >
          {props.value ? (
            <div className='flex flex-wrap items-center gap-3'>
              <Button
                variant='ghost'
                className='h-auto p-0'
                aria-label={t('Preview test image')}
                onClick={() => setPreview(true)}
              >
                <img
                  className='h-20 w-36 rounded object-contain'
                  src={`data:${props.value.mime_type};base64,${props.value.data}`}
                  alt={t('Test image preview')}
                />
              </Button>
              <div className='min-w-0 flex-1'>
                <p className='text-sm break-all'>{props.value.name}</p>
                <p className='text-muted-foreground text-xs'>
                  {props.value.width} × {props.value.height}
                </p>
              </div>
              <Button
                variant='outline'
                size='sm'
                disabled={props.disabled || reading}
                onClick={() => {
                  setError('')
                  props.onChange(null)
                }}
              >
                {t('Remove')}
              </Button>
            </div>
          ) : (
            <p className='text-muted-foreground text-center text-sm'>
              {t('Drop an image here or paste a screenshot.')}
            </p>
          )}
          <div className='mt-3 flex flex-wrap gap-2'>
            <Button
              variant='outline'
              size='sm'
              disabled={props.disabled || reading}
              onClick={() => input.current?.click()}
            >
              {reading && <Loader2 className='size-4 animate-spin' />}
              {t('Select test image')}
            </Button>
            <Button
              variant='outline'
              size='sm'
              disabled={props.disabled || reading}
              onClick={() => {
                try {
                  setError('')
                  props.onChange(createChannelTestSample())
                  props.onSamplePrompt()
                } catch {
                  setError('Unable to create the test image.')
                }
              }}
            >
              {t('Use sample image')}
            </Button>
          </div>
        </div>
        {error && (
          <p role='alert' className='text-destructive text-xs'>
            {t(error)}
          </p>
        )}
        <p className='text-muted-foreground text-xs'>
          {t(
            'One PNG, JPEG or WebP image, up to 4 MiB. Images are cleared when this dialog closes.'
          )}
        </p>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Request success does not prove image understanding. Review the response; sample checks only cover this test image.'
          )}
        </p>
      </CollapsibleContent>
      <Dialog
        open={preview}
        onOpenChange={setPreview}
        title={t('Preview test image')}
      >
        {props.value && (
          <img
            className='max-h-[60vh] w-full object-contain'
            src={`data:${props.value.mime_type};base64,${props.value.data}`}
            alt={t('Test image preview')}
          />
        )}
      </Dialog>
    </Collapsible>
  )
}
