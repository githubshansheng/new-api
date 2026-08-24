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
import i18next, { type TFunction } from 'i18next'

import { toIntlLocale } from '@/i18n/languages'
import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

import { formatQuota } from './format.ts'

export {
  parsePaymentAmountMinor,
  paymentAmountInputFromMinor,
} from './liandong-amount.ts'

export function formatLiandongAmount(
  currency: string,
  amountMinor: number
): string {
  if (!Number.isSafeInteger(amountMinor)) return '-'

  const absoluteMinor = Math.abs(amountMinor)
  const whole = Math.floor(absoluteMinor / 100)
  const fraction = String(absoluteMinor % 100).padStart(2, '0')
  const amount = `${amountMinor < 0 ? '-' : ''}${Intl.NumberFormat().format(whole)}.${fraction}`

  if (currency === 'CNY') return `￥${amount}`
  if (currency === 'USD') return `$${amount}`
  return `${currency} ${amount}`
}

export function formatLiandongQuota(quota: number): string {
  return `${Intl.NumberFormat().format(quota)}（${formatQuota(quota)}）`
}

export function calculateLiandongEffectiveQuotaUSD(
  quota: number,
  groupRatio = 1,
  quotaPerUnit = DEFAULT_CURRENCY_CONFIG.quotaPerUnit
): number | null {
  if (
    !Number.isFinite(quota) ||
    quota < 0 ||
    !Number.isFinite(groupRatio) ||
    groupRatio <= 0 ||
    !Number.isFinite(quotaPerUnit) ||
    quotaPerUnit <= 0
  ) {
    return null
  }
  return quota / quotaPerUnit / groupRatio
}

export function formatLiandongEffectiveQuota(
  quota: number,
  groupRatio = 1,
  quotaPerUnit = DEFAULT_CURRENCY_CONFIG.quotaPerUnit,
  officialLabel = i18next.t('Official')
): string {
  const quotaCNY = calculateLiandongEffectiveQuotaUSD(quota, 1, quotaPerUnit)
  if (quotaCNY === null) return formatLiandongQuota(quota)

  const effectiveQuota = calculateLiandongEffectiveQuotaUSD(
    quota,
    groupRatio,
    quotaPerUnit
  )
  const officialQuotaFormatter = Intl.NumberFormat(undefined, {
    maximumFractionDigits: 0,
  })
  const cnyFormatter = Intl.NumberFormat(undefined, {
    maximumFractionDigits: 4,
  })
  const formattedUSD = officialQuotaFormatter.format(effectiveQuota ?? quotaCNY)
  const formattedCNY = cnyFormatter.format(quotaCNY)
  return `≈${officialLabel}$${formattedUSD}（￥${formattedCNY}）`
}

export function formatLiandongGroupRatio(
  group: string,
  groupRatio: number,
  multiplierLabel = i18next.t('Multiplier'),
  locale = i18next.resolvedLanguage
): string {
  const normalizedGroup = group.trim()
  if (!normalizedGroup || !Number.isFinite(groupRatio) || groupRatio <= 0) {
    return normalizedGroup
  }
  let formattedRatio = Intl.NumberFormat(toIntlLocale(locale), {
    maximumFractionDigits: 4,
  }).format(groupRatio)
  if (locale?.toLowerCase().startsWith('zh')) {
    formattedRatio = formattedRatio.replace('.', ',')
  }
  return `${normalizedGroup}(${multiplierLabel}${formattedRatio})`
}

const liandongMessageAliases: Record<string, string> = {
  'liandong active order already exists': 'Failed to create payment order',
  'liandong product inventory is unavailable': 'Out of stock',
  'liandong product has active orders': 'Delete failed',
  'Product inventory is unavailable': 'Out of stock',
  链动卡网支付当前不可用: 'Liandong gateway disabled',
  链动卡网支付未完成配置: 'Verification is not configured properly',
  '链动卡网支付配置读取失败，请稍后重试': 'Failed to create payment order',
  '创建支付订单失败，请稍后重试': 'Failed to create payment order',
  '支付订单已创建但本地状态保存失败，请联系管理员核查':
    'This payment order requires administrator review',
  链动卡网入账当前已关闭: 'Operation failed',
  链动卡网订单入账失败: 'Operation failed',
}

export function localizeLiandongMessage(
  t: TFunction,
  message: unknown,
  fallbackKey: string
): string {
  if (typeof message !== 'string') return t(fallbackKey)

  const normalized = message.trim()
  if (!normalized) return t(fallbackKey)
  if (normalized.startsWith('链动创建支付订单失败')) {
    return t('Failed to create payment order')
  }

  const proxyConnectionErrorPrefixes = [
    'Proxy connection failed:',
    'SOCKS5 proxy connection failed:',
  ]
  const proxyConnectionErrorPrefix = proxyConnectionErrorPrefixes.find(
    (prefix) => normalized.startsWith(prefix)
  )
  if (proxyConnectionErrorPrefix) {
    const detail = normalized.slice(proxyConnectionErrorPrefix.length).trim()
    return `${t('Proxy connection failed')}${detail ? `: ${detail}` : ''}`
  }

  const messageKey = liandongMessageAliases[normalized] || normalized
  return i18next.exists(messageKey) ? t(messageKey) : t(fallbackKey)
}
