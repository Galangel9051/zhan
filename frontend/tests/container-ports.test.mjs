import test from 'node:test'
import assert from 'node:assert/strict'
import { parsePublishedPorts, portBrowserURL } from '../src/container-ports.ts'

const parse = value => parsePublishedPorts(value)[0]

test('port mappings use compact labels and merge duplicate IPv4/IPv6 publications', () => {
 const ports=parsePublishedPorts('0.0.0.0:8081 → 80/tcp, :::8081 → 80/tcp, [::]:8081 → 80/tcp, 0.0.0.0:5353 → 53/udp')
 assert.equal(ports.length,2);assert.equal(ports[0].label,'8081:80');assert.deepEqual(ports[0].bindings,['0.0.0.0','::','[::]']);assert.equal(ports[0].raw.length,3);assert.equal(ports[1].protocol,'udp')
 assert.deepEqual(parsePublishedPorts(''),[])
 assert.equal(parse('not a published port').label,'not a published port')
 assert.equal(parse('0.0.0.0:99999 → 80/tcp').hostPort,'')
})

test('wildcard publications open on the Docker server hostname rather than client localhost', () => {
 assert.equal(portBrowserURL(parse('0.0.0.0:8081 → 80/tcp'),'server.example.com','running'),'http://server.example.com:8081/')
 assert.equal(portBrowserURL(parse(':::8443 → 443/tcp'),'[2001:db8::1]','running'),'https://[2001:db8::1]:8443/')
 assert.equal(portBrowserURL(parse('192.168.1.10:3000 → 3000/tcp'),'server.example.com','running'),'http://192.168.1.10:3000/')
 assert.equal(portBrowserURL(parse('2001:db8::2:3000 → 3000/tcp'),'server.example.com','running'),'http://[2001:db8::2]:3000/')
})

test('remote loopback bindings, stopped containers and non-TCP ports are not browser links', () => {
 for(const binding of ['127.0.0.1','::1','0:0:0:0:0:0:0:1','::ffff:127.0.0.1']) {
  assert.equal(portBrowserURL(parse(`${binding}:8080 → 80/tcp`),'remote.example.com','running'),null)
  assert.equal(portBrowserURL(parse(`${binding}:8080 → 80/tcp`),'127.example.com','running'),null)
 }
 assert.equal(portBrowserURL(parse('127.0.0.1:8080 → 80/tcp'),'localhost','running'),'http://127.0.0.1:8080/')
 for(const state of ['stopped','paused','exited'])assert.equal(portBrowserURL(parse('0.0.0.0:8080 → 80/tcp'),'server',''+state),null)
 assert.equal(portBrowserURL(parse('0.0.0.0:53 → 53/udp'),'server','running'),null)
 assert.equal(portBrowserURL(parse('0.0.0.0:1000 → 1000/sctp'),'server','running'),null)
 assert.equal(portBrowserURL(parse('0.0.0.0:8080 → 80/tcp'),'bad/host','running'),null)
 assert.equal(portBrowserURL(parse('javascript:alert(1)'),'server','running'),null)
})
