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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Spinner } from '@/components/ui/spinner'
import { formatQuota, formatTimestampToDate } from '@/lib/format'

import { getRedemption, retryManualLimitedQuotaReclaim } from '../api'
import { RECLAIM_STATUS } from '../constants'
import type { ReclaimReviewData, Redemption } from '../types'
import { ManualReclaimReviewDialog } from './manual-reclaim-review-dialog'
import { ReclaimStatusBadge } from './reclaim-status-badge'

interface ReclaimDetailsSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  redemption: Redemption | null
  onUpdated?: () => void
}

export function ReclaimDetailsSheet(props: ReclaimDetailsSheetProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const redemptionId = props.redemption?.id
  const [actionError, setActionError] = useState('')
  const [manualReviewOpen, setManualReviewOpen] = useState(false)
  const detailsQuery = useQuery({
    queryKey: ['redemption', 'reclaim-details', redemptionId],
    queryFn: async () => {
      if (!redemptionId) throw new Error('Missing redemption ID')
      const response = await getRedemption(redemptionId)
      if (!response.success || !response.data) {
        throw new Error(
          response.message || t('Failed to load latest reclaim details')
        )
      }
      return response.data
    },
    enabled: props.open && redemptionId !== undefined,
    initialData: props.redemption ?? undefined,
  })
  const retryMutation = useMutation({
    mutationFn: retryManualLimitedQuotaReclaim,
  })
  const redemption = detailsQuery.data ?? props.redemption

  let loadError = ''
  if (detailsQuery.isError) {
    let serverMessage: string | undefined
    if (isAxiosError<{ message?: string }>(detailsQuery.error)) {
      serverMessage = detailsQuery.error.response?.data?.message
    } else if (detailsQuery.error instanceof Error) {
      serverMessage = detailsQuery.error.message
    }
    loadError = serverMessage || t('Failed to load latest reclaim details')
  }

  useEffect(() => {
    setActionError('')
    setManualReviewOpen(false)
  }, [props.open, redemptionId])

  const refreshRelatedData = async () => {
    await detailsQuery.refetch()
    props.onUpdated?.()
    void queryClient.invalidateQueries({
      queryKey: ['redemption-reclaim', 'stats'],
    })
  }

  const handleRetryAutomaticReview = async () => {
    if (!redemption) return
    setActionError('')
    try {
      const response = await retryMutation.mutateAsync(redemption.id)
      if (!response.success || !response.data) {
        setActionError(
          response.message || t('Automatic review could not be completed')
        )
        return
      }
      await refreshRelatedData()
      setActionError('')
      toast.success(t('Automatic review completed'), {
        description: t(
          '{{updated}} records updated; {{reclaimed}} reclaimed.',
          {
            updated: response.data.updated_count,
            reclaimed: formatQuota(response.data.reclaimed_quota),
          }
        ),
      })
    } catch (error) {
      const serverMessage = isAxiosError<{ message?: string }>(error)
        ? error.response?.data?.message
        : undefined
      setActionError(
        serverMessage || t('Automatic review could not be completed')
      )
    }
  }

  const handleManualResolved = async (result: ReclaimReviewData) => {
    await refreshRelatedData()
    setActionError('')
    toast.success(t('Manual review resolved'), {
      description: t('{{updated}} records updated; {{reclaimed}} reclaimed.', {
        updated: result.updated_count,
        reclaimed: formatQuota(result.reclaimed_quota),
      }),
    })
  }

  const walletConsumedQuota = redemption?.used_user_id
    ? Math.max(
        0,
        redemption.quota -
          (redemption.reclaim_remaining_quota ?? 0) -
          (redemption.reclaimed_quota ?? 0)
      )
    : 0

  return (
    <>
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

          {detailsQuery.isLoading && !redemption && (
            <div className='flex items-center gap-2 px-4' role='status'>
              <Spinner />
              <span className='text-muted-foreground text-sm'>
                {t('Loading...')}
              </span>
            </div>
          )}

          {redemption && (
            <div
              className='flex flex-col gap-4 overflow-y-auto px-4 pb-6'
              aria-busy={detailsQuery.isFetching}
            >
              {loadError && (
                <Alert variant='destructive'>
                  <AlertTitle>{t('Latest Details Unavailable')}</AlertTitle>
                  <AlertDescription className='flex flex-col gap-2'>
                    <span>{loadError}</span>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      className='self-start'
                      onClick={() => void detailsQuery.refetch()}
                    >
                      {t('Retry')}
                    </Button>
                  </AlertDescription>
                </Alert>
              )}

              <div className='flex items-center justify-between gap-3 rounded-lg border p-3'>
                <span className='text-muted-foreground text-sm'>
                  {t('Status')}
                </span>
                <ReclaimStatusBadge redemption={redemption} />
              </div>

              {redemption.reclaim_status === RECLAIM_STATUS.MANUAL_REVIEW && (
                <Alert>
                  <AlertTitle>{t('Review Action Required')}</AlertTitle>
                  <AlertDescription className='flex flex-col gap-3'>
                    <p>
                      {redemption.used_user_id > 0
                        ? t(
                            'Retrying replays wallet history and may resolve all manual-review codes for this user. If it still fails, verify the records and enter the remaining quota manually.'
                          )
                        : t(
                            'This record has no redeemed user. Verify the source record, then close it manually with 0 remaining quota.'
                          )}
                    </p>
                    <div className='flex flex-col gap-2 sm:flex-row'>
                      {redemption.used_user_id > 0 && (
                        <Button
                          type='button'
                          variant='outline'
                          onClick={() => void handleRetryAutomaticReview()}
                          disabled={retryMutation.isPending}
                        >
                          {retryMutation.isPending && (
                            <Spinner data-icon='inline-start' />
                          )}
                          {t('Retry Automatic Review')}
                        </Button>
                      )}
                      <Button
                        type='button'
                        onClick={() => setManualReviewOpen(true)}
                        disabled={retryMutation.isPending}
                      >
                        {t('Resolve Manually')}
                      </Button>
                    </div>
                  </AlertDescription>
                </Alert>
              )}

              {actionError && (
                <Alert variant='destructive'>
                  <AlertTitle>
                    {t('Automatic Review Still Needs Attention')}
                  </AlertTitle>
                  <AlertDescription>{actionError}</AlertDescription>
                </Alert>
              )}

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
                  {redemption.reclaim_status === RECLAIM_STATUS.MANUAL_REVIEW
                    ? '-'
                    : formatQuota(walletConsumedQuota)}
                </dd>
                <dt className='text-muted-foreground'>
                  {t('Reclaim Remaining Quota')}
                </dt>
                <dd className='text-right tabular-nums'>
                  {formatQuota(redemption.reclaim_remaining_quota ?? 0)}
                </dd>
                <dt className='text-muted-foreground'>
                  {t('Reclaimed Quota')}
                </dt>
                <dd className='text-right tabular-nums'>
                  {formatQuota(redemption.reclaimed_quota ?? 0)}
                </dd>
                <dt className='text-muted-foreground'>
                  {t('Reclaim Enabled At')}
                </dt>
                <dd className='text-right'>
                  {(redemption.reclaim_enabled_time ?? 0) > 0
                    ? formatTimestampToDate(
                        redemption.reclaim_enabled_time ?? 0
                      )
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

              {(redemption.reclaim_reviewed_by ?? 0) > 0 && (
                <div className='flex flex-col gap-3 rounded-lg border p-4'>
                  <div className='font-medium'>{t('Review Audit')}</div>
                  <dl className='grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-2 text-sm'>
                    <dt className='text-muted-foreground'>
                      {t('Reviewed By')}
                    </dt>
                    <dd className='text-right'>
                      {t('Administrator {{id}}', {
                        id: redemption.reclaim_reviewed_by,
                      })}
                    </dd>
                    <dt className='text-muted-foreground'>
                      {t('Reviewed At')}
                    </dt>
                    <dd className='text-right'>
                      {formatTimestampToDate(
                        redemption.reclaim_reviewed_time ?? 0
                      )}
                    </dd>
                  </dl>
                  {redemption.reclaim_review_note && (
                    <div>
                      <div className='text-muted-foreground text-xs'>
                        {t('Review Note')}
                      </div>
                      <p className='mt-1 text-sm break-words whitespace-pre-wrap'>
                        {redemption.reclaim_review_note}
                      </p>
                    </div>
                  )}
                </div>
              )}

              {redemption.reclaim_error && (
                <Alert
                  variant={
                    redemption.reclaim_status === RECLAIM_STATUS.MANUAL_REVIEW
                      ? 'default'
                      : 'destructive'
                  }
                >
                  <AlertTitle>
                    {redemption.reclaim_status === RECLAIM_STATUS.MANUAL_REVIEW
                      ? t('Manual Review Reason')
                      : t('Reclaim Error')}
                  </AlertTitle>
                  <AlertDescription>
                    {redemption.reclaim_error}
                  </AlertDescription>
                </Alert>
              )}
            </div>
          )}
        </SheetContent>
      </Sheet>

      {redemption && manualReviewOpen && (
        <ManualReclaimReviewDialog
          open={manualReviewOpen}
          onOpenChange={setManualReviewOpen}
          redemption={redemption}
          onResolved={handleManualResolved}
        />
      )}
    </>
  )
}
