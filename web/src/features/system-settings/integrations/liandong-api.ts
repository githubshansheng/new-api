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
import { api } from '@/lib/api'

import type {
  LiandongApiResponse,
  LiandongOrderPage,
  LiandongProductPayload,
  LiandongProviderGoods,
  LiandongRootProduct,
  LiandongSettings,
  LiandongSettingsUpdate,
} from './liandong-types'

export async function getLiandongSettings(): Promise<
  LiandongApiResponse<LiandongSettings>
> {
  const response = await api.get('/api/option/liandong')
  return response.data
}

export async function updateLiandongSettings(
  data: LiandongSettingsUpdate
): Promise<LiandongApiResponse> {
  const response = await api.put('/api/option/liandong', data, {
    skipBusinessError: true,
  })
  return response.data
}

export async function listLiandongProducts(): Promise<
  LiandongApiResponse<LiandongRootProduct[]>
> {
  const response = await api.get('/api/option/liandong/products')
  return response.data
}

export async function createLiandongProduct(
  data: LiandongProductPayload
): Promise<LiandongApiResponse<LiandongRootProduct>> {
  const response = await api.post('/api/option/liandong/products', data, {
    skipBusinessError: true,
  })
  return response.data
}

export async function updateLiandongProduct(
  id: number,
  data: LiandongProductPayload
): Promise<LiandongApiResponse<LiandongRootProduct>> {
  const response = await api.patch(
    `/api/option/liandong/products/${id}`,
    data,
    { skipBusinessError: true }
  )
  return response.data
}

export async function listLiandongOrders(
  page: number,
  pageSize: number,
  keyword: string
): Promise<LiandongApiResponse<LiandongOrderPage>> {
  const response = await api.get('/api/option/liandong/orders', {
    params: {
      p: page,
      page_size: pageSize,
      keyword: keyword || undefined,
    },
    disableDuplicate: true,
  })
  return response.data
}

export async function requeueLiandongOrder(
  localTradeNo: string
): Promise<LiandongApiResponse> {
  const response = await api.post(
    `/api/option/liandong/orders/${encodeURIComponent(localTradeNo)}/requeue`,
    null,
    { skipBusinessError: true }
  )
  return response.data
}

export async function closeLiandongOrder(
  localTradeNo: string
): Promise<LiandongApiResponse> {
  const response = await api.post(
    `/api/option/liandong/orders/${encodeURIComponent(localTradeNo)}/close`,
    null,
    { skipBusinessError: true }
  )
  return response.data
}

export async function retryLiandongFulfillment(
  localTradeNo: string
): Promise<LiandongApiResponse> {
  const response = await api.post(
    `/api/option/liandong/orders/${encodeURIComponent(localTradeNo)}/retry-fulfillment`,
    null,
    { skipBusinessError: true }
  )
  return response.data
}

export async function manualFulfillLiandongOrder(
  localTradeNo: string
): Promise<LiandongApiResponse> {
  const response = await api.post(
    `/api/option/liandong/orders/${encodeURIComponent(localTradeNo)}/manual-fulfill`,
    null,
    { skipBusinessError: true }
  )
  return response.data
}

export async function listLiandongProviderGoods(
  goodsType: string,
  keyword: string
): Promise<LiandongApiResponse<LiandongProviderGoods[]>> {
  const response = await api.get('/api/option/liandong/provider-goods', {
    params: {
      goods_type: goodsType || undefined,
      name: keyword || undefined,
    },
    disableDuplicate: true,
    skipBusinessError: true,
  })
  return response.data
}

export async function addLiandongInventory(
  productId: number,
  count: number,
  name: string
): Promise<LiandongApiResponse<{ created: number }>> {
  const response = await api.post(
    `/api/option/liandong/products/${productId}/inventory`,
    { count, name },
    { skipBusinessError: true }
  )
  return response.data
}

export async function disableLiandongInventory(
  productId: number,
  count: number
): Promise<LiandongApiResponse> {
  const response = await api.post(
    `/api/option/liandong/products/${productId}/inventory/disable`,
    { count },
    { skipBusinessError: true }
  )
  return response.data
}

export async function uploadLiandongThumbnail(
  productId: number,
  file: Blob
): Promise<LiandongApiResponse> {
  const body = new FormData()
  body.append('file', file, 'liandong-product.webp')
  const response = await api.put(
    `/api/option/liandong/products/${productId}/thumbnail`,
    body,
    { skipBusinessError: true }
  )
  return response.data
}

export async function deleteLiandongThumbnail(
  productId: number
): Promise<LiandongApiResponse> {
  const response = await api.delete(
    `/api/option/liandong/products/${productId}/thumbnail`,
    { skipBusinessError: true }
  )
  return response.data
}
