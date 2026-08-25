/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { useSystemConfigStore } from '@/stores/system-config-store'

import {
  getRedemption,
  resolveManualLimitedQuotaReclaim,
  retryManualLimitedQuotaReclaim,
} from '../../api'
import type { Redemption } from '../../types'
import { ReclaimDetailsSheet } from '../reclaim-details-sheet'

vi.mock('../../api', () => ({
  getRedemption: vi.fn(),
  resolveManualLimitedQuotaReclaim: vi.fn(),
  retryManualLimitedQuotaReclaim: vi.fn(),
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

const getRedemptionMock = vi.mocked(getRedemption)
const retryReviewMock = vi.mocked(retryManualLimitedQuotaReclaim)
const resolveReviewMock = vi.mocked(resolveManualLimitedQuotaReclaim)
const quotaPerUnit = 500_000

function redemption(overrides: Partial<Redemption> = {}): Redemption {
  return {
    id: 1,
    user_id: 1,
    name: 'limited-code',
    key: 'limited-key',
    status: 3,
    quota: quotaPerUnit * 50,
    created_time: 1,
    redeemed_time: 2,
    expired_time: Math.floor(Date.now() / 1000) + 86_400,
    used_user_id: 8,
    reclaim_status: 3,
    reclaim_remaining_quota: 0,
    reclaimed_quota: 0,
    reclaim_enabled_time: 3,
    reclaimed_time: 0,
    reclaim_error: 'Historical logs do not reconcile.',
    ...overrides,
  }
}

function renderSheet(initial: Redemption) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const onUpdated = vi.fn()
  render(
    <QueryClientProvider client={queryClient}>
      <ReclaimDetailsSheet
        open
        onOpenChange={() => undefined}
        redemption={initial}
        onUpdated={onUpdated}
      />
    </QueryClientProvider>
  )
  return { onUpdated }
}

async function openManualReview(initial: Redemption) {
  const user = userEvent.setup()
  getRedemptionMock.mockResolvedValue({ success: true, data: initial })
  const rendered = renderSheet(initial)
  await waitFor(() =>
    expect(getRedemptionMock).toHaveBeenCalledWith(initial.id)
  )
  await user.click(screen.getByRole('button', { name: 'Resolve Manually' }))
  expect(
    screen.getByRole('heading', { name: 'Manually Resolve Limited Quota' })
  ).toBeInTheDocument()
  return { user, ...rendered }
}

async function enterManualDecision(
  user: ReturnType<typeof userEvent.setup>,
  amount: string,
  note = 'Checked wallet logs and account history.'
) {
  const amountInput = screen.getByLabelText('Verified Remaining Quota (USD)')
  await user.clear(amountInput)
  await user.type(amountInput, amount)
  await user.type(screen.getByLabelText('Review Note'), note)
  await user.click(screen.getByRole('button', { name: 'Review Wallet Impact' }))
}

describe('ReclaimDetailsSheet manual review flow', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useSystemConfigStore.getState().setConfig({
      currency: {
        displayInCurrency: true,
        quotaDisplayType: 'USD',
        quotaPerUnit,
        usdExchangeRate: 1,
        customCurrencySymbol: '¤',
        customCurrencyExchangeRate: 1,
      },
    })
  })

  test('an unredeemed code does not report its full quota as wallet consumption', async () => {
    const initial = redemption({
      status: 1,
      redeemed_time: 0,
      used_user_id: 0,
      reclaim_status: 1,
      reclaim_error: '',
    })
    getRedemptionMock.mockResolvedValue({ success: true, data: initial })

    renderSheet(initial)

    expect(
      screen.getByText('Wallet Consumption Applied').nextElementSibling
    ).toHaveTextContent('$0')
  })

  test('retries automatic reconstruction and refreshes the open details sheet', async () => {
    const initial = redemption()
    const completed = redemption({
      reclaim_status: 2,
      reclaim_remaining_quota: quotaPerUnit * 20,
      reclaimed_quota: quotaPerUnit * 20,
      reclaimed_time: 100,
      reclaim_error: '',
      reclaim_reviewed_by: 7,
      reclaim_reviewed_time: 99,
    })
    getRedemptionMock.mockResolvedValue({ success: true, data: initial })
    retryReviewMock.mockResolvedValue({
      success: true,
      data: {
        user_id: 8,
        redemption_ids: [1],
        updated_count: 1,
        pending_count: 0,
        completed_count: 1,
        reclaimed_quota: quotaPerUnit * 20,
      },
    })
    const { onUpdated } = renderSheet(initial)
    await waitFor(() => expect(getRedemptionMock).toHaveBeenCalledTimes(1))
    getRedemptionMock.mockResolvedValue({ success: true, data: completed })

    await userEvent.click(
      screen.getByRole('button', { name: 'Retry Automatic Review' })
    )

    expect(await screen.findByText('Reclaim Completed')).toBeInTheDocument()
    expect(
      screen.getByRole('heading', { name: 'Limited Quota Reclaim Details' })
    ).toBeInTheDocument()
    expect(onUpdated).toHaveBeenCalledTimes(1)
  })

  test('keeps the review actions open when automatic reconstruction is still ambiguous', async () => {
    const initial = redemption()
    getRedemptionMock.mockResolvedValue({ success: true, data: initial })
    retryReviewMock.mockResolvedValue({
      success: false,
      message: 'Wallet history is still ambiguous.',
    })
    renderSheet(initial)

    await userEvent.click(
      screen.getByRole('button', { name: 'Retry Automatic Review' })
    )

    expect(
      await screen.findByText('Wallet history is still ambiguous.')
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Resolve Manually' })
    ).toBeEnabled()
  })

  test('validates manual inputs and can close a review with zero remaining quota', async () => {
    const initial = redemption()
    const { user, onUpdated } = await openManualReview(initial)

    await user.click(
      screen.getByRole('button', { name: 'Review Wallet Impact' })
    )
    expect(
      screen.getByText(
        'Remaining limited quota must be between 0 and the issued quota.'
      )
    ).toBeInTheDocument()

    await user.type(
      screen.getByLabelText('Verified Remaining Quota (USD)'),
      '51'
    )
    await user.type(screen.getByLabelText('Review Note'), 'Checked records.')
    await user.click(
      screen.getByRole('button', { name: 'Review Wallet Impact' })
    )
    expect(
      screen.getByText(
        'Remaining limited quota must be between 0 and the issued quota.'
      )
    ).toBeInTheDocument()

    await user.clear(screen.getByLabelText('Verified Remaining Quota (USD)'))
    await user.type(
      screen.getByLabelText('Verified Remaining Quota (USD)'),
      '0'
    )
    await user.click(
      screen.getByRole('button', { name: 'Review Wallet Impact' })
    )
    expect(
      screen.getByText(
        'This review will be completed without deducting quota from the user wallet.'
      )
    ).toBeInTheDocument()

    resolveReviewMock.mockResolvedValue({
      success: true,
      data: {
        user_id: 8,
        redemption_ids: [1],
        updated_count: 1,
        pending_count: 0,
        completed_count: 1,
        reclaimed_quota: 0,
      },
    })
    getRedemptionMock.mockResolvedValue({
      success: true,
      data: redemption({
        reclaim_status: 2,
        reclaim_error: '',
        reclaim_reviewed_by: 7,
        reclaim_reviewed_time: 100,
        reclaim_review_note: 'Checked records.',
      }),
    })
    await user.click(
      screen.getByRole('button', { name: 'Confirm Manual Resolution' })
    )

    await waitFor(() =>
      expect(resolveReviewMock).toHaveBeenCalledWith(1, {
        remaining_quota: 0,
        note: 'Checked records.',
      })
    )
    expect(onUpdated).toHaveBeenCalledTimes(1)
    expect(
      screen.getByRole('heading', { name: 'Limited Quota Reclaim Details' })
    ).toBeInTheDocument()
  })

  test('keeps a verified future amount active until its deadline', async () => {
    const initial = redemption({
      expired_time: Math.floor(Date.now() / 1000) + 86_400,
    })
    const { user } = await openManualReview(initial)
    await enterManualDecision(user, '25')

    expect(
      screen.getByText(/The verified quota will remain active until/)
    ).toBeInTheDocument()

    resolveReviewMock.mockResolvedValue({
      success: true,
      data: {
        user_id: 8,
        redemption_ids: [1],
        updated_count: 1,
        pending_count: 1,
        completed_count: 0,
        reclaimed_quota: 0,
      },
    })
    getRedemptionMock.mockResolvedValue({
      success: true,
      data: redemption({
        reclaim_status: 1,
        reclaim_remaining_quota: quotaPerUnit * 25,
        reclaim_error: '',
        reclaim_reviewed_by: 7,
        reclaim_reviewed_time: 100,
        reclaim_review_note: 'Checked wallet logs and account history.',
      }),
    })
    await user.click(
      screen.getByRole('button', { name: 'Confirm Manual Resolution' })
    )

    await waitFor(() =>
      expect(resolveReviewMock).toHaveBeenCalledWith(1, {
        remaining_quota: quotaPerUnit * 25,
        note: 'Checked wallet logs and account history.',
      })
    )
    expect(await screen.findByText('Reclaim Active')).toBeInTheDocument()
  })

  test('requires zero remaining quota when the redeemed user is missing', async () => {
    const initial = redemption({ used_user_id: 0 })
    const { user } = await openManualReview(initial)

    expect(
      screen.queryByRole('button', { name: 'Retry Automatic Review' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByText(
        'This record has no redeemed user. Verify the source record, then close it manually with 0 remaining quota.'
      )
    ).toBeInTheDocument()

    await user.type(
      screen.getByLabelText('Verified Remaining Quota (USD)'),
      '1'
    )
    await user.type(
      screen.getByLabelText('Review Note'),
      'Checked source data.'
    )
    await user.click(
      screen.getByRole('button', { name: 'Review Wallet Impact' })
    )
    expect(
      screen.getByText(
        'A record without a redeemed user can only be closed with 0 remaining quota.'
      )
    ).toBeInTheDocument()
  })

  test('warns that an expired verified amount is reclaimed immediately and keeps the dialog on failure', async () => {
    const initial = redemption({
      expired_time: Math.floor(Date.now() / 1000) - 60,
    })
    const { user } = await openManualReview(initial)
    await enterManualDecision(user, '20')

    expect(
      screen.getByText(
        '$20 will be deducted from the user wallet immediately. The wallet may become negative.'
      )
    ).toBeInTheDocument()

    resolveReviewMock.mockResolvedValue({
      success: false,
      message: 'The wallet changed; refresh and review again.',
    })
    await user.click(
      screen.getByRole('button', { name: 'Confirm Manual Resolution' })
    )

    expect(
      await screen.findByText('The wallet changed; refresh and review again.')
    ).toBeInTheDocument()
    expect(
      screen.getByRole('heading', { name: 'Manually Resolve Limited Quota' })
    ).toBeInTheDocument()
  })
})
