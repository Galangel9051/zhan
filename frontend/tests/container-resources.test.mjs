import test from 'node:test'
import assert from 'node:assert/strict'
import { aggregateResources, formatBytes, formatCPU } from '../src/resource-utils.ts'
import { defaultColumns, normalizeColumns } from '../src/container-columns.ts'

test('resource totals include only active sampled containers and do not add shared memory limits', () => {
 const containers = [{id:'a',state:'running'},{id:'b',state:'running'},{id:'stopped',state:'exited'}]
 const value = {cpu_percent:120,memory_bytes:1024,memory_limit:4096,memory_percent:25,network_rx:10,network_tx:20,block_read:30,block_write:40,pids:2}
 const sample={containers:{a:value,b:value,stopped:value}}
 const totals=aggregateResources(containers,sample)
 assert.equal(totals.cpu_percent,240);assert.equal(totals.memory_bytes,2048);assert.equal(totals.memory_limit,null);assert.equal(totals.pids,4);assert.equal(totals.partial,false)
 assert.equal(aggregateResources(containers,{containers:{a:value}}).partial,true)
 assert.equal(aggregateResources(containers,undefined),null)
 assert.equal(aggregateResources([{id:'stopped',state:'exited'}],sample),null)
 assert.equal(formatBytes(0),'0 B');assert.equal(formatBytes(1024**3),'1 GiB');assert.equal(formatBytes(null),'—');assert.equal(formatCPU(120),'120.00%')
})
test('column preferences recover from invalid data and keep essential columns visible', () => {
 const columns=normalizeColumns([{key:'memory',width:99999,visible:false},{key:'name',width:1,visible:false},{key:'memory',width:150,visible:true},{key:'unknown',width:10,visible:true},{key:'cpu',width:NaN,visible:true},{key:'actions',width:90,visible:false}])
 assert.equal(columns.length,defaultColumns().length);assert.equal(columns[0].key,'name');assert.equal(columns.at(-1).key,'actions')
 assert.equal(columns[0].visible,true);assert.equal(columns.at(-1).visible,true);assert.equal(columns[0].width,200)
 assert.equal(columns.find(column=>column.key==='memory').visible,false);assert.equal(columns.find(column=>column.key==='memory').width,320)
 assert.equal(columns.find(column=>column.key==='cpu').width,100)
 assert.deepEqual(normalizeColumns(null),defaultColumns())
})
