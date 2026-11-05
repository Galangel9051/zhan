export function runtimeAction(status: string): 'start' | 'stop' | null {
 if (['running', 'partial', 'unhealthy', 'healthy', 'restarting', 'starting', 'paused'].includes(status)) return 'stop'
 if (['stopped', 'exited', 'created', 'dead'].includes(status)) return 'start'
 return null
}
