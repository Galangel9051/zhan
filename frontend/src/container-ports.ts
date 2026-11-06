export type PublishedPort = {
  label: string
  hostPort: string
  containerPort: string
  protocol: string
  bindings: string[]
  raw: string[]
}

export function parsePublishedPorts(value: string): PublishedPort[] {
  const result: PublishedPort[] = []
  for (const raw of value.split(',').map(part => part.trim()).filter(Boolean)) {
    const match = /^(.*):(\d+)\s*→\s*(\d+)\/(tcp|udp|sctp)$/i.exec(raw)
    if (!match || [match[2], match[3]].some(port => Number(port) < 1 || Number(port) > 65535)) {
      result.push({ label: raw, hostPort: '', containerPort: '', protocol: '', bindings: [], raw: [raw] })
      continue
    }
    const [, binding, hostPort, containerPort, transport] = match
    const protocol = transport.toLowerCase()
    const previous = result.find(port => port.hostPort === hostPort && port.containerPort === containerPort && port.protocol === protocol)
    if (previous) {
      if (!previous.bindings.includes(binding)) previous.bindings.push(binding)
      previous.raw.push(raw)
    } else {
      result.push({ label: `${hostPort}:${containerPort}`, hostPort, containerPort, protocol, bindings: [binding], raw: [raw] })
    }
  }
  return result
}

function normalizeHost(host: string): string {
  const value = host.replace(/^\[|\]$/g, '').toLowerCase()
  if (!value || !/^[a-z0-9.:-]+$/i.test(value)) return value
  try {
    return new URL(`http://${value.includes(':') ? `[${value}]` : value}/`).hostname.replace(/^\[|\]$/g, '')
  } catch {
    return value
  }
}

function isLoopback(host: string): boolean {
  host = normalizeHost(host)
  return host === 'localhost' || host === '::1' || /^127(?:\.\d{1,3}){3}$/.test(host) || /^::ffff:7f[0-9a-f]{2}:/.test(host)
}

export function portBrowserURL(port: PublishedPort, pageHost: string, state: string): string | null {
  if (port.protocol !== 'tcp' || state !== 'running') return null
  // A loopback binding belongs to the Docker server, not the viewer's computer.
  const binding = port.bindings.find(host => !isLoopback(host)) ?? port.bindings[0]
  if (binding == null || (isLoopback(binding) && !isLoopback(pageHost))) return null
  let host = normalizeHost(binding)
  if (host === '' || host === '0.0.0.0' || host === '::') host = normalizeHost(pageHost)
  if (!host || !/^[a-z0-9.:-]+$/i.test(host)) return null
  const scheme = port.containerPort === '443' || port.hostPort === '443' ? 'https' : 'http'
  try {
    return new URL(`${scheme}://${host.includes(':') ? `[${host}]` : host}:${port.hostPort}/`).href
  } catch {
    return null
  }
}
