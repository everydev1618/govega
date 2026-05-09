import { useState, useEffect, useCallback } from 'react'
import { api } from '../lib/api'
import { Modal } from '../components/Modal'
import type { Task, TaskDetail, TaskStatus, TaskPriority, AgentResponse } from '../lib/types'

const COLUMNS: { key: TaskStatus; label: string; bg: string; badge: string }[] = [
  { key: 'todo',    label: 'Todo',        bg: 'bg-zinc-900/40 border-zinc-800',         badge: 'bg-zinc-800 text-zinc-300' },
  { key: 'doing',   label: 'Doing',       bg: 'bg-blue-900/10 border-blue-900/20',      badge: 'bg-blue-900/50 text-blue-300' },
  { key: 'blocked', label: 'Blocked',     bg: 'bg-amber-900/10 border-amber-900/20',    badge: 'bg-amber-900/50 text-amber-300' },
  { key: 'done',    label: 'Done',        bg: 'bg-green-900/10 border-green-900/20',    badge: 'bg-green-900/50 text-green-300' },
]

const PRIORITY_DOT: Record<TaskPriority, string> = {
  urgent: 'bg-red-500',
  high:   'bg-orange-500',
  normal: 'bg-zinc-500',
  low:    'bg-zinc-700',
}

export function Tasks() {
  const [tasks, setTasks] = useState<Task[]>([])
  const [loading, setLoading] = useState(true)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [detail, setDetail] = useState<TaskDetail | null>(null)
  const [showCreate, setShowCreate] = useState(false)

  const loadTasks = useCallback(async () => {
    try {
      // Hide canceled by default — humans rarely revisit them. Add a filter
      // toggle later if it turns out to matter.
      const all = await api.listTasks({ status: 'todo,doing,blocked,done' })
      setTasks(all)
    } catch (e) {
      console.error('listTasks failed', e)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { loadTasks() }, [loadTasks])

  const openDetail = async (id: string) => {
    setSelectedId(id)
    try {
      setDetail(await api.getTask(id))
    } catch (e) {
      console.error('getTask failed', e)
    }
  }

  const moveTask = async (id: string, status: TaskStatus) => {
    try {
      await api.updateTask(id, { status })
      await loadTasks()
      if (selectedId === id) setDetail(await api.getTask(id))
    } catch (e) {
      console.error('updateTask failed', e)
    }
  }

  const grouped: Record<TaskStatus, Task[]> = { todo: [], doing: [], blocked: [], done: [], canceled: [] }
  for (const t of tasks) {
    if (grouped[t.status]) grouped[t.status].push(t)
  }

  if (loading) return <div className="h-8 w-48 bg-muted rounded animate-pulse" />

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-bold">Tasks</h2>
        <button
          onClick={() => setShowCreate(true)}
          className="px-3 py-1.5 rounded bg-primary text-primary-foreground text-sm hover:opacity-90"
        >
          New task
        </button>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
        {COLUMNS.map(col => (
          <div key={col.key} className={`min-w-0 rounded-lg border p-3 flex flex-col ${col.bg}`}>
            <div className="flex items-center justify-between mb-3">
              <span className="text-sm font-semibold">{col.label}</span>
              <span className={`text-xs px-2 py-0.5 rounded-full font-mono ${col.badge}`}>
                {grouped[col.key].length}
              </span>
            </div>
            <div className="flex-1 overflow-y-auto space-y-2 min-h-0">
              {grouped[col.key].length === 0 && (
                <p className="text-xs text-muted-foreground/50 text-center py-8">Empty</p>
              )}
              {grouped[col.key].map(t => (
                <TaskCard
                  key={t.id}
                  task={t}
                  selected={selectedId === t.id}
                  onSelect={() => openDetail(t.id)}
                  onMove={status => moveTask(t.id, status)}
                />
              ))}
            </div>
          </div>
        ))}
      </div>

      <Modal
        open={!!(selectedId && detail)}
        onClose={() => { setSelectedId(null); setDetail(null) }}
        title={detail?.title}
      >
        {detail && (
          <TaskDetailBody
            detail={detail}
            onUpdate={async () => { await loadTasks(); setDetail(await api.getTask(selectedId!)) }}
            onDelete={async () => {
              await api.deleteTask(selectedId!)
              setSelectedId(null); setDetail(null)
              await loadTasks()
            }}
          />
        )}
      </Modal>

      {showCreate && (
        <CreateTaskModal
          onClose={() => setShowCreate(false)}
          onCreated={async () => { setShowCreate(false); await loadTasks() }}
        />
      )}
    </div>
  )
}

function TaskCard({ task, selected, onSelect, onMove }: {
  task: Task
  selected: boolean
  onSelect: () => void
  onMove: (s: TaskStatus) => void
}) {
  return (
    <div
      onClick={onSelect}
      className={`p-3 rounded-lg border cursor-pointer transition-colors ${
        selected ? 'border-primary bg-accent' : 'border-border bg-card hover:border-primary/50'
      }`}
    >
      <div className="flex items-start gap-2 mb-1">
        <span className={`mt-1.5 w-2 h-2 rounded-full shrink-0 ${PRIORITY_DOT[task.priority] || PRIORITY_DOT.normal}`} title={`priority: ${task.priority}`} />
        <span className="font-semibold text-sm leading-tight">{task.title}</span>
      </div>
      {task.description && (
        <p className="text-xs text-muted-foreground line-clamp-2 mb-2">{task.description}</p>
      )}
      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span>{task.assignee || 'unassigned'}</span>
        {task.due_at && <span>due {new Date(task.due_at).toLocaleDateString()}</span>}
      </div>
      <div className="mt-2 flex gap-1 flex-wrap" onClick={e => e.stopPropagation()}>
        {(['todo', 'doing', 'blocked', 'done'] as TaskStatus[])
          .filter(s => s !== task.status)
          .map(s => (
            <button
              key={s}
              onClick={() => onMove(s)}
              className="text-[10px] px-1.5 py-0.5 rounded bg-muted hover:bg-accent"
              title={`Move to ${s}`}
            >
              → {s}
            </button>
          ))}
      </div>
    </div>
  )
}

// TaskDetailBody is the body of the task detail modal. The Modal component
// owns the chrome (title, close button, overlay) — this just renders the
// inner content. Same shape as before, minus the side-panel wrapper.
function TaskDetailBody({ detail, onUpdate, onDelete }: {
  detail: TaskDetail
  onUpdate: () => Promise<void>
  onDelete: () => Promise<void>
}) {
  const [comment, setComment] = useState('')

  const submitComment = async () => {
    const c = comment.trim()
    if (!c) return
    await api.addTaskComment(detail.id, c)
    setComment('')
    await onUpdate()
  }

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-2 text-sm">
        <div className="text-muted-foreground">Status</div><div>{detail.status}</div>
        <div className="text-muted-foreground">Priority</div><div>{detail.priority}</div>
        <div className="text-muted-foreground">Assignee</div><div>{detail.assignee || '—'}</div>
        <div className="text-muted-foreground">Created by</div><div>{detail.created_by || '—'}</div>
        <div className="text-muted-foreground">Updated</div><div className="text-xs">{new Date(detail.updated_at).toLocaleString()}</div>
      </div>

      {detail.description && (
        <div>
          <h4 className="text-xs font-semibold uppercase text-muted-foreground mb-1">Description</h4>
          <p className="text-sm whitespace-pre-wrap">{detail.description}</p>
        </div>
      )}

      {detail.processes.length > 0 && (
        <div>
          <h4 className="text-xs font-semibold uppercase text-muted-foreground mb-1">
            Linked processes ({detail.processes.length})
          </h4>
          <div className="space-y-1">
            {detail.processes.map(pid => (
              <div key={pid} className="text-xs font-mono text-muted-foreground">{pid}</div>
            ))}
          </div>
        </div>
      )}

      <div>
        <h4 className="text-xs font-semibold uppercase text-muted-foreground mb-1">
          Comments ({detail.comments.length})
        </h4>
        <div className="space-y-2 mb-2">
          {detail.comments.map(c => (
            <div key={c.id} className="p-2 rounded bg-muted text-xs">
              <div className="flex justify-between text-muted-foreground mb-0.5">
                <span className="font-semibold">{c.author || 'user'}</span>
                <span>{new Date(c.created_at).toLocaleString()}</span>
              </div>
              <p className="whitespace-pre-wrap">{c.content}</p>
            </div>
          ))}
        </div>
        <div className="flex gap-1">
          <input
            value={comment}
            onChange={e => setComment(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && submitComment()}
            placeholder="Add a comment…"
            className="flex-1 px-2 py-1 text-xs rounded border border-border bg-background"
          />
          <button onClick={submitComment} className="text-xs px-2 py-1 rounded bg-primary text-primary-foreground">
            Post
          </button>
        </div>
      </div>

      <button
        onClick={onDelete}
        className="w-full py-1.5 rounded bg-destructive text-white text-sm hover:bg-destructive/80"
      >
        Delete task
      </button>
    </div>
  )
}

function CreateTaskModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => Promise<void> }) {
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [priority, setPriority] = useState<TaskPriority>('normal')
  const [assignee, setAssignee] = useState('')
  const [agents, setAgents] = useState<AgentResponse[]>([])

  useEffect(() => {
    api.getAgents().then(setAgents).catch(e => console.error('getAgents', e))
  }, [])

  const submit = async () => {
    if (!title.trim()) return
    await api.createTask({ title: title.trim(), description, priority, assignee })
    await onCreated()
  }

  return (
    <Modal open={true} onClose={onClose} title="New task" widthClass="w-[480px]">
      <div className="space-y-3">
        <input
          autoFocus
          value={title}
          onChange={e => setTitle(e.target.value)}
          placeholder="Title"
          className="w-full px-3 py-2 rounded border border-border bg-background"
        />
        <textarea
          value={description}
          onChange={e => setDescription(e.target.value)}
          placeholder="Description (optional)"
          rows={4}
          className="w-full px-3 py-2 rounded border border-border bg-background text-sm"
        />
        <div className="grid grid-cols-2 gap-2">
          <select
            value={priority}
            onChange={e => setPriority(e.target.value as TaskPriority)}
            className="px-3 py-2 rounded border border-border bg-background text-sm"
          >
            <option value="low">low</option>
            <option value="normal">normal</option>
            <option value="high">high</option>
            <option value="urgent">urgent</option>
          </select>
          <select
            value={assignee}
            onChange={e => setAssignee(e.target.value)}
            className="px-3 py-2 rounded border border-border bg-background text-sm"
            title="Assignee"
          >
            <option value="">(orchestrator picks)</option>
            {agents.map(a => (
              <option key={a.name} value={a.name}>
                {a.display_name ? `${a.display_name} (${a.name})` : a.name}
              </option>
            ))}
          </select>
        </div>
        <div className="flex justify-end gap-2 pt-2">
          <button onClick={onClose} className="px-3 py-1.5 rounded text-sm hover:bg-accent">Cancel</button>
          <button onClick={submit} className="px-3 py-1.5 rounded bg-primary text-primary-foreground text-sm">
            Create
          </button>
        </div>
      </div>
    </Modal>
  )
}
