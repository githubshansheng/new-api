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
  Boxes,
  Check,
  ChevronLeft,
  ChevronRight,
  CircleX,
  ImageIcon,
  KeyRound,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
  RotateCcw,
  Save,
  Search,
  ShieldAlert,
  ShieldCheck,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { getAdminPlans } from '@/features/subscriptions/api'
import {
  formatDuration,
  formatResetPeriod,
} from '@/features/subscriptions/lib/format'
import type { PlanRecord } from '@/features/subscriptions/types'
import {
  formatLiandongAmount,
  formatLiandongQuota,
  localizeLiandongMessage,
  parsePaymentAmountMinor,
  paymentAmountInputFromMinor,
} from '@/lib/liandong-payment'
import {
  liandongFulfillmentStatusLabel,
  liandongPaymentStatusLabel,
} from '@/lib/liandong-status'

import {
  SettingsFormGrid,
  SettingsSwitchField,
} from '../components/settings-form-layout'
import { SettingsSection } from '../components/settings-section'
import {
  closeLiandongOrder,
  createLiandongProduct,
  deleteLiandongThumbnail,
  disableLiandongInventory,
  getLiandongSettings,
  listLiandongProviderGoods,
  listLiandongOrders,
  listLiandongProducts,
  manualFulfillLiandongOrder,
  requeueLiandongOrder,
  retryLiandongFulfillment,
  addLiandongInventory,
  updateLiandongProduct,
  updateLiandongSettings,
  uploadLiandongThumbnail,
} from './liandong-api'
import { LiandongThumbnailEditor } from './liandong-thumbnail-editor'
import type {
  LiandongProductPayload,
  LiandongProviderGoods,
  LiandongRootOrder,
  LiandongRootProduct,
  LiandongSettings,
} from './liandong-types'

const PAGE_SIZE = 10

const defaultSettings: LiandongSettings = {
  enabled: false,
  create_enabled: false,
  reconcile_enabled: false,
  fulfill_enabled: false,
  iframe_enabled: false,
  base_url: 'https://pay.ldxp.cn',
  proxy_enabled: false,
  proxy_url: '',
  poll_interval_seconds: 30,
  client_poll_interval_seconds: 5,
  reconcile_batch_size: 50,
  payment_timeout_minutes: 30,
  juuid: '',
  auth_mode: 'manual_token',
  username_configured: false,
  password_configured: false,
  merchant_token_configured: false,
}

const emptyProduct: LiandongProductPayload = {
  business_type: 'quota',
  goods_type: 'card',
  name: '',
  goods_key: '',
  quota_amount: 0,
  plan_id: 0,
  expected_amount_minor: 0,
  currency: 'CNY',
  inventory_mode: 'unlimited',
  inventory_capacity: 0,
  enabled: true,
  sort_order: 0,
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

function formatTimestamp(timestamp: number): string {
  return timestamp > 0 ? new Date(timestamp * 1000).toLocaleString() : '-'
}

function goodsTypeLabel(goodsType: string, t: (key: string) => string): string {
  switch (goodsType) {
    case 'article':
      return t('Knowledge')
    case 'resource':
      return t('Resource')
    case 'equity':
      return t('Entitlement')
    default:
      return t('Card key')
  }
}

function inventoryLevelLabel(
  level: LiandongRootProduct['inventory_level'],
  t: (key: string) => string
): string {
  switch (level) {
    case 'unlimited':
      return t('Unlimited stock')
    case 'sufficient':
      return t('In stock')
    case 'normal':
      return t('Limited stock')
    case 'low':
      return t('Low stock')
    case 'out_of_stock':
      return t('Out of stock')
  }
}

function inventoryLevelVariant(
  level: LiandongRootProduct['inventory_level']
): StatusVariant {
  if (level === 'out_of_stock') return 'danger'
  if (level === 'low') return 'warning'
  return 'success'
}

export function LiandongPaymentSection() {
  const { t } = useTranslation()
  const [settings, setSettings] = useState(defaultSettings)
  const [products, setProducts] = useState<LiandongRootProduct[]>([])
  const [plans, setPlans] = useState<PlanRecord[]>([])
  const [orders, setOrders] = useState<LiandongRootOrder[]>([])
  const [orderTotal, setOrderTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [keyword, setKeyword] = useState('')
  const [searchKeyword, setSearchKeyword] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [merchantToken, setMerchantToken] = useState('')
  const [clearUsername, setClearUsername] = useState(false)
  const [clearPassword, setClearPassword] = useState(false)
  const [clearToken, setClearToken] = useState(false)
  const [loading, setLoading] = useState(true)
  const [savingSettings, setSavingSettings] = useState(false)
  const [productDialogOpen, setProductDialogOpen] = useState(false)
  const [editingProduct, setEditingProduct] =
    useState<LiandongRootProduct | null>(null)
  const [productForm, setProductForm] =
    useState<LiandongProductPayload>(emptyProduct)
  const [paymentAmountInput, setPaymentAmountInput] = useState('')
  const [savingProduct, setSavingProduct] = useState(false)
  const [providerGoods, setProviderGoods] = useState<LiandongProviderGoods[]>(
    []
  )
  const [providerGoodsKeyword, setProviderGoodsKeyword] = useState('')
  const [loadingProviderGoods, setLoadingProviderGoods] = useState(false)
  const [thumbnailBlob, setThumbnailBlob] = useState<Blob | null>(null)
  const [deletingThumbnail, setDeletingThumbnail] = useState(false)
  const [inventoryAddCount, setInventoryAddCount] = useState(0)
  const [inventoryName, setInventoryName] = useState('')
  const [inventoryDisableCount, setInventoryDisableCount] = useState(0)
  const [inventoryAction, setInventoryAction] = useState('')
  const [orderAction, setOrderAction] = useState('')
  const [closeOrder, setCloseOrder] = useState<LiandongRootOrder | null>(null)

  const loadOrders = useCallback(async () => {
    const response = await listLiandongOrders(page, PAGE_SIZE, searchKeyword)
    if (response.success && response.data) {
      setOrders(response.data.items || [])
      setOrderTotal(response.data.total || 0)
    }
  }, [page, searchKeyword])

  const loadProducts = useCallback(async () => {
    const response = await listLiandongProducts()
    if (!response.success) return null
    const loadedProducts = response.data || []
    setProducts(loadedProducts)
    return loadedProducts
  }, [])

  const refreshEditingProduct = useCallback(
    async (productID: number) => {
      const refreshedProducts = await loadProducts()
      const currentProduct = refreshedProducts?.find(
        (product) => product.id === productID
      )
      if (currentProduct) setEditingProduct(currentProduct)
    },
    [loadProducts]
  )

  const refreshEditingProductBestEffort = useCallback(
    async (productID: number) => {
      try {
        await refreshEditingProduct(productID)
      } catch {
        // Preserve the original product operation error shown to the user.
      }
    },
    [refreshEditingProduct]
  )

  const loadSettings = useCallback(async () => {
    const response = await getLiandongSettings()
    if (response.success && response.data) setSettings(response.data)
  }, [])

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const [
          settingsResponse,
          productsResponse,
          ordersResponse,
          plansResponse,
        ] = await Promise.all([
          getLiandongSettings(),
          listLiandongProducts(),
          listLiandongOrders(1, PAGE_SIZE, ''),
          getAdminPlans(),
        ])
        if (cancelled) return
        if (settingsResponse.success && settingsResponse.data) {
          setSettings(settingsResponse.data)
        }
        if (productsResponse.success) {
          setProducts(productsResponse.data || [])
        }
        if (ordersResponse.success && ordersResponse.data) {
          setOrders(ordersResponse.data.items || [])
          setOrderTotal(ordersResponse.data.total || 0)
        }
        if (plansResponse.success) setPlans(plansResponse.data || [])
      } catch {
        if (!cancelled) toast.error(t('Failed to load Liandong settings'))
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [t])

  useEffect(() => {
    if (loading) return
    void loadOrders()
  }, [loadOrders, loading])

  const totalPages = Math.max(1, Math.ceil(orderTotal / PAGE_SIZE))
  const planOptions = useMemo(
    () =>
      plans
        .map((record) => record.plan)
        .filter(Boolean)
        .sort((a, b) => a.sort_order - b.sort_order),
    [plans]
  )
  const selectedPlan = useMemo(
    () => planOptions.find((plan) => plan.id === productForm.plan_id),
    [planOptions, productForm.plan_id]
  )

  const saveSettings = async () => {
    setSavingSettings(true)
    try {
      const response = await updateLiandongSettings({
        enabled: settings.enabled,
        create_enabled: settings.create_enabled,
        reconcile_enabled: settings.reconcile_enabled,
        fulfill_enabled: settings.fulfill_enabled,
        iframe_enabled: settings.iframe_enabled,
        base_url: settings.base_url,
        proxy_enabled: settings.proxy_enabled,
        proxy_url: settings.proxy_url,
        poll_interval_seconds: settings.poll_interval_seconds,
        client_poll_interval_seconds: settings.client_poll_interval_seconds,
        reconcile_batch_size: settings.reconcile_batch_size,
        payment_timeout_minutes: settings.payment_timeout_minutes,
        juuid: settings.juuid,
        auth_mode: settings.auth_mode,
        username:
          settings.auth_mode === 'credentials'
            ? username.trim() || undefined
            : undefined,
        password:
          settings.auth_mode === 'credentials'
            ? password || undefined
            : undefined,
        merchant_token:
          settings.auth_mode === 'manual_token'
            ? merchantToken.trim() || undefined
            : undefined,
        clear_username: clearUsername,
        clear_password: clearPassword,
        clear_token: clearToken,
      })
      if (!response.success) {
        toast.error(
          localizeLiandongMessage(t, response.message, 'Update failed')
        )
        return
      }
      toast.success(t('Updated successfully'))
      setUsername('')
      setPassword('')
      setMerchantToken('')
      setClearUsername(false)
      setClearPassword(false)
      setClearToken(false)
      await loadSettings()
    } catch {
      toast.error(t('Update failed'))
    } finally {
      setSavingSettings(false)
    }
  }

  const emergencyDisable = async () => {
    setSavingSettings(true)
    try {
      const response = await updateLiandongSettings({ enabled: false })
      if (!response.success) {
        toast.error(
          localizeLiandongMessage(t, response.message, 'Update failed')
        )
        return
      }
      setSettings((current) => ({ ...current, enabled: false }))
      toast.success(t('Liandong gateway disabled'))
    } catch {
      toast.error(t('Update failed'))
    } finally {
      setSavingSettings(false)
    }
  }

  const openCreateProduct = () => {
    setEditingProduct(null)
    setProductForm(emptyProduct)
    setPaymentAmountInput('')
    setProviderGoods([])
    setProviderGoodsKeyword('')
    setThumbnailBlob(null)
    setInventoryAddCount(0)
    setInventoryName('')
    setInventoryDisableCount(0)
    setProductDialogOpen(true)
  }

  const openEditProduct = (product: LiandongRootProduct) => {
    setEditingProduct(product)
    setProductForm({
      business_type: product.business_type,
      goods_type: product.goods_type,
      name: product.name,
      goods_key: product.goods_key,
      quota_amount: product.quota_amount,
      plan_id: product.plan_id,
      expected_amount_minor: product.expected_amount_minor,
      currency: product.currency,
      inventory_mode: product.inventory_mode,
      inventory_capacity: product.inventory_capacity,
      enabled: product.enabled,
      sort_order: product.sort_order,
    })
    setPaymentAmountInput(
      paymentAmountInputFromMinor(product.expected_amount_minor)
    )
    setProviderGoods([])
    setProviderGoodsKeyword('')
    setThumbnailBlob(null)
    setInventoryAddCount(0)
    setInventoryName(product.name)
    setInventoryDisableCount(0)
    setProductDialogOpen(true)
  }

  const searchProviderGoods = async () => {
    setLoadingProviderGoods(true)
    try {
      const response = await listLiandongProviderGoods(
        productForm.goods_type,
        providerGoodsKeyword.trim()
      )
      if (!response.success) {
        toast.error(
          localizeLiandongMessage(t, response.message, 'Query failed')
        )
        return
      }
      setProviderGoods(response.data || [])
    } catch {
      toast.error(t('Query failed'))
    } finally {
      setLoadingProviderGoods(false)
    }
  }

  const deleteThumbnail = async () => {
    if (!editingProduct) return
    setDeletingThumbnail(true)
    try {
      const response = await deleteLiandongThumbnail(editingProduct.id)
      if (!response.success) {
        toast.error(
          localizeLiandongMessage(t, response.message, 'Operation failed')
        )
        return
      }
      setEditingProduct((current) =>
        current
          ? {
              ...current,
              thumbnail_url: undefined,
              thumbnail_version: undefined,
            }
          : current
      )
      setThumbnailBlob(null)
      toast.success(t('Thumbnail deleted'))
      await loadProducts()
    } catch {
      toast.error(t('Operation failed'))
    } finally {
      setDeletingThumbnail(false)
    }
  }

  const runInventoryAction = async (action: 'add' | 'disable') => {
    if (!editingProduct) return
    const count = action === 'add' ? inventoryAddCount : inventoryDisableCount
    if (!Number.isInteger(count) || count <= 0 || count > 1000) {
      toast.error(
        t('Count must be between {{min}} and {{max}}', {
          min: 1,
          max: 1000,
        })
      )
      return
    }
    setInventoryAction(action)
    try {
      const response =
        action === 'add'
          ? await addLiandongInventory(
              editingProduct.id,
              count,
              inventoryName.trim()
            )
          : await disableLiandongInventory(editingProduct.id, count)
      if (!response.success) {
        toast.error(
          localizeLiandongMessage(t, response.message, 'Operation failed')
        )
        return
      }
      toast.success(t('Inventory updated'))
      if (action === 'add') setInventoryAddCount(0)
      else setInventoryDisableCount(0)
      await refreshEditingProduct(editingProduct.id)
    } catch {
      toast.error(t('Operation failed'))
    } finally {
      setInventoryAction('')
    }
  }

  const saveProduct = async () => {
    if (!productForm.name.trim() || !productForm.goods_key.trim()) {
      toast.error(t('Product name and goods key are required'))
      return
    }
    if (
      productForm.business_type === 'quota' &&
      (!Number.isInteger(productForm.quota_amount) ||
        productForm.quota_amount <= 0)
    ) {
      toast.error(t('Quota amount must be a positive whole number'))
      return
    }
    if (
      productForm.business_type === 'subscription' &&
      productForm.plan_id <= 0
    ) {
      toast.error(t('Please select a subscription plan'))
      return
    }
    if (
      inventoryAddCount !== 0 &&
      (!Number.isInteger(inventoryAddCount) ||
        inventoryAddCount < 1 ||
        inventoryAddCount > 1000)
    ) {
      toast.error(
        t('Count must be between {{min}} and {{max}}', {
          min: 1,
          max: 1000,
        })
      )
      return
    }
    const expectedAmountMinor = parsePaymentAmountMinor(paymentAmountInput)
    if (expectedAmountMinor === null) {
      toast.error(
        t('Payment amount must be positive with no more than two decimals')
      )
      return
    }

    setSavingProduct(true)
    let savedProduct: LiandongRootProduct | null = null
    try {
      const payload = {
        ...productForm,
        expected_amount_minor: expectedAmountMinor,
        name: productForm.name.trim(),
        goods_key: productForm.goods_key.trim(),
        currency: productForm.currency.trim().toUpperCase(),
        quota_amount:
          productForm.business_type === 'quota' ? productForm.quota_amount : 0,
        plan_id:
          productForm.business_type === 'subscription'
            ? productForm.plan_id
            : 0,
      }
      const response = editingProduct
        ? await updateLiandongProduct(editingProduct.id, payload)
        : await createLiandongProduct(payload)
      if (!response.success) {
        toast.error(
          localizeLiandongMessage(t, response.message, 'Update failed')
        )
        return
      }
      savedProduct = response.data || null
      if (!savedProduct) {
        toast.error(t('Product response is missing'))
        return
      }
      setEditingProduct(savedProduct)
      if (thumbnailBlob) {
        const thumbnailResponse = await uploadLiandongThumbnail(
          savedProduct.id,
          thumbnailBlob
        )
        if (!thumbnailResponse.success) {
          toast.error(
            localizeLiandongMessage(
              t,
              thumbnailResponse.message,
              'Thumbnail upload failed'
            )
          )
          await refreshEditingProductBestEffort(savedProduct.id)
          return
        }
        setThumbnailBlob(null)
      }
      if (
        productForm.inventory_mode === 'redemption_code' &&
        inventoryAddCount > 0
      ) {
        const inventoryResponse = await addLiandongInventory(
          savedProduct.id,
          inventoryAddCount,
          inventoryName.trim() || payload.name
        )
        if (!inventoryResponse.success) {
          toast.error(
            localizeLiandongMessage(
              t,
              inventoryResponse.message,
              'Inventory update failed'
            )
          )
          await refreshEditingProductBestEffort(savedProduct.id)
          return
        }
        setInventoryAddCount(0)
      }
      toast.success(t('Updated successfully'))
      setProductDialogOpen(false)
      await loadProducts()
    } catch {
      toast.error(t('Update failed'))
      if (savedProduct) {
        await refreshEditingProductBestEffort(savedProduct.id)
      }
    } finally {
      setSavingProduct(false)
    }
  }

  const toggleProduct = async (product: LiandongRootProduct) => {
    setOrderAction(`product-${product.id}`)
    try {
      const response = await updateLiandongProduct(product.id, {
        business_type: product.business_type,
        goods_type: product.goods_type,
        name: product.name,
        goods_key: product.goods_key,
        quota_amount: product.quota_amount,
        plan_id: product.plan_id,
        expected_amount_minor: product.expected_amount_minor,
        currency: product.currency,
        inventory_mode: product.inventory_mode,
        inventory_capacity: product.inventory_capacity,
        enabled: !product.enabled,
        sort_order: product.sort_order,
      })
      if (!response.success) {
        toast.error(
          localizeLiandongMessage(t, response.message, 'Update failed')
        )
        return
      }
      await loadProducts()
    } finally {
      setOrderAction('')
    }
  }

  const runOrderAction = async (
    order: LiandongRootOrder,
    action: 'requeue' | 'retry' | 'manual' | 'close'
  ) => {
    setOrderAction(`${action}-${order.local_trade_no}`)
    try {
      let response
      switch (action) {
        case 'requeue':
          response = await requeueLiandongOrder(order.local_trade_no)
          break
        case 'retry':
          response = await retryLiandongFulfillment(order.local_trade_no)
          break
        case 'manual':
          response = await manualFulfillLiandongOrder(order.local_trade_no)
          break
        case 'close':
          response = await closeLiandongOrder(order.local_trade_no)
          break
      }
      if (!response.success) {
        toast.error(
          localizeLiandongMessage(t, response.message, 'Operation failed')
        )
        return
      }
      toast.success(t('Operation completed'))
      await loadOrders()
    } catch {
      toast.error(t('Operation failed'))
    } finally {
      setOrderAction('')
      setCloseOrder(null)
    }
  }

  if (loading) {
    return (
      <div className='text-muted-foreground flex min-h-48 items-center justify-center gap-2 text-sm'>
        <Loader2 className='h-5 w-5 animate-spin' />
        {t('Loading...')}
      </div>
    )
  }

  return (
    <SettingsSection title={t('Liandong Payment Gateway')}>
      <Alert>
        <ShieldAlert className='h-4 w-4' />
        <AlertDescription>
          {t(
            'Only Root can manage this gateway. Merchant token is stored and used only by the backend.'
          )}
        </AlertDescription>
      </Alert>

      <div className='space-y-4 border-b pb-6'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div>
            <h4 className='font-medium'>{t('Gateway controls')}</h4>
            <p className='text-muted-foreground text-xs'>
              {t('Use the master switch for immediate emergency shutdown.')}
            </p>
          </div>
          <Button
            variant='destructive'
            onClick={emergencyDisable}
            disabled={!settings.enabled || savingSettings}
          >
            <CircleX className='h-4 w-4' />
            {t('Emergency disable')}
          </Button>
        </div>

        <SettingsFormGrid className='gap-y-2'>
          <SettingsSwitchField
            checked={settings.enabled}
            onCheckedChange={(enabled) =>
              setSettings((current) =>
                enabled
                  ? {
                      ...current,
                      enabled: true,
                      create_enabled: true,
                      reconcile_enabled: true,
                      fulfill_enabled: true,
                      iframe_enabled: true,
                    }
                  : { ...current, enabled: false }
              )
            }
            label={t('Enable Liandong gateway')}
            description={t(
              'Controls all Liandong payment activity. Disabling it immediately stops new orders, verification, and activation.'
            )}
          />
          <SettingsSwitchField
            checked={settings.create_enabled}
            onCheckedChange={(create_enabled) =>
              setSettings((current) => ({ ...current, create_enabled }))
            }
            label={t('Allow order creation')}
            description={t(
              'Allows users to create payment orders from enabled Liandong products.'
            )}
          />
          <SettingsSwitchField
            checked={settings.reconcile_enabled}
            onCheckedChange={(reconcile_enabled) =>
              setSettings((current) => ({ ...current, reconcile_enabled }))
            }
            label={t('Enable scheduled payment verification')}
            description={t(
              'Runs one global backend task that checks pending orders in batches at the configured interval.'
            )}
          />
          <SettingsSwitchField
            checked={settings.fulfill_enabled}
            onCheckedChange={(fulfill_enabled) =>
              setSettings((current) => ({ ...current, fulfill_enabled }))
            }
            label={t('Enable automatic activation')}
            description={t(
              'Automatically credits quota or activates the subscription after payment is confirmed.'
            )}
          />
          <SettingsSwitchField
            checked={settings.iframe_enabled}
            onCheckedChange={(iframe_enabled) =>
              setSettings((current) => ({ ...current, iframe_enabled }))
            }
            label={t('Embed payment page in iframe')}
            description={t(
              'Shows the Liandong payment page inside the payment dialog instead of opening a new page.'
            )}
          />
          <SettingsSwitchField
            checked={settings.proxy_enabled}
            onCheckedChange={(proxy_enabled) =>
              setSettings((current) => ({ ...current, proxy_enabled }))
            }
            label={t('Enable dedicated card marketplace proxy')}
            description={t(
              'Routes only card marketplace backend API requests through the configured HTTP, HTTPS, or SOCKS5 proxy. User payment pages remain direct browser requests.'
            )}
          />
        </SettingsFormGrid>

        <div className='grid gap-4 sm:grid-cols-2'>
          <div className='grid gap-1.5 sm:col-span-2'>
            <Label htmlFor='liandong-base-url'>
              {t('Provider Base URL')}
            </Label>
            <Input
              id='liandong-base-url'
              type='url'
              value={settings.base_url}
              maxLength={2048}
              placeholder='https://pay.ldxp.cn'
              onChange={(event) =>
                setSettings((current) => ({
                  ...current,
                  base_url: event.target.value,
                }))
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Base URL used for card marketplace API requests and payment pages. HTTPS is required; an optional path prefix is supported.'
              )}
            </p>
          </div>

          {settings.proxy_enabled && (
            <div className='grid gap-1.5 sm:col-span-2'>
              <Label htmlFor='liandong-proxy-url'>
                {t('Card marketplace proxy URL')}
              </Label>
              <Input
                id='liandong-proxy-url'
                value={settings.proxy_url}
                maxLength={2048}
                placeholder='http://username:password@127.0.0.1:7890'
                onChange={(event) =>
                  setSettings((current) => ({
                    ...current,
                    proxy_url: event.target.value,
                  }))
                }
              />
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Supports HTTP, HTTPS, and SOCKS5 proxy URLs, with optional username and password in this field. Examples: http://username:password@host:port and socks5://host:port:username:password.'
                )}
              </p>
            </div>
          )}

          <div className='grid gap-1.5'>
            <Label htmlFor='liandong-juuid'>{t('JUUID')}</Label>
            <Input
              id='liandong-juuid'
              value={settings.juuid}
              maxLength={128}
              onChange={(event) =>
                setSettings((current) => ({
                  ...current,
                  juuid: event.target.value,
                }))
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Open the Liandong platform, press F12, and obtain JUUID from the browser network requests.'
              )}
            </p>
          </div>
          <div className='grid gap-1.5'>
            <Label>{t('Authentication mode')}</Label>
            <Select
              items={[
                { value: 'manual_token', label: t('Manual merchant token') },
                { value: 'credentials', label: t('Account and password') },
              ]}
              value={settings.auth_mode}
              onValueChange={(authMode) => {
                if (authMode !== 'manual_token' && authMode !== 'credentials') {
                  return
                }
                setSettings((current) => ({
                  ...current,
                  auth_mode: authMode,
                }))
              }}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  <SelectItem value='manual_token'>
                    {t('Manual merchant token')}
                  </SelectItem>
                  <SelectItem value='credentials'>
                    {t('Account and password')}
                  </SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Choose exactly one authentication method. Credential mode automatically signs in again after a 401 response.'
              )}
            </p>
          </div>

          {settings.auth_mode === 'manual_token' ? (
            <div className='grid gap-1.5 sm:col-span-2'>
              <Label htmlFor='liandong-token'>{t('Merchant token')}</Label>
              <Input
                id='liandong-token'
                type='password'
                value={merchantToken}
                maxLength={512}
                disabled={clearToken}
                placeholder={
                  settings.merchant_token_configured
                    ? t('Configured; enter a new value to replace it')
                    : t('Not configured')
                }
                onChange={(event) => setMerchantToken(event.target.value)}
                autoComplete='new-password'
              />
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Stored only by the backend and sent only when querying Liandong merchant APIs.'
                )}
              </p>
              <div className='flex items-start gap-2 pt-1'>
                <Checkbox
                  id='liandong-clear-token'
                  checked={clearToken}
                  onCheckedChange={(checked) => setClearToken(checked === true)}
                  className='mt-0.5'
                />
                <div className='space-y-0.5'>
                  <Label htmlFor='liandong-clear-token' className='text-xs'>
                    {t('Clear stored merchant token')}
                  </Label>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Applied when settings are saved. Verification cannot run until a new token is configured.'
                    )}
                  </p>
                </div>
              </div>
            </div>
          ) : (
            <>
              <div className='grid gap-1.5'>
                <Label htmlFor='liandong-username'>
                  {t('Liandong account')}
                </Label>
                <Input
                  id='liandong-username'
                  value={username}
                  maxLength={128}
                  disabled={clearUsername}
                  placeholder={
                    settings.username_configured
                      ? t('Configured; enter a new value to replace it')
                      : t('Not configured')
                  }
                  onChange={(event) => setUsername(event.target.value)}
                  autoComplete='off'
                />
                <div className='flex items-center gap-2'>
                  <Checkbox
                    id='liandong-clear-username'
                    checked={clearUsername}
                    onCheckedChange={(checked) =>
                      setClearUsername(checked === true)
                    }
                  />
                  <Label htmlFor='liandong-clear-username' className='text-xs'>
                    {t('Clear stored account')}
                  </Label>
                </div>
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='liandong-password'>
                  {t('Liandong password')}
                </Label>
                <Input
                  id='liandong-password'
                  type='password'
                  value={password}
                  maxLength={256}
                  disabled={clearPassword}
                  placeholder={
                    settings.password_configured
                      ? t('Configured; enter a new value to replace it')
                      : t('Not configured')
                  }
                  onChange={(event) => setPassword(event.target.value)}
                  autoComplete='new-password'
                />
                <div className='flex items-center gap-2'>
                  <Checkbox
                    id='liandong-clear-password'
                    checked={clearPassword}
                    onCheckedChange={(checked) =>
                      setClearPassword(checked === true)
                    }
                  />
                  <Label htmlFor='liandong-clear-password' className='text-xs'>
                    {t('Clear stored password')}
                  </Label>
                </div>
              </div>
              <p className='text-muted-foreground text-xs sm:col-span-2'>
                {t(
                  'Account and password are write-only. A refreshed merchant token is cached only by the backend.'
                )}
              </p>
            </>
          )}
          <div className='grid gap-1.5'>
            <Label htmlFor='liandong-poll-interval'>
              {t('Verification interval (seconds)')}
            </Label>
            <Input
              id='liandong-poll-interval'
              type='number'
              min={1}
              max={3600}
              value={settings.poll_interval_seconds}
              onChange={(event) =>
                setSettings((current) => ({
                  ...current,
                  poll_interval_seconds: event.target.valueAsNumber || 1,
                }))
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Global backend schedule for checking all pending payments. Allowed range: 1 to 3600 seconds.'
              )}
            </p>
          </div>
          <div className='grid gap-1.5'>
            <Label htmlFor='liandong-client-poll-interval'>
              {t('Order status query interval (seconds)')}
            </Label>
            <Input
              id='liandong-client-poll-interval'
              type='number'
              min={1}
              max={60}
              value={settings.client_poll_interval_seconds}
              onChange={(event) =>
                setSettings((current) => ({
                  ...current,
                  client_poll_interval_seconds: event.target.valueAsNumber || 1,
                }))
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Controls how often an open payment dialog queries the local new-api order status. Allowed range: 1 to 60 seconds.'
              )}
            </p>
          </div>
          <div className='grid gap-1.5'>
            <Label htmlFor='liandong-batch-size'>
              {t('Batch verification size')}
            </Label>
            <Input
              id='liandong-batch-size'
              type='number'
              min={1}
              max={500}
              step={1}
              value={settings.reconcile_batch_size}
              onChange={(event) =>
                setSettings((current) => ({
                  ...current,
                  reconcile_batch_size: event.target.valueAsNumber || 1,
                }))
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Number of records requested from the first page of each global verification batch. Allowed range: 1 to 500.'
              )}
            </p>
          </div>
          <div className='grid gap-1.5'>
            <Label htmlFor='liandong-payment-timeout'>
              {t('Payment timeout (minutes)')}
            </Label>
            <Input
              id='liandong-payment-timeout'
              type='number'
              min={1}
              max={1440}
              step={1}
              value={settings.payment_timeout_minutes}
              onChange={(event) =>
                setSettings((current) => ({
                  ...current,
                  payment_timeout_minutes: event.target.valueAsNumber || 1,
                }))
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'The payment dialog counts down from this server-side timeout. Expired unpaid orders are closed and reserved stock is released.'
              )}
            </p>
          </div>
        </div>

        <div className='flex justify-end'>
          <Button onClick={saveSettings} disabled={savingSettings}>
            {savingSettings ? (
              <Loader2 className='h-4 w-4 animate-spin' />
            ) : (
              <Save className='h-4 w-4' />
            )}
            {t('Save settings')}
          </Button>
        </div>
      </div>

      <div className='space-y-4 border-b pb-6'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div>
            <h4 className='font-medium'>{t('Liandong products')}</h4>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Each fixed product maps one goods key to a server-side activation target.'
              )}
            </p>
          </div>
          <Button variant='outline' onClick={openCreateProduct}>
            <Plus className='h-4 w-4' />
            {t('Add product')}
          </Button>
        </div>

        <StaticDataTable
          tableClassName='min-w-[1040px]'
          data={products}
          getRowKey={(product) => product.id}
          emptyContent={t('No products configured')}
          columns={[
            {
              id: 'thumbnail',
              header: t('Image'),
              cell: (product) => (
                <div className='bg-muted flex h-12 w-12 items-center justify-center overflow-hidden rounded border'>
                  {product.thumbnail_url ? (
                    <img
                      src={product.thumbnail_url}
                      alt={product.name}
                      className='h-full w-full object-cover'
                    />
                  ) : (
                    <ImageIcon className='text-muted-foreground h-5 w-5' />
                  )}
                </div>
              ),
            },
            {
              id: 'name',
              header: t('Product'),
              cell: (product) => (
                <div className='min-w-0'>
                  <p className='truncate font-medium'>{product.name}</p>
                  <p className='text-muted-foreground truncate font-mono text-xs'>
                    {product.goods_key}
                  </p>
                </div>
              ),
            },
            {
              id: 'goods_type',
              header: t('Goods type'),
              cell: (product) => goodsTypeLabel(product.goods_type, t),
            },
            {
              id: 'target',
              header: t('Activation target'),
              cell: (product) =>
                product.business_type === 'quota'
                  ? formatLiandongQuota(product.quota_amount)
                  : `${t('Plan')} #${product.plan_id}`,
            },
            {
              id: 'inventory',
              header: t('Inventory'),
              cell: (product) =>
                product.inventory_mode === 'unlimited' ? (
                  <StatusBadge
                    label={t('Unlimited stock')}
                    variant='success'
                    copyable={false}
                  />
                ) : (
                  <div className='space-y-1 text-xs'>
                    <StatusBadge
                      label={inventoryLevelLabel(product.inventory_level, t)}
                      variant={inventoryLevelVariant(product.inventory_level)}
                      copyable={false}
                    />
                    <p>
                      {t('Available {{available}} / Capacity {{capacity}}', {
                        available: product.inventory_available,
                        capacity: product.inventory_capacity,
                      })}
                    </p>
                    <p className='text-muted-foreground'>
                      {t('Reserved {{reserved}}, Consumed {{consumed}}', {
                        reserved: product.inventory_reserved,
                        consumed: product.inventory_consumed,
                      })}
                    </p>
                  </div>
                ),
            },
            {
              id: 'amount',
              header: t('Payment amount'),
              cell: (product) =>
                formatLiandongAmount(
                  product.currency,
                  product.expected_amount_minor
                ),
            },
            {
              id: 'enabled',
              header: t('Enabled'),
              cell: (product) => (
                <Switch
                  checked={product.enabled}
                  onCheckedChange={() => void toggleProduct(product)}
                  disabled={orderAction === `product-${product.id}`}
                  aria-label={t('Toggle product')}
                />
              ),
            },
            {
              id: 'actions',
              header: t('Actions'),
              className: 'text-right',
              cellClassName: 'text-right',
              cell: (product) => (
                <Button
                  variant='ghost'
                  size='icon-sm'
                  onClick={() => openEditProduct(product)}
                  aria-label={t('Edit')}
                >
                  <Pencil className='h-4 w-4' />
                </Button>
              ),
            },
          ]}
        />
      </div>

      <div className='space-y-4'>
        <div>
          <h4 className='font-medium'>{t('Liandong order operations')}</h4>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Payment status is confirmed by global batch reconciliation and exact order verification.'
            )}
          </p>
        </div>

        <form
          className='flex gap-2'
          onSubmit={(event) => {
            event.preventDefault()
            setPage(1)
            setSearchKeyword(keyword.trim())
          }}
        >
          <div className='relative min-w-0 flex-1'>
            <Search className='text-muted-foreground absolute top-2.5 left-3 h-4 w-4' />
            <Input
              value={keyword}
              onChange={(event) => setKeyword(event.target.value)}
              placeholder={t('Search order number, product, or user ID')}
              className='pl-9'
            />
          </div>
          <Button type='submit' variant='outline'>
            {t('Search')}
          </Button>
          <Button
            type='button'
            variant='ghost'
            size='icon'
            onClick={() => void loadOrders()}
            aria-label={t('Refresh')}
          >
            <RefreshCw className='h-4 w-4' />
          </Button>
        </form>

        <StaticDataTable
          tableClassName='min-w-[1120px]'
          data={orders}
          getRowKey={(order) => order.local_trade_no}
          emptyContent={t('No matching orders')}
          columns={[
            {
              id: 'order',
              header: t('Order'),
              cell: (order) => (
                <div className='min-w-0'>
                  <p className='max-w-52 truncate font-mono text-xs'>
                    {order.local_trade_no}
                  </p>
                  <p className='text-muted-foreground max-w-52 truncate text-xs'>
                    {order.provider_trade_no || '-'}
                  </p>
                </div>
              ),
            },
            {
              id: 'user',
              header: t('User'),
              cell: (order) => `#${order.user_id}`,
            },
            {
              id: 'product',
              header: t('Product'),
              cell: (order) => order.product_name,
            },
            {
              id: 'status',
              header: t('Status'),
              cell: (order) => (
                <div className='flex flex-col items-start gap-1'>
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
                  {order.late_payment && (
                    <StatusBadge
                      label={t('Late payment')}
                      variant='warning'
                      copyable={false}
                    />
                  )}
                </div>
              ),
            },
            {
              id: 'checks',
              header: t('Checks'),
              cell: (order) => (
                <div className='text-xs'>
                  <p>{order.check_count}</p>
                  {order.last_error && (
                    <p
                      className='text-destructive max-w-40 truncate'
                      title={order.last_error}
                    >
                      {order.last_error}
                    </p>
                  )}
                </div>
              ),
            },
            {
              id: 'created',
              header: t('Timeline'),
              cell: (order) => (
                <div className='space-y-1 text-xs'>
                  <p>
                    {t('Created')}: {formatTimestamp(order.created_at)}
                  </p>
                  <p>
                    {t('Expires')}: {formatTimestamp(order.expires_at)}
                  </p>
                  {order.closed_reason && (
                    <p
                      className='text-muted-foreground max-w-48 truncate'
                      title={order.closed_reason}
                    >
                      {t('Closed reason')}: {order.closed_reason}
                    </p>
                  )}
                </div>
              ),
            },
            {
              id: 'actions',
              header: t('Actions'),
              className: 'text-right',
              cellClassName: 'text-right',
              cell: (order) => {
                const busy = orderAction.endsWith(order.local_trade_no)
                const canClose = !['paid', 'closed'].includes(
                  order.payment_status
                )
                const canRetry =
                  order.payment_status === 'paid' &&
                  order.fulfillment_status !== 'fulfilled'
                const canManualFulfill =
                  order.late_payment &&
                  order.payment_status === 'review_required' &&
                  order.fulfillment_status === 'review_required'
                const canRequeue =
                  (order.payment_status === 'paid' &&
                    ['waiting', 'failed'].includes(order.fulfillment_status)) ||
                  (!!order.provider_trade_no &&
                    [
                      'pending',
                      'create_unknown',
                      'expired',
                      'review_required',
                      'closed',
                    ].includes(order.payment_status))
                return (
                  <div className='flex justify-end gap-1'>
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <Button
                            variant='ghost'
                            size='icon-sm'
                            onClick={() =>
                              void runOrderAction(order, 'requeue')
                            }
                            disabled={busy || !canRequeue}
                            aria-label={t('Requeue verification')}
                          />
                        }
                      >
                        <RotateCcw className='h-4 w-4' />
                      </TooltipTrigger>
                      <TooltipContent>
                        {t('Requeue verification')}
                      </TooltipContent>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <Button
                            variant='ghost'
                            size='icon-sm'
                            onClick={() => void runOrderAction(order, 'retry')}
                            disabled={busy || !canRetry}
                            aria-label={t('Retry activation')}
                          />
                        }
                      >
                        <RefreshCw className='h-4 w-4' />
                      </TooltipTrigger>
                      <TooltipContent>{t('Retry activation')}</TooltipContent>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <Button
                            variant='ghost'
                            size='icon-sm'
                            onClick={() => void runOrderAction(order, 'manual')}
                            disabled={busy || !canManualFulfill}
                            aria-label={t('Manually fulfill late payment')}
                          />
                        }
                      >
                        <ShieldCheck className='h-4 w-4' />
                      </TooltipTrigger>
                      <TooltipContent>
                        {t('Manually fulfill late payment')}
                      </TooltipContent>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <Button
                            variant='ghost'
                            size='icon-sm'
                            onClick={() => setCloseOrder(order)}
                            disabled={busy || !canClose}
                            aria-label={t('Close order')}
                            className='text-destructive'
                          />
                        }
                      >
                        <CircleX className='h-4 w-4' />
                      </TooltipTrigger>
                      <TooltipContent>{t('Close order')}</TooltipContent>
                    </Tooltip>
                  </div>
                )
              },
            },
          ]}
        />

        <div className='flex items-center justify-between gap-3'>
          <p className='text-muted-foreground text-xs'>
            {t('{{count}} orders', { count: orderTotal })}
          </p>
          <div className='flex items-center gap-2'>
            <Button
              variant='outline'
              size='icon-sm'
              onClick={() => setPage((current) => Math.max(1, current - 1))}
              disabled={page <= 1}
              aria-label={t('Previous page')}
            >
              <ChevronLeft className='h-4 w-4' />
            </Button>
            <span className='text-sm'>
              {page} / {totalPages}
            </span>
            <Button
              variant='outline'
              size='icon-sm'
              onClick={() =>
                setPage((current) => Math.min(totalPages, current + 1))
              }
              disabled={page >= totalPages}
              aria-label={t('Next page')}
            >
              <ChevronRight className='h-4 w-4' />
            </Button>
          </div>
        </div>
      </div>

      <Dialog
        open={productDialogOpen}
        onOpenChange={setProductDialogOpen}
        title={editingProduct ? t('Edit product') : t('Add product')}
        contentHeight='min(72vh, 720px)'
        contentClassName='sm:max-w-4xl'
        bodyClassName='space-y-4'
        footer={
          <>
            <Button
              variant='outline'
              onClick={() => setProductDialogOpen(false)}
              disabled={savingProduct}
            >
              {t('Cancel')}
            </Button>
            <Button onClick={saveProduct} disabled={savingProduct}>
              {savingProduct && <Loader2 className='h-4 w-4 animate-spin' />}
              {t('Save')}
            </Button>
          </>
        }
      >
        <div className='space-y-5'>
          <div className='space-y-3 border-b pb-5'>
            <div>
              <h5 className='text-sm font-medium'>
                {t('Liandong goods quick fill')}
              </h5>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Query enabled goods from the Liandong platform and fill the goods key, name, and type.'
                )}
              </p>
            </div>
            <div className='grid gap-2 sm:grid-cols-[180px_minmax(0,1fr)_auto]'>
              <Select
                items={[
                  { value: 'card', label: t('Card key') },
                  { value: 'article', label: t('Knowledge') },
                  { value: 'resource', label: t('Resource') },
                  { value: 'equity', label: t('Entitlement') },
                ]}
                value={productForm.goods_type}
                onValueChange={(goodsType) =>
                  setProductForm((current) => ({
                    ...current,
                    goods_type: goodsType || 'card',
                  }))
                }
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    <SelectItem value='card'>{t('Card key')}</SelectItem>
                    <SelectItem value='article'>{t('Knowledge')}</SelectItem>
                    <SelectItem value='resource'>{t('Resource')}</SelectItem>
                    <SelectItem value='equity'>{t('Entitlement')}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              <Input
                value={providerGoodsKeyword}
                onChange={(event) =>
                  setProviderGoodsKeyword(event.target.value)
                }
                placeholder={t('Search Liandong goods by name')}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') {
                    event.preventDefault()
                    void searchProviderGoods()
                  }
                }}
              />
              <Button
                type='button'
                variant='outline'
                onClick={() => void searchProviderGoods()}
                disabled={loadingProviderGoods}
              >
                {loadingProviderGoods ? (
                  <Loader2 className='h-4 w-4 animate-spin' />
                ) : (
                  <Search className='h-4 w-4' />
                )}
                {t('Query goods')}
              </Button>
            </div>
            {providerGoods.length > 0 && (
              <div className='max-h-44 overflow-y-auto rounded border'>
                {providerGoods.map((goods) => {
                  const selected =
                    productForm.goods_key === goods.goods_key &&
                    productForm.goods_type === goods.goods_type
                  return (
                    <button
                      key={`${goods.goods_type}-${goods.goods_key}`}
                      type='button'
                      className='hover:bg-accent flex w-full items-center gap-3 border-b px-3 py-2 text-left text-sm last:border-b-0'
                      onClick={() =>
                        setProductForm((current) => ({
                          ...current,
                          goods_type: goods.goods_type || current.goods_type,
                          goods_key: goods.goods_key,
                          name: goods.name,
                        }))
                      }
                    >
                      <div className='min-w-0 flex-1'>
                        <p className='truncate font-medium'>{goods.name}</p>
                        <p className='text-muted-foreground truncate font-mono text-xs'>
                          {goods.goods_key}
                        </p>
                      </div>
                      <span className='text-muted-foreground text-xs'>
                        {goodsTypeLabel(goods.goods_type, t)}
                      </span>
                      {selected && <Check className='text-primary h-4 w-4' />}
                    </button>
                  )
                })}
              </div>
            )}
          </div>

          <div className='space-y-3 border-b pb-5'>
            <div className='flex items-center gap-2'>
              <ImageIcon className='h-4 w-4' />
              <h5 className='text-sm font-medium'>{t('Product thumbnail')}</h5>
            </div>
            <LiandongThumbnailEditor
              currentUrl={editingProduct?.thumbnail_url}
              deleting={deletingThumbnail}
              onBlobChange={setThumbnailBlob}
              onDelete={editingProduct ? deleteThumbnail : undefined}
            />
          </div>

          <div className='grid gap-4 border-b pb-5 sm:grid-cols-2'>
            <div className='grid gap-1.5'>
              <Label>{t('Product name')}</Label>
              <Input
                value={productForm.name}
                maxLength={128}
                onChange={(event) =>
                  setProductForm((current) => ({
                    ...current,
                    name: event.target.value,
                  }))
                }
              />
              <p className='text-muted-foreground text-xs'>
                {t('Displayed to users when they choose a fixed product.')}
              </p>
            </div>
            <div className='grid gap-1.5'>
              <Label>{t('Goods key')}</Label>
              <Input
                value={productForm.goods_key}
                maxLength={128}
                className='font-mono'
                onChange={(event) =>
                  setProductForm((current) => ({
                    ...current,
                    goods_key: event.target.value,
                  }))
                }
              />
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Must match the goods_key configured for this product on the Liandong platform.'
                )}
              </p>
            </div>
            <div className='grid gap-1.5'>
              <Label>{t('Business type')}</Label>
              <Select
                items={[
                  { value: 'quota', label: t('Quota recharge') },
                  { value: 'subscription', label: t('Subscription') },
                ]}
                value={productForm.business_type}
                onValueChange={(value) => {
                  if (value !== 'quota' && value !== 'subscription') return
                  setProductForm((current) => ({
                    ...current,
                    business_type: value,
                  }))
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    <SelectItem value='quota'>{t('Quota recharge')}</SelectItem>
                    <SelectItem value='subscription'>
                      {t('Subscription')}
                    </SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <div className='grid gap-1.5'>
              <Label>{t('Goods type')}</Label>
              <Select
                items={[
                  { value: 'card', label: t('Card key') },
                  { value: 'article', label: t('Knowledge') },
                  { value: 'resource', label: t('Resource') },
                  { value: 'equity', label: t('Entitlement') },
                ]}
                value={productForm.goods_type}
                onValueChange={(goodsType) =>
                  setProductForm((current) => ({
                    ...current,
                    goods_type: goodsType || 'card',
                  }))
                }
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    <SelectItem value='card'>{t('Card key')}</SelectItem>
                    <SelectItem value='article'>{t('Knowledge')}</SelectItem>
                    <SelectItem value='resource'>{t('Resource')}</SelectItem>
                    <SelectItem value='equity'>{t('Entitlement')}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>

            {productForm.business_type === 'quota' ? (
              <div className='grid gap-1.5 sm:col-span-2'>
                <Label>{t('Quota amount')}</Label>
                <Input
                  type='number'
                  min={1}
                  step={1}
                  value={productForm.quota_amount}
                  onChange={(event) =>
                    setProductForm((current) => ({
                      ...current,
                      quota_amount: event.target.valueAsNumber || 0,
                    }))
                  }
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Positive whole-number quota credited after payment succeeds.'
                  )}
                  {productForm.quota_amount > 0 && (
                    <>
                      {' '}
                      {t('Displayed quota: {{value}}', {
                        value: formatLiandongQuota(productForm.quota_amount),
                      })}
                    </>
                  )}
                </p>
              </div>
            ) : (
              <div className='grid gap-1.5 sm:col-span-2'>
                <Label>{t('Subscription plan')}</Label>
                <Select
                  items={planOptions.map((plan) => ({
                    value: String(plan.id),
                    label: plan.title,
                  }))}
                  value={
                    productForm.plan_id > 0 ? String(productForm.plan_id) : null
                  }
                  onValueChange={(value) =>
                    setProductForm((current) => ({
                      ...current,
                      plan_id: Number(value || 0),
                    }))
                  }
                >
                  <SelectTrigger>
                    <SelectValue>{t('Select subscription plan')}</SelectValue>
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {planOptions.map((plan) => (
                        <SelectItem key={plan.id} value={String(plan.id)}>
                          {plan.title}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                {selectedPlan ? (
                  <div className='bg-muted/40 grid gap-2 rounded border p-3 text-xs sm:grid-cols-2'>
                    <p>
                      {t('Validity Period')}: {formatDuration(selectedPlan, t)}
                    </p>
                    <p>
                      {t('Quota')}:{' '}
                      {formatLiandongQuota(selectedPlan.total_amount)}
                    </p>
                    <p>
                      {t('Quota Reset')}: {formatResetPeriod(selectedPlan, t)}
                    </p>
                    <p>
                      {t('User group')}:{' '}
                      {selectedPlan.upgrade_group || t('No change')}
                    </p>
                  </div>
                ) : (
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Subscription plan activated after payment succeeds. The plan snapshot is reserved when the order is created.'
                    )}
                  </p>
                )}
              </div>
            )}

            <div className='grid gap-1.5'>
              <Label>{t('Currency')}</Label>
              <Select
                items={[
                  { value: 'CNY', label: t('CNY (￥)') },
                  { value: 'USD', label: t('USD ($)') },
                ]}
                value={productForm.currency}
                onValueChange={(currency) =>
                  setProductForm((current) => ({
                    ...current,
                    currency: currency || 'CNY',
                  }))
                }
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    <SelectItem value='CNY'>{t('CNY (￥)')}</SelectItem>
                    <SelectItem value='USD'>{t('USD ($)')}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <div className='grid gap-1.5'>
              <Label>
                {productForm.currency === 'USD'
                  ? t('Payment amount ($)')
                  : t('Payment amount (￥)')}
              </Label>
              <Input
                type='number'
                min={0.01}
                step={0.01}
                value={paymentAmountInput}
                onChange={(event) => setPaymentAmountInput(event.target.value)}
              />
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Enter a positive amount with no more than two decimal places.'
                )}
              </p>
            </div>
          </div>

          <div className='space-y-3 border-b pb-5'>
            <div className='flex items-center gap-2'>
              <Boxes className='h-4 w-4' />
              <h5 className='text-sm font-medium'>{t('Inventory settings')}</h5>
            </div>
            <div className='grid gap-4 sm:grid-cols-2'>
              <div className='grid gap-1.5'>
                <Label>{t('Inventory mode')}</Label>
                <Select
                  items={[
                    { value: 'unlimited', label: t('Unlimited stock') },
                    {
                      value: 'redemption_code',
                      label: t('Internal redemption-code stock'),
                    },
                  ]}
                  value={productForm.inventory_mode}
                  onValueChange={(inventoryMode) => {
                    if (
                      inventoryMode !== 'unlimited' &&
                      inventoryMode !== 'redemption_code'
                    ) {
                      return
                    }
                    setProductForm((current) => ({
                      ...current,
                      inventory_mode: inventoryMode,
                      inventory_capacity:
                        inventoryMode === 'unlimited'
                          ? 0
                          : Math.max(1, current.inventory_capacity),
                    }))
                  }}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='unlimited'>
                        {t('Unlimited stock')}
                      </SelectItem>
                      <SelectItem value='redemption_code'>
                        {t('Internal redemption-code stock')}
                      </SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Internal stock codes are reservation units only. They are never delivered to users and cannot be redeemed through the normal redemption endpoint.'
                  )}
                </p>
              </div>
              {productForm.inventory_mode === 'redemption_code' && (
                <div className='grid gap-1.5'>
                  <Label>{t('Inventory capacity')}</Label>
                  <Input
                    type='number'
                    min={1}
                    step={1}
                    value={productForm.inventory_capacity}
                    onChange={(event) =>
                      setProductForm((current) => ({
                        ...current,
                        inventory_capacity: event.target.valueAsNumber || 0,
                      }))
                    }
                  />
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Available plus reserved stock cannot exceed this fixed selling capacity.'
                    )}
                  </p>
                </div>
              )}
            </div>

            {productForm.inventory_mode === 'redemption_code' && (
              <div className='grid gap-4 sm:grid-cols-2'>
                <div className='space-y-2 rounded border p-3'>
                  <div className='flex items-center gap-2'>
                    <KeyRound className='h-4 w-4' />
                    <Label>{t('Batch add inventory')}</Label>
                  </div>
                  <Input
                    type='number'
                    min={0}
                    max={1000}
                    step={1}
                    value={inventoryAddCount}
                    onChange={(event) =>
                      setInventoryAddCount(event.target.valueAsNumber || 0)
                    }
                    placeholder={t('Quantity')}
                  />
                  <Input
                    value={inventoryName}
                    maxLength={128}
                    onChange={(event) => setInventoryName(event.target.value)}
                    placeholder={t(
                      'Inventory code name; defaults to product name'
                    )}
                  />
                  <p className='text-muted-foreground text-xs'>
                    {editingProduct
                      ? t(
                          'The quantity is added when the product is saved, or can be added immediately with the button below.'
                        )
                      : t(
                          'The quantity is generated after the product is created.'
                        )}
                  </p>
                  {editingProduct && (
                    <Button
                      type='button'
                      variant='outline'
                      onClick={() => void runInventoryAction('add')}
                      disabled={inventoryAction !== ''}
                    >
                      {inventoryAction === 'add' ? (
                        <Loader2 className='h-4 w-4 animate-spin' />
                      ) : (
                        <Plus className='h-4 w-4' />
                      )}
                      {t('Add inventory now')}
                    </Button>
                  )}
                </div>

                {editingProduct && (
                  <div className='space-y-2 rounded border p-3'>
                    <Label>{t('Current inventory')}</Label>
                    <div className='grid grid-cols-2 gap-2 text-xs'>
                      <p>
                        {t('Available')}: {editingProduct.inventory_available}
                      </p>
                      <p>
                        {t('Reserved')}: {editingProduct.inventory_reserved}
                      </p>
                      <p>
                        {t('Consumed')}: {editingProduct.inventory_consumed}
                      </p>
                      <p>
                        {t('Disabled')}: {editingProduct.inventory_disabled}
                      </p>
                    </div>
                    <Input
                      type='number'
                      min={0}
                      max={1000}
                      step={1}
                      value={inventoryDisableCount}
                      onChange={(event) =>
                        setInventoryDisableCount(
                          event.target.valueAsNumber || 0
                        )
                      }
                      placeholder={t('Available quantity to disable')}
                    />
                    <Button
                      type='button'
                      variant='outline'
                      className='text-destructive'
                      onClick={() => void runInventoryAction('disable')}
                      disabled={inventoryAction !== ''}
                    >
                      {inventoryAction === 'disable' ? (
                        <Loader2 className='h-4 w-4 animate-spin' />
                      ) : (
                        <CircleX className='h-4 w-4' />
                      )}
                      {t('Disable available inventory')}
                    </Button>
                  </div>
                )}
              </div>
            )}
          </div>

          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='grid gap-1.5'>
              <Label>{t('Sort order')}</Label>
              <Input
                type='number'
                step={1}
                value={productForm.sort_order}
                onChange={(event) =>
                  setProductForm((current) => ({
                    ...current,
                    sort_order: event.target.valueAsNumber || 0,
                  }))
                }
              />
              <p className='text-muted-foreground text-xs'>
                {t('Products with a larger sort value are displayed first.')}
              </p>
            </div>
            <div className='space-y-1'>
              <div className='flex items-center justify-between gap-3'>
                <Label>{t('Enabled')}</Label>
                <Switch
                  checked={productForm.enabled}
                  onCheckedChange={(enabled) =>
                    setProductForm((current) => ({ ...current, enabled }))
                  }
                  aria-label={t('Enabled')}
                />
              </div>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Only enabled products are visible to users and can create new payment orders.'
                )}
              </p>
            </div>
          </div>
        </div>
      </Dialog>

      <ConfirmDialog
        open={!!closeOrder}
        onOpenChange={(open) => {
          if (!open) setCloseOrder(null)
        }}
        title={t('Close payment order')}
        desc={t(
          'The user may still complete payment later. After closing, this order will not be verified or activated automatically.'
        )}
        destructive
        confirmText={t('Close order')}
        isLoading={
          !!closeOrder && orderAction === `close-${closeOrder.local_trade_no}`
        }
        handleConfirm={() => {
          if (closeOrder) void runOrderAction(closeOrder, 'close')
        }}
      />
    </SettingsSection>
  )
}
