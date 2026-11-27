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
export type PricingValues = Record<
  string,
  number | string | Record<string, string>
>

export type PricingSyncSnapshot = {
  empty_version: string
  entries: Array<{
    model_name: string
    version: string
    configured: PricingValues
    effective: PricingValues
  }>
}

export type PricingSyncChange = {
  model_name: string
  expected_version: string
  pricing: PricingValues
}

const ratioFields = {
  model_ratio: 'ModelRatio',
  completion_ratio: 'CompletionRatio',
  cache_ratio: 'CacheRatio',
  create_cache_ratio: 'CreateCacheRatio',
  image_ratio: 'ImageRatio',
  audio_ratio: 'AudioRatio',
  audio_completion_ratio: 'AudioCompletionRatio',
}

const optionKeys: Record<string, string> = {
  ...ratioFields,
  model_price: 'ModelPrice',
  billing_mode: 'billing_setting.billing_mode',
  billing_expr: 'billing_setting.billing_expr',
}

// Both themes submit selected models through the same atomic, versioned API.
export function buildModelPricingSyncChanges(
  snapshot: PricingSyncSnapshot,
  selections: Record<string, Record<string, number | string>>
): PricingSyncChange[] {
  const entries = new Map(
    snapshot.entries.map((entry) => [entry.model_name, entry])
  )
  return Object.entries(selections).map(([name, fields]) => {
    const entry = entries.get(name)
    const pricing: PricingValues = { ...entry?.configured }
    if (fields.model_price !== undefined) {
      for (const key of Object.values(ratioFields)) delete pricing[key]
    } else if (
      Object.keys(ratioFields).some((field) => fields[field] !== undefined)
    ) {
      delete pricing.ModelPrice
    }
    for (const [field, value] of Object.entries(fields)) {
      const key = optionKeys[field]
      if (key) {
        pricing[key] =
          field === 'billing_mode' || field === 'billing_expr'
            ? value
            : Number(value)
      }
    }
    if (typeof fields.billing_expr === 'string' && fields.billing_expr.trim()) {
      pricing['billing_setting.billing_mode'] = 'tiered_expr'
    } else if (
      fields.model_price !== undefined ||
      Object.keys(ratioFields).some((field) => fields[field] !== undefined)
    ) {
      pricing['billing_setting.billing_mode'] = 'ratio'
    }
    if (
      pricing['billing_setting.billing_mode'] === 'tiered_expr' &&
      pricing['billing_setting.billing_expr'] === undefined
    ) {
      const expression = entry?.effective['billing_setting.billing_expr']
      if (expression !== undefined) {
        pricing['billing_setting.billing_expr'] = expression
      }
    }
    return {
      model_name: name,
      expected_version: entry?.version ?? snapshot.empty_version,
      pricing,
    }
  })
}
