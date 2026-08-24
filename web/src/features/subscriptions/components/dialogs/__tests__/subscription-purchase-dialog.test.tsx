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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { formatQuota } from '@/lib/format'

import { paySubscriptionBalance } from '../../../api'
import type { PlanRecord } from '../../../types'
import { SubscriptionPurchaseDialog } from '../subscription-purchase-dialog'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

vi.mock('../../../api', () => ({
  paySubscriptionStripe: vi.fn(),
  paySubscriptionCreem: vi.fn(),
  paySubscriptionEpay: vi.fn(),
  paySubscriptionWaffoPancake: vi.fn(),
  paySubscriptionBalance: vi.fn(),
}))

const balancePayMock = vi.mocked(paySubscriptionBalance)
const plan: PlanRecord = {
  plan: {
    id: 7,
    title: 'Pro',
    price_amount: 0.0001,
    currency: 'USD',
    duration_unit: 'month',
    duration_value: 1,
    quota_reset_period: 'never',
    enabled: true,
    sort_order: 1,
    allow_balance_pay: true,
    allow_wallet_overflow: true,
    max_purchase_per_user: 0,
    total_amount: 1_000,
    stripe_price_id: 'price_1',
  },
}

describe('SubscriptionPurchaseDialog', () => {
  beforeEach(() => {
    balancePayMock.mockResolvedValue({
      success: false,
      message: 'stale balance',
    })
  })

  test('excludes limited quota from balance payment while leaving external payment enabled', () => {
    render(
      <SubscriptionPurchaseDialog
        open
        onOpenChange={() => undefined}
        plan={plan}
        userQuota={100}
        limitedQuota={80}
        enableStripe
      />
    )

    expect(screen.getByText('Required')).toBeInTheDocument()
    expect(screen.getByText('Wallet Total Balance')).toBeInTheDocument()
    expect(
      screen.getByText('Limited Quota (Not Available for Subscriptions)')
    ).toBeInTheDocument()
    expect(screen.getByText('Available for Subscription')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Pay with Balance' })
    ).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Stripe' })).toBeEnabled()
  })

  test('keeps the dialog open and refreshes balances after a rejected balance payment', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    const onBalanceRefresh = vi.fn()
    render(
      <SubscriptionPurchaseDialog
        open
        onOpenChange={onOpenChange}
        plan={plan}
        userQuota={100}
        limitedQuota={0}
        onBalanceRefresh={onBalanceRefresh}
      />
    )

    await user.click(screen.getByRole('button', { name: 'Pay with Balance' }))
    await waitFor(() =>
      expect(balancePayMock).toHaveBeenCalledWith({ plan_id: 7 })
    )
    expect(onBalanceRefresh).toHaveBeenCalledOnce()
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  test('shows negative wallet and regular balances without clamping them to zero', () => {
    render(
      <SubscriptionPurchaseDialog
        open
        onOpenChange={() => undefined}
        plan={plan}
        userQuota={-10}
        limitedQuota={0}
      />
    )

    expect(
      screen.getByText('Wallet Total Balance').parentElement
    ).toHaveTextContent(formatQuota(-10))
    expect(
      screen.getByText('Available for Subscription').parentElement
    ).toHaveTextContent(formatQuota(-10))
    expect(
      screen.getByRole('button', { name: 'Pay with Balance' })
    ).toBeDisabled()
  })
})
