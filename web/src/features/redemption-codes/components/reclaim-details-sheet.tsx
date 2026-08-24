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

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { formatQuota, formatTimestampToDate } from '@/lib/format'

import type { Redemption } from '../types'
import { ReclaimStatusBadge } from './reclaim-status-badge'

interface ReclaimDetailsSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  redemption: Redemption | null
}

export function ReclaimDetailsSheet(props: ReclaimDetailsSheetProps) {
  const { t } = useTranslation()
  const redemption = props.redemption
  const walletConsumedQuota = redemption?.used_user_id
    ? Math.max(
        0,
        redemption.quota -
          (redemption.reclaim_remaining_quota ?? 0) -
          (redemption.reclaimed_quota ?? 0)
      )
    : 0

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className='sm:max-w-lg'>
        <SheetHeader>
          <SheetTitle>{t('Limited Quota Reclaim Details')}</SheetTitle>
          <SheetDescription>
            {t(
              'Reclaim status and settlement information for this redemption code.'
            )}
          </SheetDescription>
        </SheetHeader>
        {redemption && (
          <div className='flex flex-col gap-4 overflow-y-auto px-4 pb-6'>
            <div className='flex items-center justify-between gap-3 rounded-lg border p-3'>
              <span className='text-muted-foreground text-sm'>
                {t('Status')}
              </span>
              <ReclaimStatusBadge redemption={redemption} />
            </div>
            <dl className='grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-3 rounded-lg border p-4 text-sm'>
              <dt className='text-muted-foreground'>{t('Code Name')}</dt>
              <dd className='max-w-64 truncate text-right font-medium'>
                {redemption.name}
              </dd>
              <dt className='text-muted-foreground'>{t('Issued Quota')}</dt>
              <dd className='text-right tabular-nums'>
                {formatQuota(redemption.quota)}
              </dd>
              <dt className='text-muted-foreground'>{t('Redeemed At')}</dt>
              <dd className='text-right'>
                {redemption.redeemed_time > 0
                  ? formatTimestampToDate(redemption.redeemed_time)
                  : '-'}
              </dd>
              <dt className='text-muted-foreground'>
                {t('Wallet Consumption Applied')}
              </dt>
              <dd className='text-right tabular-nums'>
                {redemption.reclaim_status === 3
                  ? '-'
                  : formatQuota(walletConsumedQuota)}
              </dd>
              <dt className='text-muted-foreground'>
                {t('Reclaim Remaining Quota')}
              </dt>
              <dd className='text-right tabular-nums'>
                {formatQuota(redemption.reclaim_remaining_quota ?? 0)}
              </dd>
              <dt className='text-muted-foreground'>{t('Reclaimed Quota')}</dt>
              <dd className='text-right tabular-nums'>
                {formatQuota(redemption.reclaimed_quota ?? 0)}
              </dd>
              <dt className='text-muted-foreground'>
                {t('Reclaim Enabled At')}
              </dt>
              <dd className='text-right'>
                {(redemption.reclaim_enabled_time ?? 0) > 0
                  ? formatTimestampToDate(redemption.reclaim_enabled_time ?? 0)
                  : '-'}
              </dd>
              <dt className='text-muted-foreground'>{t('Reclaimed At')}</dt>
              <dd className='text-right'>
                {(redemption.reclaimed_time ?? 0) > 0
                  ? formatTimestampToDate(redemption.reclaimed_time ?? 0)
                  : '-'}
              </dd>
              <dt className='text-muted-foreground'>{t('Redeemed By')}</dt>
              <dd className='text-right'>
                {redemption.used_user_id > 0
                  ? t('User {{id}}', { id: redemption.used_user_id })
                  : '-'}
              </dd>
              <dt className='text-muted-foreground'>{t('Expires')}</dt>
              <dd className='text-right'>
                {redemption.expired_time > 0
                  ? formatTimestampToDate(redemption.expired_time)
                  : t('Never')}
              </dd>
            </dl>
            {redemption.reclaim_error && (
              <Alert
                variant={
                  redemption.reclaim_status === 3 ? 'default' : 'destructive'
                }
              >
                <AlertTitle>
                  {redemption.reclaim_status === 3
                    ? t('Manual Review Reason')
                    : t('Reclaim Error')}
                </AlertTitle>
                <AlertDescription>{redemption.reclaim_error}</AlertDescription>
              </Alert>
            )}
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}
