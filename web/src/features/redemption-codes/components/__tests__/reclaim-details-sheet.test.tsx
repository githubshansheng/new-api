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
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'

import type { Redemption } from '../../types'

const i18n = (await import('i18next')).default
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { ReclaimDetailsSheet } = await import('../reclaim-details-sheet')

await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

test('an unredeemed code does not report its full quota as wallet consumption', () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      displayInCurrency: true,
      quotaDisplayType: 'USD',
      quotaPerUnit: 500000,
      usdExchangeRate: 1,
      customCurrencySymbol: '¤',
      customCurrencyExchangeRate: 1,
    },
  })

  const redemption: Redemption = {
    id: 1,
    user_id: 1,
    name: 'pending-code',
    key: 'pending-key',
    status: 1,
    quota: 25000000,
    created_time: 1,
    redeemed_time: 0,
    expired_time: 2000000000,
    used_user_id: 0,
    reclaim_status: 1,
    reclaim_remaining_quota: 0,
    reclaimed_quota: 0,
  }

  render(
    <I18nextProvider i18n={i18n}>
      <ReclaimDetailsSheet
        open
        onOpenChange={() => undefined}
        redemption={redemption}
      />
    </I18nextProvider>
  )

  expect(
    screen.getByText('Wallet Consumption Applied').nextElementSibling
  ).toHaveTextContent('$0')
})
