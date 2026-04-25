// App.tsx
import { BrowserRouter, Routes, Route, NavLink } from 'react-router-dom'
import ProcessesPage     from './pages/ProcessesPage'
import InstancesPage     from './pages/InstancesPage'
import InstanceDetailPage from './pages/InstanceDetailPage'
import IncidentsPage     from './pages/IncidentsPage'
import './styles/operate.css'

export default function App() {
  return (
    <BrowserRouter>
      <div className="operate-layout">
        {/* Sidebar */}
        <nav className="operate-sidebar">
          <div className="operate-sidebar-brand">
            <span className="operate-sidebar-logo">⚙</span>
            <span>Operate</span>
          </div>
          <NavLink to="/processes"  className={navClass}>Processes</NavLink>
          <NavLink to="/instances"  className={navClass}>Instances</NavLink>
          <NavLink to="/incidents"  className={navClass}>Incidents</NavLink>
        </nav>

        {/* Main */}
        <main className="operate-main">
          <Routes>
            <Route path="/"                    element={<ProcessesPage />} />
            <Route path="/processes"           element={<ProcessesPage />} />
            <Route path="/instances"           element={<InstancesPage />} />
            <Route path="/instances/:key"      element={<InstanceDetailPage />} />
            <Route path="/incidents"           element={<IncidentsPage />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}

const navClass = ({ isActive }: { isActive: boolean }) =>
  `operate-nav-link ${isActive ? 'operate-nav-link--active' : ''}`
