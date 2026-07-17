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
import type { TFunction } from 'i18next'

export function liandongPaymentStatusLabel(
  t: TFunction,
  status: string
): string {
  switch (status) {
    case 'creating':
      return t('Creating order')
    case 'pending':
      return t('Pending payment')
    case 'paid':
      return t('Paid')
    case 'create_failed':
      return t('Order creation failed')
    case 'create_unknown':
      return t('Order creation status unknown')
    case 'expired':
      return t('Expired')
    case 'review_required':
      return t('Administrator review required')
    case 'closed':
      return t('Closed')
    default:
      return status
  }
}

export function liandongFulfillmentStatusLabel(
  t: TFunction,
  status: string
): string {
  switch (status) {
    case 'waiting':
      return t('Waiting for activation')
    case 'fulfilled':
      return t('Activated')
    case 'failed':
      return t('Activation failed')
    case 'review_required':
      return t('Administrator review required')
    default:
      return status
  }
}
