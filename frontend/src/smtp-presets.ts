export type SMTPProvider = 'qq' | 'gmail' | 'custom'
export type SMTPConnection = { host: string; port: number; tls_mode: string }
export const smtpPresets = {
  qq: { host: 'smtp.qq.com', port: 465, tls_mode: 'tls' },
  gmail: { host: 'smtp.gmail.com', port: 587, tls_mode: 'starttls' },
} satisfies Record<Exclude<SMTPProvider, 'custom'>, SMTPConnection>

export function detectSMTPProvider(connection: SMTPConnection): SMTPProvider {
  const encrypted = (connection.port === 465 && connection.tls_mode === 'tls') || (connection.port === 587 && connection.tls_mode === 'starttls')
  if (!encrypted) return 'custom'
  if (connection.host.toLowerCase() === 'smtp.qq.com') return 'qq'
  if (connection.host.toLowerCase() === 'smtp.gmail.com') return 'gmail'
  return 'custom'
}

export function sameSMTPConnection(a: SMTPConnection, b: SMTPConnection) {
  return a.host === b.host && a.port === b.port && a.tls_mode === b.tls_mode
}
