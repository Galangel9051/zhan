import { useState } from 'react'
import { ExternalLink, ChevronDown } from 'lucide-react'
import { Collapse } from './motion'
import { parsePublishedPorts, portBrowserURL, type PublishedPort } from './container-ports'

type Props = { ports: string; state: string }

export default function ContainerPorts({ ports, state }: Props) {
  const [expanded, setExpanded] = useState(false)
  const mappings = parsePublishedPorts(ports)
  if (!mappings.length) return <span className="port-empty">—</span>
  function renderPort(port: PublishedPort, index: number) {
      const href = portBrowserURL(port, window.location.hostname, state)
      const mapping = port.raw.join('\n')
      const unavailable = port.protocol !== 'tcp' ? '' : state !== 'running' ? '\n容器未运行' : !href ? '\n仅绑定服务器回环地址，需在服务器本机访问' : ''
      return <li key={`${port.label}:${port.protocol}:${index}`}>
        {href ? <a className="port-link" href={href} target="_blank" rel="noopener noreferrer" title={`${mapping}\n尝试在浏览器打开 ${href}`} aria-label={`在浏览器打开端口 ${port.label}`}><span>{port.label}</span><ExternalLink size={12} /></a> : <span className="port-mapping" title={mapping + unavailable}>{port.label}</span>}
        {port.protocol && port.protocol !== 'tcp' && <span className="port-protocol">{port.protocol.toUpperCase()}</span>}
      </li>
  }
  return <div className="container-ports">
    <ul className="port-mappings" aria-label="发布端口">{mappings.slice(0, 2).map(renderPort)}</ul>
    {mappings.length > 2 && <Collapse open={expanded}><ul className="port-mappings port-mappings-extra" aria-label="更多发布端口">{mappings.slice(2).map(renderPort)}</ul></Collapse>}
    {mappings.length > 2 && <button type="button" className="port-more" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}><ChevronDown size={11} className={expanded ? 'is-open' : ''} />{expanded ? '收起端口' : `还有 ${mappings.length - 2} 个端口`}</button>}
  </div>
}
