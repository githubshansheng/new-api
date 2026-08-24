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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { getLimitedQuotaReclaimStats } from '../../../api'
import { LimitedQuotaReclaimPanel } from '../limited-quota-reclaim-panel'

const navigateMock = vi.hoisted(() => vi.fn())

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateMock,
}))

vi.mock('@visactor/react-vchart', () => ({
  VChart: (props: {
    onClick?: (event: {
      datum: { start_time: number; end_time: number }
    }) => void
  }) => (
    <button
      type='button'
      aria-label='reclaim-trend-chart'
      onClick={() => props.onClick?.({ datum: { start_time: 1, end_time: 2 } })}
    />
  ),
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) => {
      if (!values) return key
      return Object.entries(values).reduce(
        (text, [name, value]) => text.replace(`{{${name}}}`, String(value)),
        key
      )
    },
  }),
}))

vi.mock('../../../api', () => ({
  getLimitedQuotaReclaimStats: vi.fn(),
}))

const statsMock = vi.mocked(getLimitedQuotaReclaimStats)

function renderPanel() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <LimitedQuotaReclaimPanel />
    </QueryClientProvider>
  )
}

describe('LimitedQuotaReclaimPanel', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  beforeEach(() => {
    navigateMock.mockClear()
    statsMock.mockResolvedValue({
      success: true,
      data: {
        active_quota: 1_000,
        active_users: 2,
        expiring_24h_quota: 200,
        expiring_24h_codes: 1,
        reclaimed_today_quota: 300,
        reclaimed_today_users: 1,
        overdue_count: 1,
        manual_review_count: 2,
        error_count: 0,
        trend: [
          {
            date: '2026-08-25',
            start_time: 1,
            end_time: 2,
            quota: 200,
            code_count: 1,
            user_count: 1,
          },
        ],
        attention: [
          {
            expired_time: 2_000_000_000,
            quota: 100,
            code_count: 1,
            user_count: 1,
            status: 'due',
          },
        ],
        today_start: 1_799_971_200,
        updated_at: 1_800_000_000,
      },
    })
  })

  test('loads independently and navigates KPI clicks with reclaim and expiry filters', async () => {
    const user = userEvent.setup()
    renderPanel()

    const active = await screen.findByRole('button', {
      name: /Active Limited Quota/,
    })
    expect(screen.getByLabelText('reclaim-trend-chart')).toBeInTheDocument()
    await user.click(active)

    expect(navigateMock).toHaveBeenCalledWith({
      to: '/redemption-codes',
      search: {
        reclaimStatus: ['active'],
        expireStart: 0,
        expireEnd: 0,
      },
    })

    navigateMock.mockClear()
    await user.click(
      screen.getByRole('button', { name: /Expiring in 24 Hours/ })
    )
    expect(navigateMock).toHaveBeenCalledWith({
      to: '/redemption-codes',
      search: {
        reclaimStatus: ['active'],
        expireStart: expect.any(Number),
        expireEnd: expect.any(Number),
      },
    })

    navigateMock.mockClear()
    await user.click(screen.getByRole('button', { name: /Reclaimed Today/ }))
    expect(navigateMock).toHaveBeenCalledWith({
      to: '/redemption-codes',
      search: {
        reclaimStatus: ['2'],
        expireStart: 1_799_971_200,
        expireEnd: 1_800_000_000,
      },
    })
    expect(screen.getByText(/Last updated:/)).toBeVisible()

    navigateMock.mockClear()
    await user.click(screen.getByLabelText('reclaim-trend-chart'))
    expect(navigateMock).toHaveBeenCalledWith({
      to: '/redemption-codes',
      search: {
        reclaimStatus: ['active'],
        expireStart: 1,
        expireEnd: 2,
      },
    })

    const attention = screen.getByRole('button', { name: /Needs Attention/ })
    expect(attention).toHaveClass('text-destructive')

    navigateMock.mockClear()
    await user.click(screen.getByRole('button', { name: /1 codes · 1 users/ }))
    expect(navigateMock).toHaveBeenCalledWith({
      to: '/redemption-codes',
      search: {
        reclaimStatus: ['due'],
        expireStart: 2_000_000_000,
        expireEnd: 2_000_000_000,
      },
    })
  })

  test('shows a retry action without throwing when the stats endpoint fails', async () => {
    statsMock.mockRejectedValueOnce(new Error('offline'))
    const user = userEvent.setup()
    renderPanel()

    const retry = await screen.findByRole('button', { name: 'Retry' })
    statsMock.mockResolvedValueOnce({
      success: false,
      message: 'still offline',
    })
    await user.click(retry)
    expect(statsMock).toHaveBeenCalledTimes(2)
    expect(
      screen.getByText('Failed to load limited quota reclaim statistics.')
    ).toBeInTheDocument()
  })

  test('shows an independent empty state', async () => {
    statsMock.mockResolvedValueOnce({
      success: true,
      data: {
        active_quota: 0,
        active_users: 0,
        expiring_24h_quota: 0,
        expiring_24h_codes: 0,
        reclaimed_today_quota: 0,
        reclaimed_today_users: 0,
        overdue_count: 0,
        manual_review_count: 0,
        error_count: 0,
        trend: [],
        attention: [],
        today_start: 1_799_971_200,
        updated_at: 1_800_000_000,
      },
    })
    renderPanel()

    expect(
      await screen.findByText('No Limited Quota Reclaim Data')
    ).toBeInTheDocument()
  })

  test('refreshes every minute while the page is visible', async () => {
    vi.useFakeTimers()
    renderPanel()
    await act(async () => {
      await Promise.resolve()
    })
    expect(statsMock).toHaveBeenCalledTimes(1)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000)
    })
    expect(statsMock).toHaveBeenCalledTimes(2)
  })
})
