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
import { PackageOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  formatDuration,
  formatResetPeriod,
} from '@/features/subscriptions/lib/format'
import type { SubscriptionPlan } from '@/features/subscriptions/types'
import {
  formatLiandongAmount,
  formatLiandongQuota,
} from '@/lib/liandong-payment'
import { cn } from '@/lib/utils'

import type { LiandongInventoryLevel, LiandongProduct } from '../types'

function inventoryLabel(
  level: LiandongInventoryLevel,
  t: (key: string) => string
): string {
  switch (level) {
    case 'unlimited':
      return t('Unlimited stock')
    case 'sufficient':
      return t('In stock')
    case 'normal':
      return t('Limited stock')
    case 'low':
      return t('Low stock')
    case 'out_of_stock':
      return t('Out of stock')
  }
}

function inventoryClassName(level: LiandongInventoryLevel): string {
  switch (level) {
    case 'unlimited':
    case 'sufficient':
      return 'bg-emerald-600 text-white'
    case 'normal':
      return 'bg-sky-600 text-white'
    case 'low':
      return 'bg-amber-500 text-black'
    case 'out_of_stock':
      return 'bg-zinc-700 text-white'
  }
}

type Props = {
  product: LiandongProduct
  onSelect: (product: LiandongProduct) => void
}

export function LiandongProductCard({ product, onSelect }: Props) {
  const { t } = useTranslation()
  const disabled = product.inventory_level === 'out_of_stock'
  const subscription = product.subscription
  let specification = t('Subscription')
  let subscriptionDetails = ''

  if (product.business_type === 'quota') {
    specification = formatLiandongQuota(product.quota_amount)
  } else if (subscription) {
    const subscriptionPlan: Partial<SubscriptionPlan> = {
      title: subscription.title,
      duration_unit:
        subscription.duration_unit as SubscriptionPlan['duration_unit'],
      duration_value: subscription.duration_value,
      custom_seconds: subscription.custom_seconds,
      total_amount: subscription.total_amount,
      quota_reset_period:
        subscription.quota_reset_period as SubscriptionPlan['quota_reset_period'],
      quota_reset_custom_seconds: subscription.quota_reset_custom_seconds,
      upgrade_group: subscription.upgrade_group,
    }
    specification = `${subscription.title} · ${formatDuration(subscriptionPlan, t)}`
    subscriptionDetails = [
      formatLiandongQuota(subscription.total_amount),
      formatResetPeriod(subscriptionPlan, t),
      subscription.upgrade_group || t('No change'),
    ].join(' · ')
  }

  return (
    <button
      type='button'
      disabled={disabled}
      onClick={() => onSelect(product)}
      className={cn(
        'bg-card text-card-foreground group relative flex h-[300px] w-[220px] shrink-0 flex-col overflow-hidden rounded-lg border text-left shadow-xs transition-colors',
        'focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none',
        disabled
          ? 'cursor-not-allowed opacity-65'
          : 'hover:border-primary/60 hover:bg-accent/20'
      )}
      aria-label={`${product.name}, ${inventoryLabel(product.inventory_level, t)}`}
    >
      <div className='bg-muted relative aspect-square w-full shrink-0 overflow-hidden border-b'>
        {product.thumbnail_url ? (
          <img
            src={product.thumbnail_url}
            alt={product.name}
            className='h-full w-full object-cover transition-transform duration-200 group-hover:scale-[1.02]'
          />
        ) : (
          <div className='text-muted-foreground flex h-full flex-col items-center justify-center gap-2 p-4 text-center'>
            <PackageOpen className='h-10 w-10' />
            <span className='line-clamp-2 text-xs'>{product.name}</span>
          </div>
        )}
        <span
          className={cn(
            'absolute top-2 right-2 rounded px-2 py-1 text-[11px] leading-none font-medium',
            inventoryClassName(product.inventory_level)
          )}
        >
          {inventoryLabel(product.inventory_level, t)}
        </span>
      </div>

      <div className='flex min-h-0 flex-1 flex-col justify-between gap-0.5 px-3 py-1.5'>
        <div className='min-w-0'>
          <p className='truncate text-[13px] leading-4 font-medium'>
            {product.name}
          </p>
          <p className='text-muted-foreground truncate text-[10px] leading-3.5'>
            {specification}
          </p>
          {subscriptionDetails && (
            <p className='text-muted-foreground truncate text-[10px] leading-3.5'>
              {subscriptionDetails}
            </p>
          )}
        </div>
        <p className='text-primary text-sm leading-4 font-bold'>
          {formatLiandongAmount(
            product.currency,
            product.expected_amount_minor
          )}
        </p>
      </div>
    </button>
  )
}
