import test from 'node:test'
import assert from 'node:assert/strict'
import { pageNumbers, parsePageJump } from '../src/pagination-utils.ts'

test('page windows always include the current page and both ends without growing with history', () => {
 for (const total of [1, 2, 7, 8, 9, 50, 1000000000]) {
  for (const page of new Set([1, Math.min(4, total), Math.ceil(total / 2), Math.max(1, total - 3), total])) {
   const items = pageNumbers(page, total)
   const numbers = items.filter(item => typeof item === 'number')
   assert(numbers.includes(page))
   assert.equal(numbers[0], 1)
   assert.equal(numbers.at(-1), total)
   assert(numbers.every(item => item >= 1 && item <= total))
   assert.deepEqual([...new Set(numbers)].sort((a, b) => a - b), numbers)
   assert(items.length <= 7)
  }
 }
 assert.deepEqual(pageNumbers(2, 3), [1, 2, 3])
})

test('page jumps accept integer page numbers and reject invalid or out-of-range input', () => {
 for (const [value, expected] of [['1', 1], ['12', 12], [' 03 ', 3], ['50', 50]]) assert.equal(parsePageJump(value, 50), expected)
 for (const value of ['', ' ', '0', '-1', '51', '1.5', '1e1', 'Infinity', 'NaN', '999999999999999999999']) assert.equal(parsePageJump(value, 50), null)
})
