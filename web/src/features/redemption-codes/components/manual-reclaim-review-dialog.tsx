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
import { useMutation } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { Textarea } from '@/components/ui/textarea'
import { getCurrencyLabel } from '@/lib/currency'
import {
  formatQuota,
  formatTimestampToDate,
  getEditableQuotaStep,
  parseQuotaFromDollars,
  quotaUnitsToEditableAmount,
} from '@/lib/format'

import { resolveManualLimitedQuotaReclaim } from '../api'
import type { ReclaimReviewData, Redemption } from '../types'

interface ManualReclaimReviewDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  redemption: Redemption
  onResolved: (result: ReclaimReviewData) => void | Promise<void>
}

export function ManualReclaimReviewDialog(
  props: ManualReclaimReviewDialogProps
) {
  const { t } = useTranslation()
  const [step, setStep] = useState<'form' | 'confirm'>('form')
  const [amount, setAmount] = useState('')
  const [note, setNote] = useState('')
  const [validationError, setValidationError] = useState<{
    field: 'amount' | 'note'
    message: string
  } | null>(null)
  const [requestError, setRequestError] = useState('')
  const resolveMutation = useMutation({
    mutationFn: (data: { remaining_quota: number; note: string }) =>
      resolveManualLimitedQuotaReclaim(props.redemption.id, data),
  })
  const submitting = resolveMutation.isPending

  const parsedAmount = Number(amount)
  const remainingQuota =
    amount.trim() !== '' && Number.isFinite(parsedAmount)
      ? parseQuotaFromDollars(parsedAmount)
      : -1
  const noteBytes = new TextEncoder().encode(note.trim()).length
  const expired =
    props.redemption.expired_time > 0 &&
    props.redemption.expired_time < Math.floor(Date.now() / 1000)
  const missingRedeemedUser = props.redemption.used_user_id <= 0

  const validate = () => {
    if (
      amount.trim() === '' ||
      !Number.isFinite(parsedAmount) ||
      parsedAmount < 0 ||
      remainingQuota < 0 ||
      remainingQuota > props.redemption.quota
    ) {
      setValidationError({
        field: 'amount',
        message: t(
          'Remaining limited quota must be between 0 and the issued quota.'
        ),
      })
      return false
    }
    if (missingRedeemedUser && remainingQuota !== 0) {
      setValidationError({
        field: 'amount',
        message: t(
          'A record without a redeemed user can only be closed with 0 remaining quota.'
        ),
      })
      return false
    }
    if (note.trim() === '' || noteBytes > 2000) {
      setValidationError({
        field: 'note',
        message: t('Review note is required and must not exceed 2000 bytes.'),
      })
      return false
    }
    setValidationError(null)
    return true
  }

  const handleReviewImpact = () => {
    if (!validate()) return
    setRequestError('')
    setStep('confirm')
  }

  const handleSubmit = async () => {
    if (!validate()) {
      setStep('form')
      return
    }
    setRequestError('')
    try {
      const response = await resolveMutation.mutateAsync({
        remaining_quota: remainingQuota,
        note: note.trim(),
      })
      if (!response.success || !response.data) {
        setRequestError(
          response.message || t('Failed to resolve manual review')
        )
        return
      }
      await props.onResolved(response.data)
      props.onOpenChange(false)
    } catch (error) {
      const serverMessage = isAxiosError<{ message?: string }>(error)
        ? error.response?.data?.message
        : undefined
      setRequestError(serverMessage || t('Failed to resolve manual review'))
    }
  }

  let impactMessage = t(
    'The verified quota will remain active until {{time}} and any unused amount will then be reclaimed.',
    { time: formatTimestampToDate(props.redemption.expired_time) }
  )
  if (remainingQuota === 0) {
    impactMessage = t(
      'This review will be completed without deducting quota from the user wallet.'
    )
  } else if (expired) {
    impactMessage = t(
      '{{quota}} will be deducted from the user wallet immediately. The wallet may become negative.',
      { quota: formatQuota(remainingQuota) }
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => !submitting && props.onOpenChange(open)}
    >
      <DialogContent className='max-h-[calc(100vh-2rem)] overflow-y-auto sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Manually Resolve Limited Quota')}</DialogTitle>
          <DialogDescription>
            {step === 'form'
              ? t(
                  'Verify the historical records, then enter the remaining limited quota and an audit note.'
                )
              : t('Confirm the wallet impact before completing this review.')}
          </DialogDescription>
        </DialogHeader>

        {step === 'form' ? (
          <FieldGroup>
            <Alert>
              <AlertTitle>{t('Original Review Reason')}</AlertTitle>
              <AlertDescription>
                {props.redemption.reclaim_error || t('No reason recorded')}
              </AlertDescription>
            </Alert>

            <dl className='grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-2 rounded-lg border p-3 text-sm'>
              <dt className='text-muted-foreground'>{t('Issued Quota')}</dt>
              <dd className='text-right font-medium tabular-nums'>
                {formatQuota(props.redemption.quota)}
              </dd>
              <dt className='text-muted-foreground'>{t('Expires')}</dt>
              <dd className='text-right'>
                {formatTimestampToDate(props.redemption.expired_time)}
              </dd>
            </dl>

            <Field data-invalid={validationError?.field === 'amount'}>
              <FieldLabel htmlFor='manual-reclaim-remaining'>
                {t('Verified Remaining Quota ({{currency}})', {
                  currency: getCurrencyLabel(),
                })}
              </FieldLabel>
              <Input
                id='manual-reclaim-remaining'
                type='number'
                min={0}
                max={
                  missingRedeemedUser
                    ? 0
                    : quotaUnitsToEditableAmount(props.redemption.quota)
                }
                step={getEditableQuotaStep()}
                value={amount}
                onChange={(event) => {
                  setAmount(event.target.value)
                  setValidationError(null)
                }}
                aria-invalid={validationError?.field === 'amount'}
                disabled={submitting}
              />
              <FieldDescription>
                {missingRedeemedUser
                  ? t(
                      'No redeemed user is recorded, so only 0 can be confirmed.'
                    )
                  : t(
                      'Enter the amount that still belongs to this redemption code.'
                    )}
              </FieldDescription>
              <FieldError>
                {validationError?.field === 'amount'
                  ? validationError.message
                  : null}
              </FieldError>
            </Field>

            <Field data-invalid={validationError?.field === 'note'}>
              <FieldLabel htmlFor='manual-reclaim-note'>
                {t('Review Note')}
              </FieldLabel>
              <Textarea
                id='manual-reclaim-note'
                value={note}
                onChange={(event) => {
                  setNote(event.target.value)
                  setValidationError(null)
                }}
                rows={4}
                maxLength={2000}
                aria-invalid={validationError?.field === 'note'}
                disabled={submitting}
                placeholder={t(
                  'Describe the evidence checked and why this amount is correct.'
                )}
              />
              <FieldDescription>
                {t('{{count}} / 2000 bytes', { count: noteBytes })}
              </FieldDescription>
              <FieldError>
                {validationError?.field === 'note'
                  ? validationError.message
                  : null}
              </FieldError>
            </Field>
          </FieldGroup>
        ) : (
          <div className='flex flex-col gap-4'>
            <Alert
              variant={
                expired && remainingQuota > 0 ? 'destructive' : 'default'
              }
            >
              <AlertTitle>{t('Wallet Impact')}</AlertTitle>
              <AlertDescription>{impactMessage}</AlertDescription>
            </Alert>
            <dl className='grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-2 rounded-lg border p-3 text-sm'>
              <dt className='text-muted-foreground'>
                {t('Verified Remaining Quota')}
              </dt>
              <dd className='text-right font-medium tabular-nums'>
                {formatQuota(remainingQuota)}
              </dd>
              <dt className='text-muted-foreground'>{t('Review Note')}</dt>
              <dd className='max-w-72 text-right break-words whitespace-pre-wrap'>
                {note.trim()}
              </dd>
            </dl>
          </div>
        )}

        {requestError && (
          <Alert variant='destructive'>
            <AlertTitle>{t('Manual Review Failed')}</AlertTitle>
            <AlertDescription>{requestError}</AlertDescription>
          </Alert>
        )}

        <DialogFooter>
          {step === 'form' ? (
            <>
              <Button
                type='button'
                variant='outline'
                onClick={() => props.onOpenChange(false)}
                disabled={submitting}
              >
                {t('Cancel')}
              </Button>
              <Button type='button' onClick={handleReviewImpact}>
                {t('Review Wallet Impact')}
              </Button>
            </>
          ) : (
            <>
              <Button
                type='button'
                variant='outline'
                onClick={() => setStep('form')}
                disabled={submitting}
              >
                {t('Back')}
              </Button>
              <Button
                type='button'
                variant={
                  expired && remainingQuota > 0 ? 'destructive' : 'default'
                }
                onClick={() => void handleSubmit()}
                disabled={submitting}
              >
                {submitting && <Spinner data-icon='inline-start' />}
                {t('Confirm Manual Resolution')}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
