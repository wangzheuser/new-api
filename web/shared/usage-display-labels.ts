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
export function formatBillingTierLabel(
  label: string | undefined,
  t: (key: string) => string
): string {
  switch (label?.trim().toLowerCase()) {
    case undefined:
    case '':
    case 'default':
      return t('Default')
    case 'standard':
      return t('Standard')
    case 'long_context':
      return t('Long context')
    case 'base':
      return t('Base tier')
    case 'request':
      return t('Per-call')
    case 'image':
      return t('Image')
    case 'audio':
      return t('Audio')
    default:
      return label ?? ''
  }
}

export function textProtocolLabel(
  format: string | undefined,
  t: (key: string) => string
): string {
  switch (format) {
    case 'openai':
      return t('Chat')
    case 'OpenAI Compatible':
      return t('OpenAI Compatible')
    case 'openai-response':
    case 'OpenAI Responses':
    case 'openai_responses_compatible':
      return t('OpenAI Responses')
    case 'anthropic':
    case 'Claude Messages':
    case 'anthropic_messages_compatible':
      return t('Claude Messages')
    case 'gemini':
    case 'Google Gemini':
      return t('Gemini Generate Content')
    default:
      return format || '—'
  }
}

export function formatReasoningEffortLabel(
  effort: string,
  t: (key: string) => string
): string {
  switch (effort) {
    case 'none':
      return t('None')
    case 'minimal':
      return t('Minimal')
    case 'low':
      return t('Low')
    case 'medium':
      return t('Medium')
    case 'high':
      return t('High')
    case 'xhigh':
      return t('Very high')
    case 'max':
      return t('Maximum')
    default:
      return effort
  }
}
