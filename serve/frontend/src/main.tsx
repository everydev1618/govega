import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { Layout } from './components/Layout'
import { Overview } from './pages/Overview'
import { ProcessExplorer } from './pages/ProcessExplorer'
import { SpawnTree } from './pages/SpawnTree'
import { EventStream } from './pages/EventStream'
import { AgentRegistry } from './pages/AgentRegistry'
import { MCPServers } from './pages/MCPServers'
import { WorkflowLauncher } from './pages/WorkflowLauncher'
import { CostDashboard } from './pages/CostDashboard'
import { Population } from './pages/Population'
import { Chat } from './pages/Chat'
import { Tasks } from './pages/Tasks'
import { Files } from './pages/Files'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Chat />} />
          <Route path="chat" element={<Chat />} />
          <Route path="chat/:agent" element={<Chat />} />
          <Route path="overview" element={<Overview />} />
          <Route path="processes" element={<ProcessExplorer />} />
          <Route path="tasks" element={<Tasks />} />
          <Route path="spawn-tree" element={<SpawnTree />} />
          <Route path="events" element={<EventStream />} />
          <Route path="population" element={<Population />} />
          <Route path="agents" element={<AgentRegistry />} />
          <Route path="mcp" element={<MCPServers />} />
          <Route path="workflows" element={<WorkflowLauncher />} />
          <Route path="files" element={<Files />} />
          <Route path="costs" element={<CostDashboard />} />
        </Route>
      </Routes>
    </BrowserRouter>
  </React.StrictMode>,
)
