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
import {
  Alert02Icon,
  ChartBarLineIcon,
  Refresh01Icon,
  ShieldKeyIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { VChart } from '@visactor/react-vchart'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { VCHART_OPTION } from '@/lib/vchart'

import { getLimitedQuotaReclaimStats } from '../../api'

type ReclaimSearchStatus =
  | '0'
  | '1'
  | '2'
  | '3'
  | '4'
  | 'active'
  | 'due'
  | 'attention'

const RECLAIM_SEARCH_STATUSES = new Set<ReclaimSearchStatus>([
  '0',
  '1',
  '2',
  '3',
  '4',
  'active',
  'due',
  'attention',
])

function LimitedQuotaReclaimPanelSkeleton() {
  return (
    <Card>
      <CardHeader>
        <Skeleton className='h-5 w-64' />
        <Skeleton className='h-4 w-96 max-w-full' />
      </CardHeader>
      <CardContent className='flex flex-col gap-4'>
        <div className='grid grid-cols-2 gap-2 lg:grid-cols-4'>
          {['active', 'expiring', 'reclaimed', 'attention'].map((key) => (
            <Skeleton key={key} className='h-24 rounded-lg' />
          ))}
        </div>
        <Skeleton className='h-56 w-full rounded-lg' />
      </CardContent>
    </Card>
  )
}

export function LimitedQuotaReclaimPanel() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const query = useQuery({
    queryKey: ['redemption-reclaim', 'stats'],
    queryFn: async () => {
      const response = await getLimitedQuotaReclaimStats()
      if (!response.success || !response.data) {
        throw new Error(response.message || 'Failed to load reclaim statistics')
      }
      return response.data
    },
    refetchInterval: () =>
      typeof document !== 'undefined' && document.visibilityState === 'visible'
        ? 60_000
        : false,
    refetchOnWindowFocus: true,
  })

  if (query.isLoading) return <LimitedQuotaReclaimPanelSkeleton />

  const data = query.data
  const navigateToCodes = (
    reclaimStatus: ReclaimSearchStatus,
    expireStart: number,
    expireEnd: number
  ) => {
    void navigate({
      to: '/redemption-codes',
      search: {
        reclaimStatus: [reclaimStatus],
        expireStart,
        expireEnd,
      },
    })
  }

  const now = data?.updated_at ?? Math.floor(Date.now() / 1000)
  const attentionCount = data
    ? data.overdue_count + data.manual_review_count + data.error_count
    : 0
  const isEmpty = Boolean(
    data &&
    data.active_quota === 0 &&
    data.expiring_24h_quota === 0 &&
    data.reclaimed_today_quota === 0 &&
    attentionCount === 0 &&
    data.trend.length === 0 &&
    data.attention.length === 0
  )
  const metricButtons = data
    ? [
        {
          label: t('Active Limited Quota'),
          value: formatQuota(data.active_quota),
          detail: t('{{count}} users', { count: data.active_users }),
          onClick: () => navigateToCodes('active', 0, 0),
          warning: false,
        },
        {
          label: t('Expiring in 24 Hours'),
          value: formatQuota(data.expiring_24h_quota),
          detail: t('{{count}} codes', { count: data.expiring_24h_codes }),
          onClick: () => navigateToCodes('active', now, now + 86_400),
          warning: false,
        },
        {
          label: t('Reclaimed Today'),
          value: formatQuota(data.reclaimed_today_quota),
          detail: t('{{count}} users', { count: data.reclaimed_today_users }),
          onClick: () =>
            navigateToCodes('2', data.today_start, data.updated_at),
          warning: false,
        },
        {
          label: t('Needs Attention'),
          value: attentionCount.toLocaleString(),
          detail: t(
            '{{overdue}} overdue · {{review}} review · {{errors}} errors',
            {
              overdue: data.overdue_count,
              review: data.manual_review_count,
              errors: data.error_count,
            }
          ),
          onClick: () => navigateToCodes('attention', 0, 0),
          warning: attentionCount > 0,
        },
      ]
    : []

  const trend = (data?.trend ?? []).slice(0, 7)
  const chartSpec = {
    type: 'bar' as const,
    data: [{ id: 'trend', values: trend }],
    xField: 'date',
    yField: 'quota',
    seriesField: 'date',
    legends: { visible: false },
    axes: [
      { orient: 'bottom' as const, label: { autoRotate: true } },
      { orient: 'left' as const, label: { formatMethod: formatQuota } },
    ],
    tooltip: {
      dimension: {
        content: [
          {
            key: t('Quota'),
            value: (datum: { quota: number }) => formatQuota(datum.quota),
          },
          {
            key: t('Codes'),
            value: (datum: { code_count: number }) => datum.code_count,
          },
          {
            key: t('Users'),
            value: (datum: { user_count: number }) => datum.user_count,
          },
        ],
      },
    },
    background: 'transparent',
  }

  const handleTrendClick = (event: {
    datum?: { start_time?: number; end_time?: number }
  }) => {
    const startTime = event.datum?.start_time
    const endTime = event.datum?.end_time
    if (typeof startTime !== 'number' || typeof endTime !== 'number') return
    navigateToCodes('active', startTime, endTime)
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2'>
          <HugeiconsIcon
            icon={ShieldKeyIcon}
            strokeWidth={2}
            className='size-5'
            aria-hidden='true'
          />
          {t('Limited Quota Reclaim')}
        </CardTitle>
        <CardDescription>
          {t('Monitor active deadlines, reclaimed quota, and exceptions.')}
        </CardDescription>
        <CardAction>
          <div className='flex flex-wrap items-center justify-end gap-2'>
            {data?.updated_at ? (
              <span
                className='text-muted-foreground text-xs whitespace-nowrap'
                aria-live='polite'
              >
                {t('Last updated:')} {formatTimestampToDate(data.updated_at)}
              </span>
            ) : null}
            <Button
              variant='outline'
              size='sm'
              onClick={() => void query.refetch()}
              disabled={query.isFetching}
            >
              <HugeiconsIcon
                icon={Refresh01Icon}
                strokeWidth={2}
                data-icon='inline-start'
              />
              {t('Refresh')}
            </Button>
            <Button
              variant='ghost'
              size='sm'
              onClick={() =>
                void navigate({
                  to: '/redemption-codes',
                  search: {
                    reclaimStatus: [],
                    expireStart: undefined,
                    expireEnd: undefined,
                  },
                })
              }
            >
              {t('View All')}
            </Button>
          </div>
        </CardAction>
      </CardHeader>
      <CardContent className='flex flex-col gap-4'>
        {query.isError && (
          <Alert variant='destructive'>
            <AlertDescription className='flex flex-wrap items-center justify-between gap-2'>
              <span>
                {t('Failed to load limited quota reclaim statistics.')}
              </span>
              <Button
                variant='outline'
                size='sm'
                onClick={() => void query.refetch()}
              >
                {t('Retry')}
              </Button>
            </AlertDescription>
          </Alert>
        )}

        {isEmpty && (
          <Empty className='min-h-52 border'>
            <EmptyHeader>
              <EmptyMedia variant='icon'>
                <HugeiconsIcon icon={ChartBarLineIcon} strokeWidth={2} />
              </EmptyMedia>
              <EmptyTitle>{t('No Limited Quota Reclaim Data')}</EmptyTitle>
              <EmptyDescription>
                {t(
                  'Reclaim activity will appear here after it is enabled for redemption codes.'
                )}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}

        {data && !isEmpty && (
          <>
            <div className='grid grid-cols-2 gap-2 lg:grid-cols-4'>
              {metricButtons.map((metric) => (
                <button
                  key={metric.label}
                  type='button'
                  onClick={metric.onClick}
                  className={cn(
                    'hover:bg-muted/50 focus-visible:ring-ring rounded-lg border p-3 text-left transition-colors outline-none focus-visible:ring-2',
                    metric.warning &&
                      'border-destructive/40 bg-destructive/5 text-destructive'
                  )}
                >
                  <div className='text-muted-foreground text-xs'>
                    {metric.label}
                  </div>
                  <div className='mt-1 font-mono text-lg font-semibold tabular-nums sm:text-xl'>
                    {metric.value}
                  </div>
                  <div className='text-muted-foreground mt-1 text-xs'>
                    {metric.detail}
                  </div>
                </button>
              ))}
            </div>

            <div className='grid gap-4 lg:grid-cols-[minmax(0,1fr)_22rem]'>
              <div className='min-w-0 overflow-hidden rounded-lg border'>
                <div className='border-b px-4 py-3 text-sm font-medium'>
                  {t('Next 7 Days')}
                </div>
                <div className='h-64 p-2'>
                  {trend.length > 0 ? (
                    <VChart
                      spec={chartSpec}
                      option={VCHART_OPTION}
                      onClick={handleTrendClick}
                    />
                  ) : (
                    <div className='text-muted-foreground flex h-full items-center justify-center text-sm'>
                      {t('No upcoming reclaim deadlines')}
                    </div>
                  )}
                </div>
              </div>

              <div className='overflow-hidden rounded-lg border'>
                <div className='border-b px-4 py-3 text-sm font-medium'>
                  {t('Recent Expirations / Exceptions')}
                </div>
                <div className='flex flex-col'>
                  {data.attention.slice(0, 5).map((item) => {
                    const rawStatus = String(item.status)
                    const hasExpiry = item.expired_time > 0
                    let reclaimStatus: ReclaimSearchStatus = 'attention'
                    if (
                      RECLAIM_SEARCH_STATUSES.has(
                        rawStatus as ReclaimSearchStatus
                      )
                    ) {
                      reclaimStatus = rawStatus as ReclaimSearchStatus
                    }
                    return (
                      <button
                        key={`${item.expired_time}-${item.status}`}
                        type='button'
                        className='hover:bg-muted/50 flex items-center justify-between gap-3 border-b px-4 py-3 text-left last:border-b-0'
                        onClick={() =>
                          navigateToCodes(
                            reclaimStatus,
                            hasExpiry ? item.expired_time : 0,
                            hasExpiry ? item.expired_time : 0
                          )
                        }
                      >
                        <span className='min-w-0'>
                          <span className='block text-sm font-medium'>
                            {hasExpiry
                              ? formatTimestampToDate(item.expired_time)
                              : t('No Expiry')}
                          </span>
                          <span className='text-muted-foreground block text-xs'>
                            {t('{{codes}} codes · {{users}} users', {
                              codes: item.code_count,
                              users: item.user_count,
                            })}
                          </span>
                        </span>
                        <span className='shrink-0 font-mono text-xs'>
                          {formatQuota(item.quota)}
                        </span>
                      </button>
                    )
                  })}
                  {data.attention.length === 0 && (
                    <div className='text-muted-foreground flex min-h-28 items-center justify-center px-4 text-sm'>
                      <HugeiconsIcon
                        icon={Alert02Icon}
                        strokeWidth={2}
                        className='mr-2 size-4'
                        aria-hidden='true'
                      />
                      {t('No items need attention')}
                    </div>
                  )}
                </div>
              </div>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
