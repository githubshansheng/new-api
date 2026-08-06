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
  Copy,
  ExternalLink,
  Loader2,
  PackageOpen,
  QrCode,
  RefreshCw,
  TriangleAlert,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  formatDuration,
  formatResetPeriod,
} from '@/features/subscriptions/lib/format'
import type { SubscriptionPlan } from '@/features/subscriptions/types'
import {
  formatLiandongAmount,
  formatLiandongQuota,
  localizeLiandongMessage,
} from '@/lib/liandong-payment'
import {
  liandongFulfillmentStatusLabel,
  liandongPaymentStatusLabel,
} from '@/lib/liandong-status'

import {
  closeLiandongOrderForUser,
  createLiandongOrder,
  getLiandongOrder,
  getLiandongPaymentPage,
} from '../../api'
import type {
  LiandongPaymentPage,
  LiandongPaymentView,
  LiandongProduct,
} from '../../types'

const terminalPaymentStatuses = new Set([
  'create_failed',
  'expired',
  'review_required',
  'closed',
])

const terminalFulfillmentStatuses = new Set(['fulfilled', 'review_required'])

const liandongIframeSandboxPermissions =
  'allow-forms allow-modals allow-popups allow-popups-to-escape-sandbox allow-same-origin allow-scripts'

function liandongPaymentDocumentURL(html: string): string {
  return `data:text/html;charset=utf-8,${encodeURIComponent(html)}`
}

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
  const [thumbnailFailed, setThumbnailFailed] = useState(false)
  const [error, setError] = useState('')
  const [paymentPage, setPaymentPage] = useState<LiandongPaymentPage | null>(
    null
  )
  const [paymentPageLoading, setPaymentPageLoading] = useState(false)
  const [paymentPageError, setPaymentPageError] = useState('')
  const [paymentPageAttempt, setPaymentPageAttempt] = useState(0)
  const successHandledRef = useRef(false)
  const closeRequestedRef = useRef(false)
  const closingRef = useRef(false)
  const orderRef = useRef<LiandongPaymentView | null>(null)
  const createAttemptRef = useRef<number | null>(null)
  const createSessionRef = useRef(0)
  const paymentPageSessionRef = useRef(0)
  const productId = product?.id
  const subscriptionPlan: Partial<SubscriptionPlan> | null =
    product?.subscription
      ? {
          title: product.subscription.title,
          subtitle: product.subscription.subtitle,
          duration_unit: product.subscription
            .duration_unit as SubscriptionPlan['duration_unit'],
          duration_value: product.subscription.duration_value,
          custom_seconds: product.subscription.custom_seconds,
          total_amount: product.subscription.total_amount,
          quota_reset_period: product.subscription
            .quota_reset_period as SubscriptionPlan['quota_reset_period'],
          quota_reset_custom_seconds:
            product.subscription.quota_reset_custom_seconds,
          upgrade_group: product.subscription.upgrade_group,
        }
      : null
  const paymentFrameURL = useMemo(() => {
    if (paymentPage?.redirect_url) return paymentPage.redirect_url
    if (paymentPage?.html) return liandongPaymentDocumentURL(paymentPage.html)
    return undefined
  }, [paymentPage?.html, paymentPage?.redirect_url])

  useEffect(() => {
    orderRef.current = order
  }, [order])

  useEffect(() => {
    setThumbnailFailed(false)
  }, [product?.thumbnail_url])

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
      if (manual) {
        setRefreshing(true)
        setPaymentPageAttempt((current) => current + 1)
      }
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
    setPaymentPage(null)
    setPaymentPageError('')
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

  const loadPaymentPage = useCallback(
    async (paymentURL: string): Promise<LiandongPaymentPage | null> => {
      const requestID = paymentPageSessionRef.current + 1
      paymentPageSessionRef.current = requestID
      setPaymentPageLoading(true)
      try {
        const response = await getLiandongPaymentPage(paymentURL)
        if (paymentPageSessionRef.current !== requestID) return null
        if (!response.success || !response.data) {
          setPaymentPageError(
            localizeLiandongMessage(
              t,
              response.message,
              'Failed to open payment page'
            )
          )
          return null
        }
        if (!response.data.html && !response.data.redirect_url) {
          setPaymentPageError(t('Failed to open payment page'))
          return null
        }
        setPaymentPageError('')
        setPaymentPage(response.data)
        return response.data
      } catch (requestError: unknown) {
        if (paymentPageSessionRef.current !== requestID) return null
        setPaymentPageError(
          paymentRequestErrorMessage(
            t,
            requestError,
            'Failed to open payment page'
          )
        )
        return null
      } finally {
        if (paymentPageSessionRef.current === requestID) {
          setPaymentPageLoading(false)
        }
      }
    },
    [t]
  )

  useEffect(() => {
    const paymentURL = order?.payment_url
    if (!open || !paymentURL || order.payment_status !== 'pending') {
      setPaymentPage(null)
      setPaymentPageError('')
      setPaymentPageLoading(false)
      return
    }

    void loadPaymentPage(paymentURL)
    return () => {
      paymentPageSessionRef.current += 1
    }
  }, [
    loadPaymentPage,
    open,
    order?.local_trade_no,
    order?.payment_status,
    order?.payment_url,
    paymentPageAttempt,
  ])

  const copyFallbackContact = async () => {
    if (!order?.fallback_contact) return
    try {
      if (!navigator.clipboard) throw new Error('Clipboard API unavailable')
      await navigator.clipboard.writeText(order.fallback_contact)
      toast.success(t('Payment contact copied'))
    } catch {
      toast.error(t('Copy failed. Please copy the payment contact manually.'))
    }
  }

  const openPaymentPage = async () => {
    if (!order) return
    const popup = window.open('', '_blank')
    if (!popup) {
      toast.error(t('Operation failed'))
      return
    }
    popup.opener = null

    if (order.fallback_url) {
      await copyFallbackContact()
      popup.location.replace(order.fallback_url)
      return
    }

    if (!order.payment_url) {
      popup.close()
      return
    }

    const page = paymentPage || (await loadPaymentPage(order.payment_url))
    if (!page) {
      popup.close()
      return
    }
    if (page.redirect_url) {
      popup.location.replace(page.redirect_url)
      return
    }
    if (page.html) {
      popup.document.open()
      popup.document.write(
        '<!doctype html><html><head><meta charset="utf-8"><meta name="referrer" content="no-referrer"><style>html,body,iframe{width:100%;height:100%;margin:0;border:0}</style></head><body><iframe title="Payment" sandbox="allow-forms allow-modals allow-popups allow-popups-to-escape-sandbox allow-same-origin allow-scripts" referrerpolicy="no-referrer"></iframe></body></html>'
      )
      popup.document.close()
      const paymentFrame = popup.document.querySelector('iframe')
      if (paymentFrame) paymentFrame.src = liandongPaymentDocumentURL(page.html)
    }
  }

  const showIframe =
    order?.iframe_allowed === true &&
    !!order.payment_url &&
    order.payment_status === 'pending'
  const showPaymentButton =
    !!order?.payment_url && order.payment_status === 'pending' && !showIframe
  const showFallbackPayment =
    !!order?.fallback_url && order.payment_status === 'pending'
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
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-2xl'
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
          {(order?.payment_url || order?.fallback_url) && (
            <Button variant='outline' onClick={() => void openPaymentPage()}>
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
      {product && (
        <div className='flex min-w-0 items-start gap-3 rounded-md border p-3'>
          <div className='bg-muted relative h-20 w-20 shrink-0 overflow-hidden rounded-md border sm:h-24 sm:w-24'>
            {product.thumbnail_url && !thumbnailFailed ? (
              <img
                src={product.thumbnail_url}
                alt={product.name}
                className='h-full w-full object-cover'
                onError={() => setThumbnailFailed(true)}
              />
            ) : (
              <div className='text-muted-foreground flex h-full w-full items-center justify-center'>
                <PackageOpen className='h-8 w-8' />
              </div>
            )}
          </div>
          <div className='min-w-0 flex-1'>
            <div className='flex flex-wrap items-start justify-between gap-x-3 gap-y-1'>
              <div className='min-w-0'>
                <p className='font-medium break-words'>{product.name}</p>
                {product.subscription?.subtitle && (
                  <p className='text-muted-foreground mt-0.5 text-xs break-words'>
                    {product.subscription.subtitle}
                  </p>
                )}
              </div>
              <p className='text-primary shrink-0 font-semibold'>
                {formatLiandongAmount(
                  product.currency,
                  product.expected_amount_minor
                )}
              </p>
            </div>

            <dl className='mt-2 grid min-w-0 gap-x-4 gap-y-1 text-xs sm:grid-cols-2'>
              {product.business_type === 'quota' ? (
                <div className='min-w-0'>
                  <dt className='text-muted-foreground'>{t('Total Quota')}</dt>
                  <dd className='break-words'>
                    {formatLiandongQuota(product.quota_amount)}
                  </dd>
                </div>
              ) : (
                subscriptionPlan && (
                  <>
                    <div className='min-w-0'>
                      <dt className='text-muted-foreground'>
                        {t('Validity Period')}
                      </dt>
                      <dd className='break-words'>
                        {formatDuration(subscriptionPlan, t)}
                      </dd>
                    </div>
                    <div className='min-w-0'>
                      <dt className='text-muted-foreground'>
                        {t('Total Quota')}
                      </dt>
                      <dd className='break-words'>
                        {product.subscription?.total_amount
                          ? formatLiandongQuota(
                              product.subscription.total_amount
                            )
                          : t('Unlimited')}
                      </dd>
                    </div>
                    <div className='min-w-0'>
                      <dt className='text-muted-foreground'>
                        {t('Quota Reset')}
                      </dt>
                      <dd className='break-words'>
                        {formatResetPeriod(subscriptionPlan, t)}
                      </dd>
                    </div>
                    <div className='min-w-0'>
                      <dt className='text-muted-foreground'>
                        {t('Upgrade Group')}
                      </dt>
                      <dd className='break-words'>
                        {product.subscription?.upgrade_group || t('No change')}
                      </dd>
                    </div>
                  </>
                )
              )}
            </dl>
          </div>
        </div>
      )}

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

          {showFallbackPayment && (
            <div className='space-y-3 rounded-md border p-4'>
              <div>
                <p className='font-medium'>{t('Payment contact')}</p>
                <p className='text-muted-foreground mt-1 text-sm'>
                  {t(
                    'Enter this contact on the payment page so the order can be verified automatically.'
                  )}
                </p>
              </div>
              <div className='flex flex-wrap items-center gap-2'>
                <code className='bg-muted min-w-0 flex-1 rounded px-3 py-2 font-mono text-sm break-all'>
                  {order.fallback_contact}
                </code>
                <Button
                  variant='outline'
                  size='icon'
                  title={t('Copy')}
                  aria-label={t('Copy')}
                  onClick={() => void copyFallbackContact()}
                >
                  <Copy className='h-4 w-4' />
                </Button>
              </div>
              <Button className='w-full' onClick={() => void openPaymentPage()}>
                <ExternalLink className='h-4 w-4' />
                {t('Copy contact and open payment page')}
              </Button>
            </div>
          )}

          {showIframe && paymentPageLoading && (
            <div className='text-muted-foreground flex min-h-[420px] items-center justify-center gap-2 rounded-md border text-sm'>
              <Loader2 className='h-5 w-5 animate-spin' />
              {t('Loading...')}
            </div>
          )}

          {showIframe && !paymentPageLoading && paymentPageError && (
            <Alert variant='destructive'>
              <TriangleAlert className='h-4 w-4' />
              <AlertDescription>{paymentPageError}</AlertDescription>
            </Alert>
          )}

          {showIframe && !paymentPageLoading && paymentPage && (
            <iframe
              src={paymentFrameURL}
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
              <Button onClick={() => void openPaymentPage()}>
                <ExternalLink className='h-4 w-4' />
                {t('Open payment page')}
              </Button>
            </div>
          )}

          {!isFulfilled &&
            !requiresAttention &&
            !showFallbackPayment &&
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
