import test from 'node:test'
import assert from 'node:assert/strict'
import { mergeModels, newModel, updateModels } from '../src/ai-models.ts'

test('refreshing models preserves selections and administrator limits without enabling new models', () => {
 const configured = { ...newModel('existing'), enabled: true, context_window: 64000, max_output_tokens: 2000, temperature: 0 }
 const models = mergeModels([configured], [{ id: 'existing', context_window: 128000 }, { id: 'new', context_window: 8192 }])
 assert.deepEqual(models[0], configured)
 assert.equal(models[1].enabled, false)
 assert.equal(models[1].context_window, 8192)
 assert.ok(models[1].max_output_tokens < models[1].context_window)
 assert.equal(mergeModels(models, [{ id: 'new' }]).length, 2)
})
test('unchecking the default picks another enabled model and clearing all clears the default', () => {
 const a = { ...newModel('a'), enabled: true }, b = { ...newModel('b'), enabled: true }
 const form = { model: 'a', models: [a, b] }
 assert.equal(updateModels(form, [{ ...a, enabled: false }, b]).model, 'b')
 assert.equal(updateModels(form, [{ ...a, enabled: false }, { ...b, enabled: false }]).model, '')
})
