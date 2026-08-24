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
import { useTranslation } from 'react-i18next'

import { StatusBadge, type StatusVariant } from '@/components/status-badge'

import { RECLAIM_STATUS } from '../constants'
import type { Redemption } from '../types'

interface ReclaimStatusBadgeProps {
  redemption: Redemption
}

export function ReclaimStatusBadge(props: ReclaimStatusBadgeProps) {
  const { t } = useTranslation()
  let label = t('Reclaim Disabled')
  let variant: StatusVariant = 'neutral'

  const reclaimStatus = props.redemption.reclaim_status ?? 0

  if (reclaimStatus === RECLAIM_STATUS.DISABLED) {
    return <span className='text-muted-foreground'>—</span>
  }

  if (reclaimStatus === RECLAIM_STATUS.PENDING) {
    if (props.redemption.used_user_id === 0) {
      label = t('Waiting for Redemption')
      variant = 'info'
    } else if (
      props.redemption.expired_time > 0 &&
      props.redemption.expired_time < Math.floor(Date.now() / 1000)
    ) {
      label = t('Reclaim Due')
      variant = 'warning'
    } else {
      label = t('Reclaim Active')
      variant = 'success'
    }
  } else if (reclaimStatus === RECLAIM_STATUS.COMPLETED) {
    label = t('Reclaim Completed')
    variant = 'neutral'
  } else if (reclaimStatus === RECLAIM_STATUS.MANUAL_REVIEW) {
    label = t('Manual Review')
    variant = 'warning'
  } else if (reclaimStatus === RECLAIM_STATUS.ERROR) {
    label = t('Reclaim Error')
    variant = 'danger'
  }

  return <StatusBadge label={label} variant={variant} copyable={false} />
}
