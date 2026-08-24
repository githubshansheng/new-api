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
import { render, screen } from '@testing-library/react'
import { describe, expect, test, vi } from 'vitest'

import type { LiandongProduct } from '../../types'
import { LiandongProductCard } from '../liandong-product-card'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

vi.mock('@/hooks/use-system-config', () => ({
  useSystemConfig: () => ({ currency: { quotaPerUnit: 500_000 } }),
}))

describe('LiandongProductCard', () => {
  test('shows quota product adjusted by the current user group ratio', () => {
    const product: LiandongProduct = {
      id: 1,
      business_type: 'quota',
      goods_type: 'rights',
      name: 'Quota package',
      quota_amount: 100_000_000,
      group_ratio: 0.25,
      plan_id: 0,
      expected_amount_minor: 20_000,
      currency: 'CNY',
      inventory_level: 'unlimited',
    }

    render(<LiandongProductCard product={product} onSelect={vi.fn()} />)

    expect(screen.getByText('≈Official$800（￥200）')).toBeInTheDocument()
  })

  test('shows subscription quota adjusted by the target group ratio', () => {
    const product: LiandongProduct = {
      id: 1,
      business_type: 'subscription',
      goods_type: 'rights',
      name: 'VIP monthly plan',
      quota_amount: 0,
      group_ratio: 0.25,
      plan_id: 8,
      expected_amount_minor: 20_000,
      currency: 'CNY',
      inventory_level: 'unlimited',
      subscription: {
        title: 'VIP monthly plan',
        subtitle: '',
        duration_unit: 'month',
        duration_value: 1,
        custom_seconds: 0,
        total_amount: 100_000_000,
        quota_reset_period: 'never',
        quota_reset_custom_seconds: 0,
        upgrade_group: 'vip',
        group_ratio: 0.25,
      },
    }

    render(<LiandongProductCard product={product} onSelect={vi.fn()} />)

    expect(screen.getByText(/≈Official\$800（￥200）/)).toBeInTheDocument()
    expect(screen.getByText(/vip\(Multiplier0\.25\)/)).toBeInTheDocument()
    expect(screen.queryByText(/^100,000,000（/)).not.toBeInTheDocument()
  })
})
