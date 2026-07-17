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

export function parsePaymentAmountMinor(value: string): number | null {
  const normalized = value.trim()
  if (!/^(?:0|[1-9]\d*)(?:\.\d{1,2})?$/.test(normalized)) return null

  const [whole, fraction = ''] = normalized.split('.')
  const minor =
    Number(whole) * 100 + Number.parseInt(fraction.padEnd(2, '0') || '0', 10)
  return Number.isSafeInteger(minor) && minor > 0 ? minor : null
}

export function paymentAmountInputFromMinor(amountMinor: number): string {
  if (!Number.isSafeInteger(amountMinor) || amountMinor <= 0) return ''

  const whole = Math.floor(amountMinor / 100)
  const fraction = amountMinor % 100
  if (fraction === 0) return String(whole)
  if (fraction % 10 === 0) return `${whole}.${fraction / 10}`
  return `${whole}.${String(fraction).padStart(2, '0')}`
}
