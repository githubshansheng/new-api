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
  Copy01Icon,
  Refresh01Icon,
  ShieldKeyIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import type { TFunction } from 'i18next'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Spinner } from '@/components/ui/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { formatQuota, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import { enableLimitedQuotaReclaim, previewLimitedQuotaReclaim } from '../api'
import { parseReclaimKeys } from '../lib'
import type {
  ReclaimPreviewData,
  ReclaimPreviewItem,
  ReclaimPreviewResult,
} from '../types'

interface LimitedQuotaReclaimDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onEnabled: () => void
}

type PreviewFilter = 'all' | 'eligible' | 'manual_review' | 'invalid'

const INVALID_PREVIEW_RESULTS = new Set<ReclaimPreviewResult>([
  'not_found',
  'no_expiry',
  'expired_unused',
  'conflict',
])

function reclaimResultLabel(
  result: ReclaimPreviewResult,
  t: TFunction
): string {
  switch (result) {
    case 'eligible':
      return t('Eligible')
    case 'already_enabled':
      return t('Already Enabled')
    case 'manual_review':
      return t('Manual Review')
    case 'not_found':
      return t('Not Found')
    case 'no_expiry':
      return t('No Expiry')
    case 'expired_unused':
      return t('Expired Unused')
    case 'conflict':
      return t('Conflict')
  }
}

function redemptionStatusLabel(status: number | undefined, t: TFunction) {
  switch (status) {
    case 1:
      return t('Unused')
    case 2:
      return t('Disabled')
    case 3:
      return t('Used')
    default:
      return '-'
  }
}

function ReclaimResultBadge(props: { result: ReclaimPreviewResult }) {
  const { t } = useTranslation()
  let variant: 'success' | 'neutral' | 'warning' | 'danger' = 'neutral'
  if (props.result === 'eligible') variant = 'success'
  if (props.result === 'manual_review' || props.result === 'already_enabled') {
    variant = 'warning'
  }
  if (
    props.result === 'not_found' ||
    props.result === 'no_expiry' ||
    props.result === 'conflict' ||
    props.result === 'expired_unused'
  ) {
    variant = 'danger'
  }
  return (
    <StatusBadge
      label={reclaimResultLabel(props.result, t)}
      variant={variant}
      copyable={false}
    />
  )
}

function previewItemChanged(
  previous: ReclaimPreviewItem | undefined,
  next: ReclaimPreviewItem
): boolean {
  if (!previous) return true
  return (
    previous.id !== next.id ||
    previous.name !== next.name ||
    previous.redemption_status !== next.redemption_status ||
    previous.quota !== next.quota ||
    previous.used_user_id !== next.used_user_id ||
    previous.username !== next.username ||
    previous.redeemed_time !== next.redeemed_time ||
    previous.result !== next.result ||
    previous.remaining_quota !== next.remaining_quota ||
    previous.expired_time !== next.expired_time ||
    previous.reason !== next.reason
  )
}

export function LimitedQuotaReclaimDialog(
  props: LimitedQuotaReclaimDialogProps
) {
  const { t } = useTranslation()
  const { copyToClipboard } = useCopyToClipboard()
  const [source, setSource] = useState('')
  const [preview, setPreview] = useState<ReclaimPreviewData | null>(null)
  const [resultFilter, setResultFilter] = useState<PreviewFilter>('all')
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [changedKeys, setChangedKeys] = useState<Set<string>>(new Set())
  const parsed = useMemo(() => parseReclaimKeys(source), [source])

  const previewMutation = useMutation({
    mutationFn: previewLimitedQuotaReclaim,
  })
  const enableMutation = useMutation({
    mutationFn: enableLimitedQuotaReclaim,
  })

  const filteredItems = useMemo(() => {
    if (!preview || resultFilter === 'all') return preview?.items ?? []
    if (resultFilter === 'invalid') {
      return preview.items.filter((item) =>
        INVALID_PREVIEW_RESULTS.has(item.result)
      )
    }
    return preview.items.filter((item) => item.result === resultFilter)
  }, [preview, resultFilter])
  const confirmableCount =
    preview?.items.filter((item) => item.can_enable).length ?? 0

  const handlePreview = async (previous?: ReclaimPreviewData) => {
    if (parsed.keys.length === 0) return
    try {
      const result = await previewMutation.mutateAsync(parsed.keys)
      if (!result.success || !result.data) {
        toast.error(result.message || t('Failed to preview reclaim changes'))
        return
      }

      if (previous) {
        const previousItems = new Map(
          previous.items.map((item) => [item.key, item])
        )
        setChangedKeys(
          new Set(
            result.data.items
              .filter((item) =>
                previewItemChanged(previousItems.get(item.key), item)
              )
              .map((item) => item.key)
          )
        )
      } else {
        setChangedKeys(new Set())
      }
      setPreview(result.data)
      setResultFilter('all')
    } catch {
      toast.error(t('Failed to preview reclaim changes'))
    }
  }

  const handleEnable = async () => {
    if (!preview) return
    try {
      const result = await enableMutation.mutateAsync({
        keys: parsed.keys,
        snapshot: preview.snapshot,
      })
      if (!result.success || !result.data) {
        toast.error(
          result.message || t('Failed to enable limited quota reclaim')
        )
        return
      }
      toast.success(
        t(
          'Enabled {{enabled}} codes; {{review}} need manual review; {{skipped}} skipped',
          {
            enabled: result.data.enabled_count,
            review: result.data.manual_review_count,
            skipped: result.data.skipped_count,
          }
        )
      )
      setConfirmOpen(false)
      props.onEnabled()
      props.onOpenChange(false)
    } catch (error: unknown) {
      if (isAxiosError(error) && error.response?.status === 409) {
        setConfirmOpen(false)
        toast.error(
          t(
            'The preview changed. Review the highlighted rows and confirm again.'
          )
        )
        await handlePreview(preview)
        return
      }
      toast.error(t('Failed to enable limited quota reclaim'))
    }
  }

  const handleOpenChange = (open: boolean) => {
    props.onOpenChange(open)
    if (!open) {
      setSource('')
      setPreview(null)
      setResultFilter('all')
      setConfirmOpen(false)
      setChangedKeys(new Set())
      previewMutation.reset()
      enableMutation.reset()
    }
  }

  const exceptionItems =
    preview?.items.filter(
      (item) => item.result !== 'eligible' && item.result !== 'already_enabled'
    ) ?? []

  return (
    <>
      <Dialog
        open={props.open}
        onOpenChange={handleOpenChange}
        title={t('Enable Limited Quota Reclaim')}
        description={t(
          'Preview each redemption code before enabling time-limited quota reclaim.'
        )}
        contentClassName='max-sm:h-[calc(100dvh-1rem)] max-sm:w-[calc(100vw-1rem)] sm:max-w-6xl'
        contentHeight='min(78vh, 760px)'
        footer={
          preview ? (
            <>
              <Button variant='outline' onClick={() => setPreview(null)}>
                {t('Back')}
              </Button>
              <Button
                onClick={() => setConfirmOpen(true)}
                disabled={
                  enableMutation.isPending ||
                  preview.summary.code_count === 0 ||
                  confirmableCount === 0
                }
              >
                <HugeiconsIcon
                  icon={ShieldKeyIcon}
                  strokeWidth={2}
                  data-icon='inline-start'
                />
                {t('Confirm Enable {{count}} Codes', {
                  count: confirmableCount,
                })}
              </Button>
            </>
          ) : (
            <>
              <Button
                variant='outline'
                onClick={() => handleOpenChange(false)}
                disabled={previewMutation.isPending}
              >
                {t('Cancel')}
              </Button>
              <Button
                onClick={() => void handlePreview()}
                disabled={parsed.keys.length === 0 || previewMutation.isPending}
              >
                {previewMutation.isPending && (
                  <Spinner data-icon='inline-start' />
                )}
                {t('Preview')}
              </Button>
            </>
          )
        }
      >
        {!preview ? (
          <div className='flex flex-col gap-4'>
            <label htmlFor='reclaim-keys' className='text-sm font-medium'>
              {t('Redemption Code Keys')}
            </label>
            <Textarea
              id='reclaim-keys'
              value={source}
              onChange={(event) => setSource(event.target.value)}
              placeholder={t('Paste keys separated by new lines or commas')}
              className='min-h-72 font-mono'
            />
            <div className='bg-muted/40 grid grid-cols-2 gap-3 rounded-lg border p-3 text-sm'>
              <div>
                <div className='text-muted-foreground'>{t('Recognized')}</div>
                <div className='font-semibold tabular-nums'>
                  {parsed.recognizedCount}
                </div>
              </div>
              <div>
                <div className='text-muted-foreground'>
                  {t('Duplicates Removed')}
                </div>
                <div className='font-semibold tabular-nums'>
                  {parsed.duplicateCount}
                </div>
              </div>
            </div>
          </div>
        ) : (
          <div className='flex flex-col gap-4'>
            {changedKeys.size > 0 && (
              <div className='border-warning/40 bg-warning/10 flex items-start gap-2 rounded-lg border p-3 text-sm'>
                <HugeiconsIcon
                  icon={Alert02Icon}
                  strokeWidth={2}
                  className='text-warning mt-0.5 size-4 shrink-0'
                  aria-hidden='true'
                />
                <span>
                  {t(
                    '{{count}} preview rows changed since the last confirmation.',
                    { count: changedKeys.size }
                  )}
                </span>
              </div>
            )}

            <div className='grid grid-cols-2 gap-2 sm:grid-cols-5'>
              {[
                [t('Codes'), preview.summary.code_count],
                [t('Users'), preview.summary.user_count],
                [t('Total Quota'), formatQuota(preview.summary.total_quota)],
                [t('Deadlines'), preview.summary.deadline_count],
                [t('Exceptions'), preview.summary.exception_count],
              ].map(([label, value]) => (
                <div
                  key={String(label)}
                  className='bg-muted/40 rounded-lg border p-3'
                >
                  <div className='text-muted-foreground text-xs'>{label}</div>
                  <div className='mt-1 font-semibold tabular-nums'>{value}</div>
                </div>
              ))}
            </div>

            <div className='flex flex-wrap items-center justify-between gap-2'>
              <NativeSelect
                value={resultFilter}
                onChange={(event) =>
                  setResultFilter(event.target.value as PreviewFilter)
                }
                aria-label={t('Filter preview result')}
              >
                <NativeSelectOption value='all'>{t('All')}</NativeSelectOption>
                <NativeSelectOption value='eligible'>
                  {t('Eligible')}
                </NativeSelectOption>
                <NativeSelectOption value='manual_review'>
                  {t('Manual Review')}
                </NativeSelectOption>
                <NativeSelectOption value='invalid'>
                  {t('Invalid')}
                </NativeSelectOption>
              </NativeSelect>
              <div className='flex gap-2'>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => void handlePreview(preview)}
                  disabled={previewMutation.isPending}
                >
                  <HugeiconsIcon
                    icon={Refresh01Icon}
                    strokeWidth={2}
                    data-icon='inline-start'
                  />
                  {t('Refresh Preview')}
                </Button>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() =>
                    void copyToClipboard(
                      exceptionItems
                        .map(
                          (item) =>
                            `${item.key}\t${item.result}\t${item.reason ?? ''}`
                        )
                        .join('\n')
                    )
                  }
                  disabled={exceptionItems.length === 0}
                >
                  <HugeiconsIcon
                    icon={Copy01Icon}
                    strokeWidth={2}
                    data-icon='inline-start'
                  />
                  {t('Copy Exceptions')}
                </Button>
              </div>
            </div>

            <div className='overflow-x-auto rounded-lg border'>
              <Table className='min-w-[72rem]'>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('Code')}</TableHead>
                    <TableHead>{t('Redemption Status / User')}</TableHead>
                    <TableHead>{t('Issued Quota')}</TableHead>
                    <TableHead>{t('Redeemed At')}</TableHead>
                    <TableHead>{t('Expires')}</TableHead>
                    <TableHead>
                      {t('Estimated Limited Quota Remaining')}
                    </TableHead>
                    <TableHead>{t('Result')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filteredItems.map((item) => (
                    <TableRow
                      key={item.key}
                      className={cn(
                        changedKeys.has(item.key) && 'bg-warning/10'
                      )}
                      data-changed={changedKeys.has(item.key) || undefined}
                    >
                      <TableCell
                        className='max-w-52 truncate font-mono'
                        title={item.key}
                      >
                        {item.key}
                      </TableCell>
                      <TableCell>
                        <div className='flex flex-col gap-0.5'>
                          <span>
                            {redemptionStatusLabel(item.redemption_status, t)}
                          </span>
                          <span className='text-muted-foreground text-xs'>
                            {item.username ||
                              (item.used_user_id > 0
                                ? t('User {{id}}', {
                                    id: item.used_user_id,
                                  })
                                : '-')}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell>{formatQuota(item.quota)}</TableCell>
                      <TableCell>
                        {item.redeemed_time > 0
                          ? formatTimestampToDate(item.redeemed_time)
                          : '-'}
                      </TableCell>
                      <TableCell>
                        {item.expired_time > 0
                          ? formatTimestampToDate(item.expired_time)
                          : t('Never')}
                      </TableCell>
                      <TableCell>{formatQuota(item.remaining_quota)}</TableCell>
                      <TableCell className='max-w-64 whitespace-normal'>
                        <div className='flex flex-col items-start gap-1'>
                          <ReclaimResultBadge result={item.result} />
                          {item.reason && (
                            <span className='text-muted-foreground text-xs'>
                              {item.reason}
                            </span>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        )}
      </Dialog>

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t('Confirm Limited Quota Reclaim')}
            </AlertDialogTitle>
            <AlertDialogDescription>
              <span className='flex flex-col gap-2'>
                <span>
                  {t(
                    'Each redemption code will be reclaimed according to its own expiration time.'
                  )}
                </span>
                <span>
                  {t(
                    "After expiry, any unused quota will be deducted from the user's wallet balance."
                  )}
                </span>
              </span>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={enableMutation.isPending}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={() => void handleEnable()}
              disabled={enableMutation.isPending}
            >
              {enableMutation.isPending && <Spinner data-icon='inline-start' />}
              {t('Confirm Enable {{count}} Codes', {
                count: confirmableCount,
              })}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
