import type { ToolCallState } from '../../lib/types'

export function statusEmoji(tc: ToolCallState): string {
  return tc.status === 'running' ? '⏳'
    : tc.status === 'error' ? '❌' : '✅'
}

export function shortToolName(name: string): string {
  const idx = name.indexOf('__')
  return idx >= 0 ? name.slice(idx + 2) : name
}

const NODE_COLORS = ['#60a5fa', '#a78bfa', '#34d399', '#fbbf24']

function toolNarrative(name: string, args: Record<string, unknown>): string {
  switch (name) {
    case 'send_to_agent': return `Traveling to ${args.agent || 'an agent'}...`
    case 'delegate': return `Delegating to ${args.agent || 'an agent'}...`
    case 'list_agents': return 'Surveying the universe...'
    case 'remember': return 'Committing to memory...'
    case 'recall': return 'Searching memories...'
    case 'forget': return 'Letting go...'
    case 'set_project': return `Opening ${args.name || 'project'} workspace...`
    case 'list_projects': return 'Checking project workspaces...'
    case 'write_file': return 'Writing a file...'
    case 'read_file': return 'Reading a file...'
    case 'list_files': return 'Browsing files...'
    case 'exec': return 'Running a command...'
    default: return `Using ${name}...`
  }
}

export function ActivityConstellation({ tools }: { tools: ToolCallState[] }) {
  const cx = 20, cy = 20
  const orbitR = 12

  return (
    <svg width="40" height="40" viewBox="0 0 40 40" className="block">
      <circle cx={cx} cy={cy} r={5} fill="#a78bfa" opacity={0.12} className="constellation-core" />
      <circle cx={cx} cy={cy} r={3} fill="#a78bfa" opacity={0.7} />

      {tools.map((tc, i) => {
        const color = NODE_COLORS[i % NODE_COLORS.length]
        const startAngle = (360 * i) / Math.max(tools.length, 1)
        const dur = 3 + i * 0.7

        return (
          <g key={tc.id}>
            <animateTransform
              attributeName="transform" type="rotate"
              from={`${startAngle} ${cx} ${cy}`}
              to={`${startAngle + 360} ${cx} ${cy}`}
              dur={`${dur}s`}
              repeatCount="indefinite"
            />
            <circle
              cx={cx + orbitR} cy={cy}
              r={2.5} fill={color} opacity={0.8}
            />
          </g>
        )
      })}
    </svg>
  )
}

export function ActivityNarrative({ tools }: { tools: ToolCallState[] }) {
  const runningNested = [...tools].reverse().find(t => t.status === 'running' && t.nested_agent)
  const running = [...tools].reverse().find(t => t.status === 'running')
  const target = runningNested || running

  let text: string
  if (target?.nested_agent) {
    const agent = target.nested_agent.split('/').pop() || target.nested_agent
    text = `${agent}: ${toolNarrative(target.name, target.arguments || {})}`
  } else if (target) {
    text = toolNarrative(target.name, target.arguments || {})
  } else {
    text = 'Thinking...'
  }

  return (
    <span className="text-xs text-muted-foreground italic ml-1">
      {text}
    </span>
  )
}

export function ToolCallBadges({
  toolCalls,
  streaming,
  onToggle,
}: {
  toolCalls: ToolCallState[]
  streaming?: boolean
  onToggle: (tcIdx: number) => void
}) {
  return (
    <div className="my-1.5">
      <div className="flex flex-row flex-wrap gap-1.5">
        {toolCalls.map((tc, j) => (
          <button key={tc.id} onClick={() => onToggle(j)}
            className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-mono
              border transition-colors ${!tc.collapsed
                ? 'border-indigo-500/50 bg-indigo-500/10 text-foreground'
                : 'border-border bg-background/50 text-muted-foreground hover:text-foreground hover:border-muted-foreground/30'
              }`}>
            <span className={tc.status === 'running' ? 'animate-pulse' : ''}>{statusEmoji(tc)}</span>
            <span>{shortToolName(tc.name)}</span>
            {tc.duration_ms != null && <span className="text-muted-foreground">{tc.duration_ms}ms</span>}
          </button>
        ))}
      </div>
      {streaming && toolCalls.length > 0 && (
        <div className="flex items-center gap-1 pt-1.5 constellation-activity">
          <ActivityConstellation tools={toolCalls} />
          <ActivityNarrative tools={toolCalls} />
        </div>
      )}
      {toolCalls.map((tc) => !tc.collapsed && (
        <div key={tc.id} className="mt-2 rounded-lg border border-border bg-background/50 px-3 py-2 space-y-1.5 text-sm">
          {tc.arguments && Object.keys(tc.arguments).length > 0 && (
            <div>
              <span className="text-xs text-muted-foreground">args</span>
              <pre className="mt-0.5 text-xs font-mono bg-background rounded p-2 overflow-x-auto border border-border whitespace-pre-wrap">
                {JSON.stringify(tc.arguments, null, 2)}
              </pre>
            </div>
          )}
          {tc.result != null && (
            <div>
              <span className="text-xs text-muted-foreground">result</span>
              <pre className="mt-0.5 text-xs font-mono bg-background rounded p-2 overflow-x-auto border border-border whitespace-pre-wrap max-h-60">
                {tc.result}
              </pre>
            </div>
          )}
        </div>
      ))}
    </div>
  )
}
