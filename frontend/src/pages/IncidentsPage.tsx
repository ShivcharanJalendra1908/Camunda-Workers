// pages/IncidentsPage.tsx
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { listIncidents, resolveIncident } from '../api/operateApi'
import { useOperateWS } from '../hooks/useOperateWS'
import type { Incident } from '../types/operate'

export default function IncidentsPage() {
  const [incidents, setIncidents] = useState<Incident[]>([])
  const [totalCount, setTotalCount] = useState(0)
  const [loading, setLoading] = useState(true)
  const [msg, setMsg] = useState<string | null>(null)
  const navigate = useNavigate()

  const load = () => {
    setLoading(true)
    listIncidents()
      .then((r) => { setIncidents(r.items); setTotalCount(r.totalCount) })
      .finally(() => setLoading(false))
  }

  useEffect(() => { load() }, [])

  useOperateWS({
    INCIDENT_CREATED:  () => load(),
    INCIDENT_RESOLVED: () => load(),
  })

  const handleResolve = async (incidentKey: number) => {
    await resolveIncident(incidentKey)
    setIncidents((prev) => prev.filter((i) => i.incidentKey !== incidentKey))
    setMsg('Incident resolved.')
  }

  return (
    <div className="operate-page">
      <div className="operate-page-header">
        <h1>Incidents</h1>
        <span className="operate-badge operate-badge--red">{totalCount} active</span>
      </div>

      {msg && <div className="operate-toast" onClick={() => setMsg(null)}>{msg} ✕</div>}

      {loading ? (
        <div className="operate-loading">Loading incidents...</div>
      ) : incidents.length === 0 ? (
        <div className="operate-empty operate-empty--large">
          <span>✓</span>
          <p>No active incidents</p>
        </div>
      ) : (
        <div className="operate-table-wrapper">
          <table className="operate-table">
            <thead>
              <tr>
                <th>Process</th>
                <th>Instance</th>
                <th>Element</th>
                <th>Error Type</th>
                <th>Message</th>
                <th>Created</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {incidents.map((inc) => (
                <tr key={inc.incidentKey}>
                  <td>{inc.bpmnProcessId}</td>
                  <td>
                    <button
                      className="operate-link"
                      onClick={() => navigate(`/instances/${inc.processInstanceKey}`)}
                    >
                      {inc.processInstanceKey}
                    </button>
                  </td>
                  <td><code>{inc.elementId}</code></td>
                  <td>
                    <span className="operate-badge operate-badge--red">{inc.errorType}</span>
                  </td>
                  <td className="operate-error-msg">{inc.errorMessage}</td>
                  <td>{new Date(inc.createdAt).toLocaleString()}</td>
                  <td>
                    <button
                      className="operate-btn operate-btn--sm operate-btn--success"
                      onClick={() => handleResolve(inc.incidentKey)}
                    >
                      Resolve
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
