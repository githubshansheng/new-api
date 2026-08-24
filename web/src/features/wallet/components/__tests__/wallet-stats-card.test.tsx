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
import userEvent from '@testing-library/user-event'
import { describe, expect, test, vi } from 'vitest'

import { WalletStatsCard } from '../wallet-stats-card'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

const user = {
  id: 1,
  username: 'ada',
  quota: 1_000,
  used_quota: 500,
  request_count: 3,
  aff_quota: 0,
  aff_history_quota: 0,
  aff_count: 0,
  group: 'default',
}

describe('WalletStatsCard', () => {
  test('uses an accessible disclosure and highlights the changed expiry group', async () => {
    const interaction = userEvent.setup()
    const deadline = Math.floor(Date.now() / 1000) + 3_600
    render(
      <WalletStatsCard
        user={user}
        limitedQuota={{
          total: 200,
          groups: [{ expired_time: deadline, remaining_quota: 200 }],
        }}
        highlightedExpiredTime={deadline}
      />
    )

    const disclosure = screen.getByRole('button', {
      name: /Balance - Limited Quota/,
    })
    expect(disclosure).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Expires within 24 hours')).toBeInTheDocument()
    expect(screen.getByText(/Expiring soon:/)).not.toHaveClass('hidden')
    expect(
      document.querySelector('[data-highlighted="true"]')
    ).toBeInTheDocument()

    await interaction.click(disclosure)
    expect(disclosure).toHaveAttribute('aria-expanded', 'false')
  })

  test('does not allow expansion when no limited quota exists', () => {
    render(
      <WalletStatsCard user={user} limitedQuota={{ total: 0, groups: [] }} />
    )
    const disclosure = screen.getByRole('button', {
      name: /Balance - Limited Quota/,
    })
    expect(disclosure).toBeDisabled()
    expect(disclosure).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByText('No limited quota')).not.toHaveClass('hidden')
  })

  test('uses the responsive four-card grid and supports Enter and Space', async () => {
    const interaction = userEvent.setup()
    const deadline = Math.floor(Date.now() / 1000) + 172_800
    render(
      <WalletStatsCard
        user={user}
        limitedQuota={{
          total: 200,
          groups: [{ expired_time: deadline, remaining_quota: 200 }],
        }}
      />
    )

    const disclosure = screen.getByRole('button', {
      name: /Balance - Limited Quota/,
    })
    expect(disclosure.parentElement).toHaveClass(
      'grid-cols-2',
      'lg:grid-cols-4'
    )

    disclosure.focus()
    await interaction.keyboard('{Enter}')
    expect(disclosure).toHaveAttribute('aria-expanded', 'true')
    await interaction.keyboard(' ')
    expect(disclosure).toHaveAttribute('aria-expanded', 'false')
  })

  test('keeps the other wallet stats available when limited quota fails', async () => {
    const interaction = userEvent.setup()
    const onRetryLimited = vi.fn()
    render(
      <WalletStatsCard
        user={user}
        limitedError
        onRetryLimited={onRetryLimited}
      />
    )

    expect(screen.getByText('--')).toBeInTheDocument()
    expect(screen.getByText('Current Balance').parentElement).toBeVisible()
    await interaction.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetryLimited).toHaveBeenCalledOnce()
  })
})
