import test from 'node:test'
import assert from 'node:assert/strict'
import { layoutGitGraph } from '../src/git-graph.ts'

test('graph preserves merge parents and joins lanes at their common ancestor', () => {
 const {rows,width}=layoutGitGraph([{hash:'merge',parents:['main','feature']},{hash:'main',parents:['base']},{hash:'feature',parents:['base']},{hash:'base',parents:[]}])
 assert.equal(rows[0].incoming,false)
 assert.deepEqual(rows[0].edges.map(e=>e.parent),['main','feature'])
 assert.equal(rows[1].incoming,true)
 assert.equal(rows[2].lane,1)
 assert.deepEqual(rows[2].edges.find(e=>e.parent==='base'&&e.from===1),{from:1,to:0,parent:'base'})
 assert.equal(rows[3].lane,0);assert.deepEqual(rows[3].edges,[]);assert.equal(width,52)
})
test('graph starts distinct tips without inventing ancestry and keeps cutoff edges', () => {
 const {rows}=layoutGitGraph([{hash:'local',parents:['a']},{hash:'remote',parents:['b']},{hash:'a',parents:['base']}])
 assert.equal(rows[1].incoming,false)
 assert(!rows[0].edges.some(e=>e.parent==='remote'))
 assert(rows[2].edges.some(e=>e.parent==='base'))
 assert.equal(layoutGitGraph([]).rows.length,0)
})
