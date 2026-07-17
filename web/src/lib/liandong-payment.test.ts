import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  parsePaymentAmountMinor,
  paymentAmountInputFromMinor,
} from './liandong-amount.ts'

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
