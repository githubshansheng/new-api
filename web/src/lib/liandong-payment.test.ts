import { describe, expect, test } from 'vitest'

import {
  parsePaymentAmountMinor,
  paymentAmountInputFromMinor,
} from './liandong-amount.ts'
import {
  calculateLiandongEffectiveQuotaUSD,
  formatLiandongEffectiveQuota,
  formatLiandongGroupRatio,
} from './liandong-payment.ts'

describe('Liandong payment amount helpers', () => {
  test('converts valid decimal strings to integer minor units', () => {
    expect(parsePaymentAmountMinor('0.01')).toBe(1)
    expect(parsePaymentAmountMinor('1.2')).toBe(120)
    expect(parsePaymentAmountMinor('1.23')).toBe(123)
  })

  test('rejects invalid, non-positive, and over-precise amounts', () => {
    expect(parsePaymentAmountMinor('1.234')).toBeNull()
    expect(parsePaymentAmountMinor('0')).toBeNull()
    expect(parsePaymentAmountMinor('-1')).toBeNull()
    expect(parsePaymentAmountMinor('invalid')).toBeNull()
  })

  test('formats integer minor units without unnecessary trailing zeroes', () => {
    expect(paymentAmountInputFromMinor(1)).toBe('0.01')
    expect(paymentAmountInputFromMinor(120)).toBe('1.2')
    expect(paymentAmountInputFromMinor(123)).toBe('1.23')
  })
})

describe('Liandong effective quota helpers', () => {
  test('divides the base USD quota by the target group ratio', () => {
    expect(
      calculateLiandongEffectiveQuotaUSD(100_000_000, 0.25, 500_000)
    ).toBe(800)
  })

  test('formats the base quota as fixed CNY independently of display currency', () => {
    expect(
      formatLiandongEffectiveQuota(100_000_000, 0.25, 500_000, '官方')
    ).toBe('≈官方$800（￥200）')
    expect(
      formatLiandongEffectiveQuota(100_000_000, 1, 500_000, '官方')
    ).toBe('≈官方$200（￥200）')
  })

  test('rounds the calculated official quota to an integer', () => {
    expect(
      formatLiandongEffectiveQuota(100_000_000, 0.35, 500_000, '官方')
    ).toBe('≈官方$571（￥200）')
  })

  test('rejects zero ratios and invalid quota units', () => {
    expect(calculateLiandongEffectiveQuotaUSD(100, 0, 500_000)).toBeNull()
    expect(calculateLiandongEffectiveQuotaUSD(100, 1, 0)).toBeNull()
  })

  test('formats Chinese group ratios with the requested decimal comma', () => {
    expect(formatLiandongGroupRatio('vip', 0.25, '倍率', 'zhCN')).toBe(
      'vip(倍率0,25)'
    )
  })
})
