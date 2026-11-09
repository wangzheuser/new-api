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
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'

import type { Channel } from '../types'

export function ChannelTemporaryDisableDetails(props: {
  info: NonNullable<Channel['temporary_auto_disable']>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const info = props.info
  const isStatistical =
    info.mechanism === 'rolling_window' ||
    (!info.mechanism && info.requests > 0)

  return (
    <>
      {info.mechanism === 'immediate_quota' && (
        <div>{t('Quota or rate-limit cooldown')}</div>
      )}
      <div>
        {t('Disabled until:')} {formatTimestampToDate(info.disabled_until)}
      </div>
      {info.status_code !== undefined && info.status_code > 0 && (
        <div>
          {t('HTTP status')}: {info.status_code}
        </div>
      )}
      {isStatistical && (
        <>
          <div>
            {t('Recent upstream sample size:')}{' '}
            {formatNumber(info.sample_size, locale)}
          </div>
          <div>
            {t('Minimum health sample size:')}{' '}
            {formatNumber(info.minimum_sample_size, locale)}
          </div>
          <div>
            {t('Upstream responses:')} {formatNumber(info.requests, locale)}
          </div>
          <div>
            {t('Matching errors:')} {formatNumber(info.errors, locale)}
          </div>
          <div>
            {t('Error rate:')}{' '}
            {new Intl.NumberFormat(locale, {
              minimumFractionDigits: 1,
              maximumFractionDigits: 1,
            }).format(info.error_rate_percent)}
            %
          </div>
          <div>
            {t('Status codes:')} {info.status_codes}
          </div>
        </>
      )}
    </>
  )
}
