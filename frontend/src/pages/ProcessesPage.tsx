// pages/ProcessesPage.tsx
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { listProcesses } from '../api/operateApi'
import type { DeployedProcess } from '../types/operate'

export default function ProcessesPage() {
  const [processes, setProcesses] = useState<DeployedProcess[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const navigate = useNavigate()

  useEffect(() => {
    listProcesses()
      .then((r) => setProcesses(r.items))
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  if (loading) return <div className="operate-loading">Loading processes...</div>
  if (error)   return <div className="operate-error">Error: {error}</div>

  return (
    <div className="operate-page">
      <div className="operate-page-header">
        <h1>Processes</h1>
        <span className="operate-badge operate-badge--gray">{processes.length} deployed</span>
      </div>

      <div className="operate-table-wrapper">
        <table className="operate-table">
          <thead>
            <tr>
              <th>Process ID</th>
              <th>Version</th>
              <th>Active</th>
              <th>Incidents</th>
              <th>Completed</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {processes.map((p) => (
              <tr key={p.processDefinitionKey}>
                <td>
                  <span className="operate-process-id">{p.bpmnProcessId}</span>
                  <span className="operate-resource-name">{p.resourceName}</span>
                </td>
                <td>v{p.version}</td>
                <td>
                  <span className="operate-badge operate-badge--blue">{p.activeCount}</span>
                </td>
                <td>
                  {p.incidentCount > 0 ? (
                    <span className="operate-badge operate-badge--red">{p.incidentCount}</span>
                  ) : (
                    <span className="operate-badge operate-badge--gray">0</span>
                  )}
                </td>
                <td>{p.completedCount}</td>
                <td>
                  <button
                    className="operate-btn operate-btn--sm"
                    onClick={() =>
                      navigate(`/instances?bpmnProcessId=${p.bpmnProcessId}`)
                    }
                  >
                    View instances →
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
