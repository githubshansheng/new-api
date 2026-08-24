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
import { Wallet02Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Activity, BarChart3, ChevronDown, WalletCards } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { LimitedQuotaSummary, UserWalletData } from '../types'

interface WalletStatsCardProps {
  user: UserWalletData | null
  loading?: boolean
  limitedQuota?: LimitedQuotaSummary
  limitedLoading?: boolean
  limitedError?: boolean
  onRetryLimited?: () => void
  highlightedExpiredTime?: number | null
}

export function WalletStatsCard(props: WalletStatsCardProps) {
  const { t } = useTranslation()
  const [expanded, setExpanded] = useState(false)
  const [currentTime, setCurrentTime] = useState(() =>
    Math.floor(Date.now() / 1000)
  )
  const limitedTotal = Math.max(0, props.limitedQuota?.total ?? 0)
  const canExpand =
    !props.limitedLoading && !props.limitedError && limitedTotal > 0

  useEffect(() => {
    if (
      props.highlightedExpiredTime !== null &&
      props.highlightedExpiredTime !== undefined
    ) {
      setExpanded(true)
    }
  }, [props.highlightedExpiredTime])

  useEffect(() => {
    if ((props.limitedQuota?.groups.length ?? 0) === 0) return
    const timer = window.setInterval(
      () => setCurrentTime(Math.floor(Date.now() / 1000)),
      60_000
    )
    return () => window.clearInterval(timer)
  }, [props.limitedQuota?.groups.length])

  const nearestGroup = props.limitedQuota?.groups[0]
  const nearestRemainingSeconds = nearestGroup
    ? Math.max(0, nearestGroup.expired_time - currentTime)
    : 0
  const nearestExpiresWithin24Hours =
    nearestRemainingSeconds > 0 && nearestRemainingSeconds <= 86_400
  const formatRemainingTime = (expiredTime: number) => {
    const remainingSeconds = Math.max(0, expiredTime - currentTime)
    if (remainingSeconds >= 86_400) {
      return t('{{count}} days remaining', {
        count: Math.ceil(remainingSeconds / 86_400),
      })
    }
    return t('{{count}} hours remaining', {
      count: Math.max(1, Math.ceil(remainingSeconds / 3_600)),
    })
  }

  const stats: {
    label: string
    value: string
    description: string
    icon: typeof WalletCards
    tone: IconBadgeTone
  }[] = [
    {
      label: t('Current Balance'),
      value: formatQuota(props.user?.quota ?? 0),
      description: t('Remaining quota'),
      icon: WalletCards,
      tone: 'success',
    },
    {
      label: t('Total Usage'),
      value: formatQuota(props.user?.used_quota ?? 0),
      description: t('Total consumed quota'),
      icon: BarChart3,
      tone: 'info',
    },
    {
      label: t('API Requests'),
      value: (props.user?.request_count ?? 0).toLocaleString(),
      description: t('Total requests made'),
      icon: Activity,
      tone: 'chart-4',
    },
  ]
  let limitedQuotaValue = (
    <div className='text-foreground mt-1.5 font-mono text-sm font-bold tracking-tight break-all tabular-nums sm:mt-2.5 sm:text-2xl'>
      {formatQuota(limitedTotal)}
    </div>
  )
  if (props.limitedLoading) {
    limitedQuotaValue = <Skeleton className='mt-2 h-7 w-full' />
  } else if (props.limitedError) {
    limitedQuotaValue = (
      <div className='text-foreground mt-1.5 font-mono text-sm font-bold tracking-tight tabular-nums sm:mt-2.5 sm:text-2xl'>
        --
      </div>
    )
  }
  let limitedQuotaDescription = t('No limited quota')
  if (props.limitedError) {
    limitedQuotaDescription = t('Limited quota is temporarily unavailable.')
  } else if (nearestGroup) {
    if (nearestExpiresWithin24Hours) {
      limitedQuotaDescription = t('Expiring soon: {{time}}', {
        time: formatTimestampToDate(nearestGroup.expired_time),
      })
    } else {
      limitedQuotaDescription = t('Nearest expiry: {{time}}', {
        time: formatTimestampToDate(nearestGroup.expired_time),
      })
    }
  }

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='grid grid-cols-2 lg:grid-cols-4'>
        <div className='min-w-0 border-r border-b px-2.5 py-2.5 sm:px-5 sm:py-4 lg:border-b-0'>
          <div className='flex items-center gap-1.5 sm:gap-2.5'>
            <IconBadge tone={stats[0].tone} size='stat'>
              <WalletCards />
            </IconBadge>
            <div className='text-muted-foreground truncate text-[11px] font-medium tracking-wider uppercase sm:text-xs'>
              {stats[0].label}
            </div>
          </div>
          {props.loading ? (
            <Skeleton className='mt-2 h-7 w-full' />
          ) : (
            <div className='text-foreground mt-1.5 font-mono text-sm font-bold tracking-tight break-all tabular-nums sm:mt-2.5 sm:text-2xl'>
              {stats[0].value}
            </div>
          )}
          <div className='text-muted-foreground/60 mt-1 hidden text-xs md:block'>
            {stats[0].description}
          </div>
        </div>

        <button
          type='button'
          className='hover:bg-muted/40 focus-visible:ring-ring min-w-0 border-b px-2.5 py-2.5 text-left outline-none focus-visible:ring-2 disabled:cursor-default disabled:opacity-70 sm:px-5 sm:py-4 lg:border-r lg:border-b-0'
          disabled={!canExpand}
          aria-expanded={canExpand ? expanded : false}
          aria-controls='limited-quota-groups'
          onClick={() => setExpanded((current) => !current)}
        >
          <div className='flex items-center gap-1.5 sm:gap-2.5'>
            <IconBadge tone='warning' size='stat'>
              <HugeiconsIcon icon={Wallet02Icon} strokeWidth={2} />
            </IconBadge>
            <div className='text-muted-foreground min-w-0 flex-1 truncate text-[11px] font-medium tracking-wider uppercase sm:text-xs'>
              {t('Balance - Limited Quota')}
            </div>
            {canExpand && (
              <ChevronDown
                className={cn(
                  'size-4 shrink-0 transition-transform',
                  expanded && 'rotate-180'
                )}
                aria-hidden='true'
              />
            )}
          </div>
          {limitedQuotaValue}
          <div
            className={cn(
              'text-muted-foreground/70 mt-1 text-[10px] leading-tight sm:text-xs',
              nearestExpiresWithin24Hours && 'text-warning'
            )}
          >
            {limitedQuotaDescription}
          </div>
        </button>

        {stats.slice(1).map((item, index) => (
          <div
            key={item.label}
            className={cn(
              'min-w-0 px-2.5 py-2.5 sm:px-5 sm:py-4',
              index === 0 && 'border-r'
            )}
          >
            <div className='flex items-center gap-1.5 sm:gap-2.5'>
              <IconBadge tone={item.tone} size='stat'>
                <item.icon />
              </IconBadge>
              <div className='text-muted-foreground truncate text-[11px] font-medium tracking-wider uppercase sm:text-xs'>
                {item.label}
              </div>
            </div>
            {props.loading ? (
              <Skeleton className='mt-2 h-7 w-full' />
            ) : (
              <div className='text-foreground mt-1.5 font-mono text-sm font-bold tracking-tight break-all tabular-nums sm:mt-2.5 sm:text-2xl'>
                {item.value}
              </div>
            )}
            <div className='text-muted-foreground/60 mt-1 hidden text-xs md:block'>
              {item.description}
            </div>
          </div>
        ))}
      </div>

      {props.limitedError && (
        <div className='flex items-center justify-between gap-3 border-t px-3 py-2 text-sm'>
          <span className='text-muted-foreground'>
            {t('Limited quota is temporarily unavailable.')}
          </span>
          <Button size='sm' variant='outline' onClick={props.onRetryLimited}>
            {t('Retry')}
          </Button>
        </div>
      )}

      {expanded && canExpand && (
        <div id='limited-quota-groups' className='flex flex-col border-t'>
          {(props.limitedQuota?.groups ?? []).map((group) => {
            const expiresWithin24Hours =
              group.expired_time > currentTime &&
              group.expired_time <= currentTime + 86_400
            return (
              <div
                key={group.expired_time}
                className={cn(
                  'flex flex-wrap items-center justify-between gap-2 border-b px-3 py-2.5 last:border-b-0 sm:px-5',
                  props.highlightedExpiredTime === group.expired_time &&
                    'bg-warning/10'
                )}
                data-highlighted={
                  props.highlightedExpiredTime === group.expired_time ||
                  undefined
                }
              >
                <div className='min-w-0'>
                  <div className='text-sm font-medium'>
                    {formatTimestampToDate(group.expired_time)}
                  </div>
                  <div className='text-muted-foreground text-xs'>
                    {formatRemainingTime(group.expired_time)}
                  </div>
                </div>
                <div className='flex items-center gap-2'>
                  {expiresWithin24Hours && (
                    <StatusBadge
                      label={t('Expires within 24 hours')}
                      variant='warning'
                      copyable={false}
                    />
                  )}
                  <span className='font-mono text-sm font-semibold tabular-nums'>
                    {formatQuota(group.remaining_quota)}
                  </span>
                </div>
              </div>
            )
          })}
          <div className='bg-muted/40 text-muted-foreground border-t px-3 py-2.5 text-xs sm:px-5'>
            {t(
              'Unused limited quota will be reclaimed at expiry. Limited quota cannot be used to purchase subscriptions.'
            )}
          </div>
        </div>
      )}
    </div>
  )
}
