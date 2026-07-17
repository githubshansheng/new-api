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
  CheckCircle2,
  Clock3,
  ExternalLink,
  Loader2,
  QrCode,
  RefreshCw,
  TriangleAlert,
} from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { localizeLiandongMessage } from '@/lib/liandong-payment'
import {
  liandongFulfillmentStatusLabel,
  liandongPaymentStatusLabel,
} from '@/lib/liandong-status'

import {
  closeLiandongOrderForUser,
  createLiandongOrder,
  getLiandongOrder,
} from '../../api'
import type { LiandongPaymentView, LiandongProduct } from '../../types'

const terminalPaymentStatuses = new Set([
  'create_failed',
  'expired',
  'review_required',
  'closed',
])

const terminalFulfillmentStatuses = new Set(['fulfilled', 'review_required'])

const liandongIframeSandboxPermissions =
  'allow-forms allow-modals allow-popups allow-popups-to-escape-sandbox allow-same-origin allow-scripts'

function clientPollIntervalMs(order: LiandongPaymentView): number {
  const seconds = Math.min(
    60,
    Math.max(1, order.client_poll_interval_seconds || 5)
  )
  return seconds * 1000
}

function formatCountdown(seconds: number): string {
  const safeSeconds = Math.max(0, seconds)
  const minutes = Math.floor(safeSeconds / 60)
  const remainingSeconds = safeSeconds % 60
  return `${String(minutes).padStart(2, '0')}:${String(remainingSeconds).padStart(2, '0')}`
}

function paymentRequestErrorMessage(
  t: ReturnType<typeof useTranslation>['t'],
  error: unknown,
  fallbackKey: string
): string {
  const requestError = error as {
    response?: { data?: { message?: unknown } }
  }
  return localizeLiandongMessage(
    t,
    requestError.response?.data?.message,
    fallbackKey
  )
}

function statusVariant(status: string): StatusVariant {
  if (status === 'fulfilled' || status === 'paid') return 'success'
  if (
    status === 'failed' ||
    status === 'create_failed' ||
    status === 'closed'
  ) {
    return 'danger'
  }
  if (status === 'review_required' || status === 'expired') return 'warning'
  return 'info'
}

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
  product: LiandongProduct | null
  attemptId: number
  onPaymentSuccess?: () => void | Promise<void>
}

export function LiandongPaymentDialog({
  open,
  onOpenChange,
  product,
  attemptId,
  onPaymentSuccess,
}: Props) {
  const { t } = useTranslation()
  const [order, setOrder] = useState<LiandongPaymentView | null>(null)
  const [creating, setCreating] = useState(false)
  const [closing, setClosing] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [remainingSeconds, setRemainingSeconds] = useState<number | null>(null)
  const [error, setError] = useState('')
  const successHandledRef = useRef(false)
  const closeRequestedRef = useRef(false)
  const closingRef = useRef(false)
  const orderRef = useRef<LiandongPaymentView | null>(null)
  const createAttemptRef = useRef<number | null>(null)
  const createSessionRef = useRef(0)
  const productId = product?.id

  useEffect(() => {
    orderRef.current = order
  }, [order])

  const handleFulfilled = useCallback(
    async (nextOrder: LiandongPaymentView) => {
      if (
        nextOrder.fulfillment_status !== 'fulfilled' ||
        successHandledRef.current
      ) {
        return
      }
      successHandledRef.current = true
      toast.success(t('Payment completed and benefits activated'))
      await onPaymentSuccess?.()
    },
    [onPaymentSuccess, t]
  )

  const refreshOrder = useCallback(
    async (localTradeNo: string, manual = false) => {
      if (manual) setRefreshing(true)
      try {
        const response = await getLiandongOrder(localTradeNo)
        if (!response.success || !response.data) {
          setError(
            localizeLiandongMessage(
              t,
              response.message,
              'Failed to refresh payment status'
            )
          )
          return
        }
        setError('')
        setOrder(response.data)
        await handleFulfilled(response.data)
      } catch (requestError: unknown) {
        setError(
          paymentRequestErrorMessage(
            t,
            requestError,
            'Failed to refresh payment status'
          )
        )
      } finally {
        if (manual) setRefreshing(false)
      }
    },
    [handleFulfilled, t]
  )

  const closeDialog = useCallback(
    async (timedOut = false) => {
      if (closingRef.current) return
      const currentOrder = orderRef.current
      if (!currentOrder) {
        if (creating) {
          closeRequestedRef.current = true
          setClosing(true)
          return
        }
        onOpenChange(false)
        return
      }

      const shouldCloseOrder =
        currentOrder.payment_status === 'creating' ||
        currentOrder.payment_status === 'pending' ||
        currentOrder.payment_status === 'create_unknown'
      if (!shouldCloseOrder) {
        onOpenChange(false)
        return
      }

      closingRef.current = true
      setClosing(true)
      try {
        const response = await closeLiandongOrderForUser(
          currentOrder.local_trade_no
        )
        if (response.success && response.data) {
          orderRef.current = response.data
          setOrder(response.data)
          await handleFulfilled(response.data)
        } else if (!timedOut) {
          toast.error(
            localizeLiandongMessage(
              t,
              response.message,
              'Failed to close payment order'
            )
          )
        }
      } catch (requestError: unknown) {
        if (!timedOut) {
          toast.error(
            paymentRequestErrorMessage(
              t,
              requestError,
              'Failed to close payment order'
            )
          )
        }
      } finally {
        closingRef.current = false
        setClosing(false)
        closeRequestedRef.current = false
        onOpenChange(false)
      }
    },
    [creating, handleFulfilled, onOpenChange, t]
  )

  useEffect(() => {
    if (!open || productId === undefined) {
      createSessionRef.current += 1
      closeRequestedRef.current = false
      closingRef.current = false
      setClosing(false)
      return
    }
    if (createAttemptRef.current === attemptId) return
    createAttemptRef.current = attemptId
    const session = createSessionRef.current + 1
    createSessionRef.current = session

    successHandledRef.current = false
    setOrder(null)
    setError('')
    setCreating(true)

    const create = async () => {
      try {
        const response = await createLiandongOrder(productId)
        if (createSessionRef.current !== session) return
        if (!response.success || !response.data) {
          setError(
            localizeLiandongMessage(
              t,
              response.message,
              'Failed to create payment order'
            )
          )
          return
        }
        setError('')
        orderRef.current = response.data
        setOrder(response.data)
        await handleFulfilled(response.data)
        if (closeRequestedRef.current) {
          await closeDialog()
        }
      } catch (requestError: unknown) {
        if (createSessionRef.current !== session) return
        setError(
          paymentRequestErrorMessage(
            t,
            requestError,
            'Failed to create payment order'
          )
        )
        if (closeRequestedRef.current) {
          closeRequestedRef.current = false
          setClosing(false)
          onOpenChange(false)
        }
      } finally {
        if (createSessionRef.current === session) setCreating(false)
      }
    }

    void create()
  }, [
    attemptId,
    closeDialog,
    handleFulfilled,
    onOpenChange,
    open,
    productId,
    t,
  ])

  useEffect(() => {
    const expiresAt = order?.expires_at || 0
    if (
      !open ||
      !order ||
      expiresAt <= 0 ||
      !['creating', 'pending', 'create_unknown'].includes(order.payment_status)
    ) {
      setRemainingSeconds(null)
      return
    }

    const updateCountdown = () => {
      const remaining = Math.max(0, expiresAt - Math.floor(Date.now() / 1000))
      setRemainingSeconds(remaining)
      if (remaining === 0) void closeDialog(true)
    }
    updateCountdown()
    const timer = window.setInterval(updateCountdown, 1000)
    return () => window.clearInterval(timer)
  }, [closeDialog, open, order])

  useEffect(() => {
    if (
      !open ||
      !order ||
      terminalFulfillmentStatuses.has(order.fulfillment_status)
    ) {
      return
    }
    if (terminalPaymentStatuses.has(order.payment_status)) {
      return
    }

    let stopped = false
    let timer: number | undefined
    const poll = async () => {
      await refreshOrder(order.local_trade_no)
      if (!stopped) {
        timer = window.setTimeout(poll, clientPollIntervalMs(order))
      }
    }
    timer = window.setTimeout(poll, clientPollIntervalMs(order))
    return () => {
      stopped = true
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [open, order, refreshOrder])

  const openPaymentPage = () => {
    if (!order?.payment_url) return
    window.open(order.payment_url, '_blank', 'noopener,noreferrer')
  }

  const showIframe =
    order?.iframe_allowed === true &&
    !!order.payment_url &&
    order.payment_status === 'pending'
  const showPaymentButton =
    !!order?.payment_url && order.payment_status === 'pending' && !showIframe
  const isFulfilled = order?.fulfillment_status === 'fulfilled'
  const requiresAttention =
    !!order &&
    (terminalPaymentStatuses.has(order.payment_status) ||
      order.fulfillment_status === 'review_required')

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) {
          onOpenChange(true)
          return
        }
        void closeDialog()
      }}
      title={
        <span className='flex items-center gap-2'>
          <QrCode className='h-5 w-5' />
          {t('Liandong Payment')}
        </span>
      }
      description={product?.name}
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-xl'
      bodyClassName='space-y-4'
      contentHeight={showIframe ? 'min(66vh, 640px)' : 'auto'}
      footer={
        <div className='flex w-full flex-wrap justify-end gap-2'>
          {order && !isFulfilled && (
            <Button
              variant='outline'
              onClick={() => refreshOrder(order.local_trade_no, true)}
              disabled={refreshing}
            >
              {refreshing ? (
                <Loader2 className='h-4 w-4 animate-spin' />
              ) : (
                <RefreshCw className='h-4 w-4' />
              )}
              {t('Refresh status')}
            </Button>
          )}
          {order?.payment_url && (
            <Button variant='outline' onClick={openPaymentPage}>
              <ExternalLink className='h-4 w-4' />
              {t('Open payment page')}
            </Button>
          )}
          <Button onClick={() => void closeDialog()} disabled={closing}>
            {closing && <Loader2 className='h-4 w-4 animate-spin' />}
            {isFulfilled ? t('Done') : t('Close')}
          </Button>
        </div>
      }
    >
      {creating && (
        <div className='text-muted-foreground flex min-h-40 items-center justify-center gap-2 text-sm'>
          <Loader2 className='h-5 w-5 animate-spin' />
          {t('Creating payment order...')}
        </div>
      )}

      {error && (
        <Alert variant='destructive'>
          <TriangleAlert className='h-4 w-4' />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {order && (
        <>
          <div className='bg-muted/30 grid gap-2 rounded-md border p-3 text-sm sm:grid-cols-2'>
            <div className='min-w-0'>
              <p className='text-muted-foreground text-xs'>
                {t('Local order number')}
              </p>
              <p className='truncate font-mono text-xs'>
                {order.local_trade_no}
              </p>
            </div>
            <div className='flex flex-wrap items-end gap-2 sm:justify-end'>
              <StatusBadge
                label={liandongPaymentStatusLabel(t, order.payment_status)}
                variant={statusVariant(order.payment_status)}
                copyable={false}
              />
              <StatusBadge
                label={liandongFulfillmentStatusLabel(
                  t,
                  order.fulfillment_status
                )}
                variant={statusVariant(order.fulfillment_status)}
                copyable={false}
              />
            </div>
          </div>

          {remainingSeconds !== null && (
            <div className='flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm'>
              <span className='text-muted-foreground flex items-center gap-2'>
                <Clock3 className='h-4 w-4' />
                {t('Payment time remaining')}
              </span>
              <span className='font-mono font-semibold tabular-nums'>
                {formatCountdown(remainingSeconds)}
              </span>
            </div>
          )}

          {isFulfilled && (
            <Alert>
              <CheckCircle2 className='h-4 w-4 text-green-600' />
              <AlertDescription>
                {t('Payment completed and benefits activated')}
              </AlertDescription>
            </Alert>
          )}

          {requiresAttention && !isFulfilled && (
            <Alert variant='destructive'>
              <TriangleAlert className='h-4 w-4' />
              <AlertDescription>
                {order.late_payment
                  ? t(
                      'Payment arrived after the order closed and requires administrator review'
                    )
                  : t('This payment order requires administrator review')}
              </AlertDescription>
            </Alert>
          )}

          {showIframe && (
            <iframe
              src={order.payment_url}
              title={t('Liandong payment page')}
              className='h-full min-h-[420px] w-full rounded-md border bg-white'
              referrerPolicy='no-referrer'
              sandbox={liandongIframeSandboxPermissions}
            />
          )}

          {showPaymentButton && (
            <div className='flex min-h-40 flex-col items-center justify-center gap-3 rounded-md border p-4 text-center'>
              <QrCode className='text-muted-foreground h-10 w-10' />
              <p className='text-muted-foreground text-sm'>
                {t('Open the payment page to scan the QR code')}
              </p>
              <Button onClick={openPaymentPage}>
                <ExternalLink className='h-4 w-4' />
                {t('Open payment page')}
              </Button>
            </div>
          )}

          {!isFulfilled &&
            !requiresAttention &&
            !showIframe &&
            !showPaymentButton && (
              <div className='text-muted-foreground flex min-h-32 items-center justify-center gap-2 text-sm'>
                <Loader2 className='h-4 w-4 animate-spin' />
                {t('Waiting for payment order to become available...')}
              </div>
            )}
        </>
      )}
    </Dialog>
  )
}
