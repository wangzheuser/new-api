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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'

import { manageMultiKeys } from '../../api'
import type { MultiKeyCooldown } from '../../types'

/** Show per-model restrictions without presenting the whole credential as disabled. */
export function MultiKeyCooldowns(props: {
  channelId: number
  keyIndex: number
  cooldowns?: MultiKeyCooldown[]
  canEdit: boolean
  onChange: () => void
}) {
  const { t } = useTranslation()
  const [selectedModel, setSelectedModel] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  /** Clear only the model confirmed by the administrator. */
  const clearModel = async () => {
    if (!selectedModel || !props.canEdit || busy) return
    setBusy(true)
    try {
      const response = await manageMultiKeys({
        channel_id: props.channelId,
        key_index: props.keyIndex,
        action: 'clear_model_cooldown',
        model: selectedModel,
      })
      if (!response.success) throw new Error(response.message)
      setSelectedModel(null)
      props.onChange()
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Operation failed')
      )
    } finally {
      setBusy(false)
    }
  }

  if (!props.cooldowns?.length) return null
  return (
    <div className='space-y-2 whitespace-normal'>
      {props.cooldowns.map((item) => (
        <div
          key={`${item.scope}:${item.model ?? ''}`}
          className='space-y-1 text-xs'
        >
          <div>
            {item.scope === 'model' ? item.model : t('Whole key cooldown')}
          </div>
          <div>
            {item.state === 'pending_probe'
              ? t('Pending recovery probe')
              : new Date(item.disabled_until * 1000).toLocaleString()}
          </div>
          <div className='text-muted-foreground'>{item.reason}</div>
          {item.scope === 'model' && item.model && props.canEdit && (
            <Button
              size='sm'
              variant='outline'
              onClick={() => setSelectedModel(item.model ?? null)}
            >
              {t('Clear model cooldown')}
            </Button>
          )}
        </div>
      ))}
      <ConfirmDialog
        open={selectedModel !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setSelectedModel(null)
        }}
        title={t('Clear model cooldown')}
        desc={t('Allow this model to use this key again?')}
        isLoading={busy}
        handleConfirm={() => {
          void clearModel()
        }}
      />
    </div>
  )
}
