import test from 'node:test'
import assert from 'node:assert/strict'
import { errorText, APIError } from '../src/api.ts'

test('API errors retain their actual reason after storing the message in React state', () => {
 const reason = '项目路径校验失败：路径不在允许管理的目录中。请修改 allowed_roots 后重启服务'
 const state = errorText(new APIError(400, reason))
 assert.equal(errorText(state), reason)
})

test('string validation errors and ordinary Error messages remain visible', () => {
 for (const reason of ['路径不存在：/opt/apps/compose.yaml', '工作区当前分支与绑定分支不一致', 'Compose 项目已被导入']) {
  assert.equal(errorText(reason), reason)
  assert.equal(errorText(new Error(reason)), reason)
 }
})

test('unrecognized and empty errors still have a useful fallback', () => {
 for (const value of [null, undefined, {}, '', '  ', new Error('')]) assert.equal(errorText(value), '操作失败，请稍后重试')
})
