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
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { createLiandongOrder, getLiandongPaymentPage } from '../../api'
import type { LiandongPaymentResponse, LiandongProduct } from '../../types'
import { LiandongPaymentDialog } from '../dialogs/liandong-payment-dialog'

vi.mock('../../api', () => ({
  createLiandongOrder: vi.fn(),
  getLiandongOrder: vi.fn(),
  getLiandongPaymentPage: vi.fn(),
  closeLiandongOrderForUser: vi.fn(),
}))

vi.mock('@/hooks/use-system-config', () => ({
  useSystemConfig: () => ({ currency: { quotaPerUnit: 500_000 } }),
}))

const product: LiandongProduct = {
  id: 1,
  business_type: 'quota',
  goods_type: 'card',
  name: 'Quota package',
  quota_amount: 500_000,
  group_ratio: 1,
  plan_id: 0,
  expected_amount_minor: 100,
  currency: 'CNY',
  inventory_level: 'unlimited',
}

const fallbackOrder = {
  success: true,
  message: '',
  data: {
    local_trade_no: 'LD-iframe-regression',
    product_name: product.name,
    business_type: 'quota',
    payment_status: 'pending',
    fulfillment_status: 'waiting',
    iframe_allowed: true,
    fallback_url: 'https://provider.example/item/quota',
    fallback_contact: '123456789012',
    client_poll_interval_seconds: 60,
    created_at: 1,
  },
} satisfies LiandongPaymentResponse

beforeEach(() => {
  vi.mocked(createLiandongOrder).mockResolvedValue(fallbackOrder)
  vi.mocked(getLiandongPaymentPage).mockReset()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('LiandongPaymentDialog payment navigation', () => {
  test('embeds the fallback product page when iframe is enabled', async () => {
    render(
      <LiandongPaymentDialog
        open
        onOpenChange={vi.fn()}
        product={product}
        attemptId={1}
      />
    )

    const frame = await screen.findByTitle('Liandong payment page')
    expect(frame).toHaveAttribute('src', fallbackOrder.data.fallback_url)
    expect(frame.getAttribute('sandbox')).not.toContain('allow-top-navigation')
    expect(screen.getByText('123456789012')).toBeInTheDocument()
  })

  test('keeps a direct payment link without embedding when iframe is disabled', async () => {
    vi.mocked(createLiandongOrder).mockResolvedValue({
      ...fallbackOrder,
      data: { ...fallbackOrder.data, iframe_allowed: false },
    })
    render(
      <LiandongPaymentDialog
        open
        onOpenChange={vi.fn()}
        product={product}
        attemptId={1}
      />
    )

    const link = await screen.findByRole('link', { name: 'Open payment page' })
    expect(link).toHaveAttribute('href', fallbackOrder.data.fallback_url)
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(screen.queryByTitle('Liandong payment page')).not.toBeInTheDocument()
  })

  test('does not block native payment navigation while clipboard permission is pending', async () => {
    vi.stubGlobal(
      'navigator',
      Object.create(navigator, {
        clipboard: {
          value: { writeText: vi.fn(() => new Promise<void>(() => {})) },
        },
      })
    )
    const openWindow = vi.spyOn(window, 'open').mockReturnValue(null)
    render(
      <LiandongPaymentDialog
        open
        onOpenChange={vi.fn()}
        product={product}
        attemptId={1}
      />
    )

    const link = await screen.findByRole('link', {
      name: 'Copy contact and open payment page',
    })
    let navigationPrevented = true
    const inspectNavigation = (event: MouseEvent) => {
      navigationPrevented = event.defaultPrevented
      event.preventDefault()
    }
    document.addEventListener('click', inspectNavigation, { once: true })
    fireEvent.click(link)

    expect(navigationPrevented).toBe(false)
    expect(link).toHaveAttribute('href', fallbackOrder.data.fallback_url)
    expect(openWindow).not.toHaveBeenCalled()
  })

  test('prepares a sandboxed HTML payment link before allowing a new tab', async () => {
    vi.mocked(createLiandongOrder).mockResolvedValue({
      ...fallbackOrder,
      data: {
        ...fallbackOrder.data,
        fallback_url: undefined,
        payment_url: '/api/payment/liandong/orders/LD-iframe-regression/page',
      },
    })
    let resolvePage!: (
      value: Awaited<ReturnType<typeof getLiandongPaymentPage>>
    ) => void
    const page = new Promise<
      Awaited<ReturnType<typeof getLiandongPaymentPage>>
    >((resolve) => {
      resolvePage = resolve
    })
    vi.mocked(getLiandongPaymentPage).mockReturnValue(page)
    const createObjectURL = vi.fn(
      () => 'blob:http://localhost/payment-document'
    )
    const revokeObjectURL = vi.fn()
    vi.stubGlobal(
      'URL',
      class extends URL {
        static createObjectURL = createObjectURL
        static revokeObjectURL = revokeObjectURL
      }
    )
    const rendered = render(
      <LiandongPaymentDialog
        open
        onOpenChange={vi.fn()}
        product={product}
        attemptId={1}
      />
    )

    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Open payment page' })
      ).toBeDisabled()
    )
    await act(async () => {
      resolvePage({
        success: true,
        message: '',
        data: { html: '<!doctype html><html><body>Payment QR</body></html>' },
      })
    })
    const link = await screen.findByRole('link', { name: 'Open payment page' })
    expect(link).toHaveAttribute(
      'href',
      'blob:http://localhost/payment-document'
    )
    const frame = screen.getByTitle('Liandong payment page')
    expect(frame.getAttribute('sandbox')).not.toContain('allow-same-origin')
    expect(frame.getAttribute('src')).toContain('data:text/html')
    rendered.unmount()
    expect(revokeObjectURL).toHaveBeenCalledWith(
      'blob:http://localhost/payment-document'
    )
  })

  test('shows page request failures without opening an empty window', async () => {
    vi.mocked(createLiandongOrder).mockResolvedValue({
      ...fallbackOrder,
      data: {
        ...fallbackOrder.data,
        fallback_url: undefined,
        payment_url: '/api/payment/liandong/orders/LD-iframe-regression/page',
      },
    })
    vi.mocked(getLiandongPaymentPage).mockResolvedValue({
      success: false,
      message: 'Failed to open payment page',
    })
    const openWindow = vi.spyOn(window, 'open').mockReturnValue(null)
    render(
      <LiandongPaymentDialog
        open
        onOpenChange={vi.fn()}
        product={product}
        attemptId={1}
      />
    )

    expect(
      await screen.findByText('Failed to open payment page')
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Open payment page' })
    ).toBeDisabled()
    expect(
      screen.queryByRole('link', { name: 'Open payment page' })
    ).not.toBeInTheDocument()
    expect(openWindow).not.toHaveBeenCalled()
  })
})
