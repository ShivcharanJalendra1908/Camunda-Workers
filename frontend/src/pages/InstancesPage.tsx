// pages/InstancesPage.tsx
import { useEffect, useState, useCallback } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { listInstances } from '../api/operateApi'
import { useOperateWS } from '../hooks/useOperateWS'
import type { ProcessInstance, ProcessInstanceState } from '../types/operate'

const PAGE_SIZE = 20

export default function InstancesPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [instances, setInstances]  = useState<ProcessInstance[]>([])
  const [totalCount, setTotalCount] = useState(0)
  const [loading, setLoading]      = useState(true)
  const navigate = useNavigate()

  const bpmnProcessId = searchParams.get('bpmnProcessId') ?? ''
  const state         = (searchParams.get('state') ?? '') as ProcessInstanceState | ''
  const page          = parseInt(searchParams.get('page') ?? '1', 10)

  const load = useCallback(() => {
    setLoading(true)
    listInstances({ bpmnProcessId: bpmnProcessId || undefined, state: state || undefined, page, pageSize: PAGE_SIZE })
      .then((r) => { setInstances(r.items); setTotalCount(r.totalCount) })
      .finally(() => setLoading(false))
  }, [bpmnProcessId, state, page])

  useEffect(() => { load() }, [load])

  // Refresh list when WS signals instance update
  useOperateWS({ INSTANCE_UPDATED: () => load() })

  const totalPages = Math.ceil(totalCount / PAGE_SIZE)

  return (
    <div className="operate-page">
      <div className="operate-page-header">
        <h1>Instances {bpmnProcessId && <span className="operate-subtitle">— {bpmnProcessId}</span>}</h1>
        <span className="operate-badge operate-badge--gray">{totalCount} total</span>
      </div>

      {/* Filters */}
      <div className="operate-filters">
        <select
          value={state}
          onChange={(e) => setSearchParams({ ...Object.fromEntries(searchParams), state: e.target.value, page: '1' })}
        >
          <option value="">All states</option>
          <option value="ACTIVE">Active</option>
          <option value="COMPLETED">Completed</option>
          <option value="CANCELED">Canceled</option>
        </select>
      </div>

      {loading ? (
        <div className="operate-loading">Loading...</div>
      ) : (
        <>
          <div className="operate-table-wrapper">
            <table className="operate-table">
              <thead>
                <tr>
                  <th>Instance Key</th>
                  <th>Process</th>
                  <th>Version</th>
                  <th>State</th>
                  <th>Start Time</th>
                  <th>Incidents</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {instances.map((i) => (
                  <tr key={i.processInstanceKey} onClick={() => navigate(`/instances/${i.processInstanceKey}`)} className="operate-row--clickable">
                    <td><code>{i.processInstanceKey}</code></td>
                    <td>{i.bpmnProcessId}</td>
                    <td>v{i.version}</td>
                    <td><StateTag state={i.state} /></td>
                    <td>{new Date(i.startTime).toLocaleString()}</td>
                    <td>
                      {(i.incidentCount ?? 0) > 0 && (
                        <span className="operate-badge operate-badge--red">{i.incidentCount}</span>
                      )}
                    </td>
                    <td>→</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* Pagination */}
          {totalPages > 1 && (
            <div className="operate-pagination">
              <button disabled={page <= 1} onClick={() => setSearchParams({ ...Object.fromEntries(searchParams), page: String(page - 1) })}>← Prev</button>
              <span>Page {page} of {totalPages}</span>
              <button disabled={page >= totalPages} onClick={() => setSearchParams({ ...Object.fromEntries(searchParams), page: String(page + 1) })}>Next →</button>
            </div>
          )}
        </>
      )}
    </div>
  )
}

function StateTag({ state }: { state: ProcessInstanceState }) {
  const map = { ACTIVE: 'blue', COMPLETED: 'green', CANCELED: 'gray' } as const
  return <span className={`operate-badge operate-badge--${map[state]}`}>{state}</span>
}
