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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import {
  enableLimitedQuotaReclaim,
  previewLimitedQuotaReclaim,
} from '../../api'
import { LimitedQuotaReclaimDialog } from '../limited-quota-reclaim-dialog'

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

vi.mock('../../api', () => ({
  previewLimitedQuotaReclaim: vi.fn(),
  enableLimitedQuotaReclaim: vi.fn(),
}))

const previewMock = vi.mocked(previewLimitedQuotaReclaim)
const enableMock = vi.mocked(enableLimitedQuotaReclaim)

function renderDialog() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const onOpenChange = vi.fn()
  const onEnabled = vi.fn()
  render(
    <QueryClientProvider client={queryClient}>
      <LimitedQuotaReclaimDialog
        open
        onOpenChange={onOpenChange}
        onEnabled={onEnabled}
      />
    </QueryClientProvider>
  )
  return { onEnabled, onOpenChange }
}

describe('LimitedQuotaReclaimDialog', () => {
  beforeEach(() => {
    previewMock.mockResolvedValue({
      success: true,
      data: {
        snapshot: 'snapshot-1',
        summary: {
          code_count: 2,
          user_count: 1,
          total_quota: 100,
          deadline_count: 1,
          exception_count: 0,
        },
        items: [
          {
            key: 'alpha',
            quota: 100,
            used_user_id: 9,
            username: 'ada',
            redeemed_time: 1,
            expired_time: 2_000_000_000,
            remaining_quota: 80,
            result: 'eligible',
            can_enable: true,
          },
          {
            key: 'beta',
            quota: 100,
            used_user_id: 0,
            redeemed_time: 0,
            expired_time: 2_000_000_000,
            remaining_quota: 0,
            result: 'already_enabled',
            can_enable: false,
          },
        ],
      },
    })
    enableMock.mockResolvedValue({
      success: true,
      data: { enabled_count: 1, manual_review_count: 0, skipped_count: 1 },
    })
  })

  test('deduplicates pasted keys and requires a second confirmation before enabling', async () => {
    const user = userEvent.setup()
    const callbacks = renderDialog()

    await user.type(
      screen.getByRole('textbox', { name: 'Redemption Code Keys' }),
      ' alpha, beta，alpha '
    )
    expect(
      screen.getByText('Duplicates Removed').nextSibling
    ).toHaveTextContent('1')

    await user.click(screen.getByRole('button', { name: 'Preview' }))
    await screen.findByText('ada')
    expect(previewMock.mock.calls[0][0]).toEqual(['alpha', 'beta'])
    expect(screen.getByRole('table').parentElement).toHaveClass(
      'overflow-x-auto'
    )

    await user.selectOptions(
      screen.getByRole('combobox', { name: 'Filter preview result' }),
      'invalid'
    )
    expect(screen.queryByText('beta')).not.toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: 'Confirm Enable 1 Codes' })
    )
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    expect(enableMock).not.toHaveBeenCalled()

    await user.click(
      screen.getByRole('button', { name: 'Confirm Enable 1 Codes' })
    )
    await waitFor(() => expect(enableMock).toHaveBeenCalled())
    expect(enableMock.mock.calls[0][0]).toEqual({
      keys: ['alpha', 'beta'],
      snapshot: 'snapshot-1',
    })
    expect(callbacks.onEnabled).toHaveBeenCalledOnce()
    expect(callbacks.onOpenChange).toHaveBeenCalledWith(false)
  })

  test('keeps the dialog open and refreshes changed rows after a snapshot conflict', async () => {
    const user = userEvent.setup()
    const callbacks = renderDialog()
    await user.type(
      screen.getByRole('textbox', { name: 'Redemption Code Keys' }),
      'alpha'
    )
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    await screen.findByText('ada')

    previewMock.mockResolvedValueOnce({
      success: true,
      data: {
        snapshot: 'snapshot-2',
        summary: {
          code_count: 1,
          user_count: 1,
          total_quota: 100,
          deadline_count: 1,
          exception_count: 1,
        },
        items: [
          {
            key: 'alpha',
            quota: 120,
            used_user_id: 9,
            username: 'ada',
            redeemed_time: 1,
            expired_time: 2_000_000_000,
            remaining_quota: 80,
            result: 'eligible',
            can_enable: true,
          },
        ],
      },
    })
    enableMock.mockRejectedValueOnce({
      isAxiosError: true,
      response: { status: 409 },
    })

    await user.click(
      screen.getByRole('button', { name: 'Confirm Enable 1 Codes' })
    )
    await user.click(
      screen.getByRole('button', { name: 'Confirm Enable 1 Codes' })
    )

    await waitFor(() => expect(previewMock).toHaveBeenCalledTimes(2))
    expect(callbacks.onOpenChange).not.toHaveBeenCalledWith(false)
    await waitFor(() =>
      expect(
        document.querySelector('[data-changed="true"]')
      ).toBeInTheDocument()
    )
  })

  test('allows a manual-review-only batch to enter the reclaim workflow', async () => {
    previewMock.mockResolvedValueOnce({
      success: true,
      data: {
        snapshot: 'manual-snapshot',
        summary: {
          code_count: 1,
          user_count: 1,
          total_quota: 50,
          deadline_count: 1,
          exception_count: 1,
        },
        items: [
          {
            key: 'manual-code',
            quota: 50,
            used_user_id: 9,
            username: 'ada',
            redeemed_time: 1,
            expired_time: 2_000_000_000,
            remaining_quota: 0,
            result: 'manual_review',
            reason: 'historical data is incomplete',
            can_enable: true,
          },
        ],
      },
    })
    enableMock.mockResolvedValueOnce({
      success: true,
      data: { enabled_count: 0, manual_review_count: 1, skipped_count: 0 },
    })
    const user = userEvent.setup()
    renderDialog()

    await user.type(
      screen.getByRole('textbox', { name: 'Redemption Code Keys' }),
      'manual-code'
    )
    await user.click(screen.getByRole('button', { name: 'Preview' }))
    await screen.findByText('historical data is incomplete')

    await user.click(
      screen.getByRole('button', { name: 'Confirm Enable 1 Codes' })
    )
    await user.click(
      screen.getByRole('button', { name: 'Confirm Enable 1 Codes' })
    )

    await waitFor(() =>
      expect(enableMock.mock.calls[0]?.[0]).toEqual({
        keys: ['manual-code'],
        snapshot: 'manual-snapshot',
      })
    )
  })
})
