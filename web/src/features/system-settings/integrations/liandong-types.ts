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
import type {
  LiandongBusinessType,
  LiandongInventoryLevel,
  LiandongSubscriptionSpec,
} from '@/features/wallet/types'

export type LiandongAuthMode = 'manual_token' | 'credentials'
export type LiandongInventoryMode = 'unlimited' | 'redemption_code'

export type LiandongSettings = {
  enabled: boolean
  create_enabled: boolean
  reconcile_enabled: boolean
  fulfill_enabled: boolean
  iframe_enabled: boolean
  base_url: string
  proxy_enabled: boolean
  proxy_url: string
  proxy_username_configured: boolean
  proxy_password_configured: boolean
  poll_interval_seconds: number
  client_poll_interval_seconds: number
  reconcile_batch_size: number
  payment_timeout_minutes: number
  juuid: string
  auth_mode: LiandongAuthMode
  username_configured: boolean
  password_configured: boolean
  merchant_token_configured: boolean
}

export type LiandongSettingsUpdate = Partial<
  Omit<
    LiandongSettings,
    | 'username_configured'
    | 'password_configured'
    | 'merchant_token_configured'
    | 'proxy_username_configured'
    | 'proxy_password_configured'
  >
> & {
  username?: string
  password?: string
  merchant_token?: string
  clear_username?: boolean
  clear_password?: boolean
  clear_token?: boolean
  proxy_username?: string
  proxy_password?: string
  clear_proxy_username?: boolean
  clear_proxy_password?: boolean
}

export type LiandongRootProduct = {
  id: number
  business_type: LiandongBusinessType
  goods_type: string
  name: string
  goods_key: string
  quota_amount: number
  plan_id: number
  expected_amount_minor: number
  currency: string
  inventory_mode: LiandongInventoryMode
  inventory_capacity: number
  inventory_available: number
  inventory_reserved: number
  inventory_consumed: number
  inventory_disabled: number
  inventory_level: LiandongInventoryLevel
  thumbnail_url?: string
  thumbnail_version?: number
  subscription?: LiandongSubscriptionSpec
  enabled: boolean
  sort_order: number
  created_by: number
  updated_by: number
  created_at: number
  updated_at: number
}

export type LiandongProductPayload = Omit<
  LiandongRootProduct,
  | 'id'
  | 'inventory_available'
  | 'inventory_reserved'
  | 'inventory_consumed'
  | 'inventory_disabled'
  | 'inventory_level'
  | 'thumbnail_url'
  | 'thumbnail_version'
  | 'subscription'
  | 'created_by'
  | 'updated_by'
  | 'created_at'
  | 'updated_at'
>

export type LiandongProviderGoods = {
  goods_key: string
  name: string
  goods_type: string
}

export type LiandongRootOrder = {
  local_trade_no: string
  provider_trade_no?: string
  user_id: number
  product_id: number
  product_name: string
  business_type: LiandongBusinessType
  target_id: number
  expected_amount_minor: number
  currency: string
  payment_status: string
  fulfillment_status: string
  inventory_code_id?: number
  expires_at: number
  closed_reason?: string
  late_payment: boolean
  last_check_at: number
  next_check_at: number
  check_count: number
  consecutive_error_count: number
  last_error?: string
  paid_at: number
  fulfilled_at: number
  created_at: number
  updated_at: number
}

export type LiandongOrderPage = {
  page: number
  page_size: number
  total: number
  items: LiandongRootOrder[]
}

export type LiandongApiResponse<T = unknown> = {
  success: boolean
  message?: string
  data?: T
}
