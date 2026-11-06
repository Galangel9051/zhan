import test from 'node:test'
import assert from 'node:assert/strict'
import { detectSMTPProvider, sameSMTPConnection, smtpPresets } from '../src/smtp-presets.ts'

test('provider recognition supports both encrypted ports and preserves nonstandard configurations as custom', () => {
 for(const [provider,preset] of Object.entries(smtpPresets)) {
  assert.equal(detectSMTPProvider(preset),provider)
  assert.equal(detectSMTPProvider({...preset,port:465,tls_mode:'tls'}),provider)
  assert.equal(detectSMTPProvider({...preset,port:587,tls_mode:'starttls'}),provider)
  assert.equal(detectSMTPProvider({...preset,tls_mode:'none'}),'custom')
  assert.equal(detectSMTPProvider({...preset,port:2525}),'custom')
 }
 assert.equal(detectSMTPProvider({host:'mail.example.com',port:587,tls_mode:'starttls'}),'custom')
 assert.equal(sameSMTPConnection(smtpPresets.qq,smtpPresets.gmail),false)
 assert.equal(sameSMTPConnection(smtpPresets.qq,{...smtpPresets.qq}),true)
 assert.equal(sameSMTPConnection(smtpPresets.qq,{...smtpPresets.qq,port:587}),false)
})
