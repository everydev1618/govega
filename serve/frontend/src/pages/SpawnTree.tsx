import { useAPI } from '../hooks/useAPI'
import { api } from '../lib/api'
import type { SpawnTreeNode } from '../lib/types'

export function SpawnTree() {
  const { data: tree, loading, refetch } = useAPI(() => api.getSpawnTree())

  if (loading) return <div className="h-8 w-48 bg-muted rounded animate-pulse" />

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold">Spawn Tree</h2>
        <button onClick={refetch} className="text-sm text-muted-foreground hover:text-foreground px-3 py-1 rounded border border-border">
          Refresh
        </button>
      </div>

      {!tree || tree.length === 0 ? (
        <p className="text-muted-foreground text-sm">No processes in the spawn tree.</p>
      ) : (
        <div className="space-y-2">
          {tree.map(node => (
            <TreeNode key={node.process_id} node={node} />
          ))}
        </div>
      )}
    </div>
  )
}

function TreeNode({ node }: { node: SpawnTreeNode }) {
  const statusColors: Record<string, string> = {
    running: 'border-l-blue-400',
    pending: 'border-l-yellow-400',
    completed: 'border-l-green-400',
    failed: 'border-l-red-400',
  }

  return (
    <div className={`ml-${Math.min(node.spawn_depth * 6, 24)}`} style={{ marginLeft: node.spawn_depth * 24 }}>
      <div className={`p-3 rounded-lg bg-card border border-border border-l-4 ${statusColors[node.status] || 'border-l-muted'}`}>
        <div className="flex items-center justify-between">
          <div>
            <span className="font-mono text-sm">{node.process_id}</span>
            <span className="ml-2 text-sm text-muted-foreground">{node.agent_name}</span>
          </div>
          <span className="text-xs text-muted-foreground">{node.status}</span>
        </div>
        {node.task && <p className="text-xs text-muted-foreground mt-1">{node.task}</p>}
        {node.spawn_reason && <p className="text-xs text-muted-foreground italic">{node.spawn_reason}</p>}
      </div>

      {node.children && node.children.length > 0 && (
        <div className="mt-1 space-y-1">
          {node.children.map(child => (
            <TreeNode key={child.process_id} node={child} />
          ))}
        </div>
      )}
    </div>
  )
}
