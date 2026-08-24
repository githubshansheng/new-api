import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

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
    assert.equal(parsePaymentAmountMinor('0.01'), 1)
    assert.equal(parsePaymentAmountMinor('1.2'), 120)
    assert.equal(parsePaymentAmountMinor('1.23'), 123)
  })

  test('rejects invalid, non-positive, and over-precise amounts', () => {
    assert.equal(parsePaymentAmountMinor('1.234'), null)
    assert.equal(parsePaymentAmountMinor('0'), null)
    assert.equal(parsePaymentAmountMinor('-1'), null)
    assert.equal(parsePaymentAmountMinor('invalid'), null)
  })

  test('formats integer minor units without unnecessary trailing zeroes', () => {
    assert.equal(paymentAmountInputFromMinor(1), '0.01')
    assert.equal(paymentAmountInputFromMinor(120), '1.2')
    assert.equal(paymentAmountInputFromMinor(123), '1.23')
  })
})

describe('Liandong effective quota helpers', () => {
  test('divides the base USD quota by the target group ratio', () => {
    assert.equal(
      calculateLiandongEffectiveQuotaUSD(100_000_000, 0.25, 500_000),
      800
    )
  })

  test('formats the base quota as fixed CNY independently of display currency', () => {
    assert.equal(
      formatLiandongEffectiveQuota(100_000_000, 0.25, 500_000, '官方'),
      '≈官方$800（￥200）'
    )
    assert.equal(
      formatLiandongEffectiveQuota(100_000_000, 1, 500_000, '官方'),
      '≈官方$200（￥200）'
    )
  })

  test('rounds the calculated official quota to an integer', () => {
    assert.equal(
      formatLiandongEffectiveQuota(100_000_000, 0.35, 500_000, '官方'),
      '≈官方$571（￥200）'
    )
  })

  test('rejects zero ratios and invalid quota units', () => {
    assert.equal(calculateLiandongEffectiveQuotaUSD(100, 0, 500_000), null)
    assert.equal(calculateLiandongEffectiveQuotaUSD(100, 1, 0), null)
  })

  test('formats Chinese group ratios with the requested decimal comma', () => {
    assert.equal(
      formatLiandongGroupRatio('vip', 0.25, '倍率', 'zhCN'),
      'vip(倍率0,25)'
    )
  })
})
