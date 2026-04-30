// frontend/src/components/BpmnViewer.tsx
// Renders BPMN diagram with token overlay using bpmn-js
// Install: npm install bpmn-js

import { useEffect, useRef, useState } from 'react'

interface ElementInstance {
    elementId: string
    elementType: string
    state: 'ACTIVE' | 'COMPLETED' | 'TERMINATED' | 'INCIDENT'
    instanceKey: number
}

interface Props {
    processDefinitionKey: number
    processInstanceKey?: number  // if provided, shows token overlay
    apiBase?: string
}

// State colors matching Camunda Operate
const STATE_COLORS = {
    ACTIVE: { fill: '#a9d4f5', stroke: '#1b85cc' },  // blue
    COMPLETED: { fill: '#c8e6c9', stroke: '#388e3c' },  // green
    TERMINATED: { fill: '#e0e0e0', stroke: '#9e9e9e' },  // gray
    INCIDENT: { fill: '#ffcdd2', stroke: '#c62828' },  // red
}

export default function BpmnViewer({ processDefinitionKey, processInstanceKey, apiBase = '' }: Props) {
    const containerRef = useRef<HTMLDivElement>(null)
    const viewerRef = useRef<any>(null)
    const [loading, setLoading] = useState(true)
    const [error, setError] = useState<string | null>(null)
    const [elements, setElements] = useState<ElementInstance[]>([])

    // Load BPMN XML and render
    useEffect(() => {
        if (!containerRef.current) return

        let cancelled = false

        const init = async () => {
            try {
                setLoading(true)
                setError(null)

                // Dynamically import bpmn-js (avoid SSR issues)
                const BpmnJS = (await import('bpmn-js')).default

                // Fetch BPMN XML from our backend
                const xmlRes = await fetch(`${apiBase}/operate/processes/${processDefinitionKey}/xml`, {
                    credentials: 'include',
                })
                if (!xmlRes.ok) throw new Error('Failed to fetch BPMN XML')
                const xml = await xmlRes.text()

                if (cancelled) return

                // Destroy previous viewer
                if (viewerRef.current) {
                    viewerRef.current.destroy()
                }

                // Create new viewer
                const viewer = new BpmnJS({ container: containerRef.current as HTMLElement })
                viewerRef.current = viewer

                // Import XML
                await viewer.importXML(xml)

                // Fit to canvas
                const canvas = viewer.get('canvas') as any
                canvas.zoom('fit-viewport', 'auto')

                setLoading(false)

                // Load element instances if instanceKey provided
                if (processInstanceKey) {
                    const elemRes = await fetch(
                        `${apiBase}/operate/instances/${processInstanceKey}/element-instances`,
                        { credentials: 'include' }
                    )
                    if (elemRes.ok) {
                        const data = await elemRes.json()
                        if (!cancelled) {
                            setElements(data.items || [])
                        }
                    }
                }
            } catch (e: any) {
                if (!cancelled) {
                    setError(e.message)
                    setLoading(false)
                }
            }
        }

        init()
        return () => { cancelled = true }
    }, [processDefinitionKey, apiBase])

    // Apply token overlay when elements change
    useEffect(() => {
        if (!viewerRef.current || elements.length === 0) return

        try {
            const elementRegistry = viewerRef.current.get('elementRegistry')
            const canvas = viewerRef.current.get('canvas')
            const overlays = viewerRef.current.get('overlays')

            // Clear old overlays
            overlays.clear()

            elements.forEach(({ elementId, state }) => {
                const element = elementRegistry.get(elementId)
                if (!element) return

                // 1. Colorizing elements with Canvas markers
                canvas.addMarker(elementId, `state-${state}`)

                // 2. Add Token Badges for ACTIVE or INCIDENT
                if (state === 'ACTIVE' || state === 'INCIDENT') {
                    const isIncident = state === 'INCIDENT'
                    overlays.add(elementId, {
                        position: { top: -18, right: -8 },
                        html: `
              <div style="
                background: ${isIncident ? '#c62828' : '#1b85cc'};
                color: white;
                border-radius: 50%;
                width: 20px;
                height: 20px;
                display: flex;
                align-items: center;
                justify-content: center;
                font-size: 11px;
                font-weight: bold;
                box-shadow: 0 1px 4px rgba(0,0,0,0.3);
              ">
                ${isIncident ? '!' : '1'}
              </div>
            `,
                    })
                }
            })
        } catch (e) {
            console.warn('Could not apply token overlay:', e)
        }
    }, [elements])

    // Cleanup on unmount
    useEffect(() => {
        return () => {
            if (viewerRef.current) {
                viewerRef.current.destroy()
            }
        }
    }, [])

    return (
        <div style={{ position: 'relative', width: '100%', height: '100%' }}>
            {loading && (
                <div style={{
                    position: 'absolute', inset: 0,
                    display: 'flex', alignItems: 'center', justifyContent: 'center',
                    background: 'var(--op-surface)', color: 'var(--op-text-muted)',
                    zIndex: 10,
                }}>
                    Loading BPMN diagram...
                </div>
            )}

            {error && (
                <div style={{
                    position: 'absolute', inset: 0,
                    display: 'flex', alignItems: 'center', justifyContent: 'center',
                    color: 'var(--op-red)', zIndex: 10,
                }}>
                    {error}
                </div>
            )}

            {/* Legend */}
            {!loading && !error && (
                <div style={{
                    position: 'absolute', top: 8, right: 8,
                    background: 'var(--op-surface)',
                    border: '1px solid var(--op-border)',
                    borderRadius: 6, padding: '8px 12px',
                    zIndex: 10, fontSize: 12,
                }}>
                    {Object.entries(STATE_COLORS).map(([state, colors]) => (
                        <div key={state} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
                            <div style={{
                                width: 12, height: 12, borderRadius: 2,
                                background: colors.fill, border: `1.5px solid ${colors.stroke}`,
                            }} />
                            <span style={{ color: 'var(--op-text-muted)' }}>{state}</span>
                        </div>
                    ))}
                </div>
            )}

            {/* BPMN canvas */}
            <div
                ref={containerRef}
                style={{ width: '100%', height: '100%', background: 'var(--op-bg)' }}
            />

            {/* bpmn-js default styles */}
            <style>{`
        .bjs-container { height: 100% !important; }
        .djs-palette { display: none !important; }
        
        .state-COMPLETED .djs-visual rect,
        .state-COMPLETED .djs-visual circle,
        .state-COMPLETED .djs-visual polygon,
        .state-COMPLETED .djs-visual path {
            fill: #c8e6c9 !important;
            stroke: #388e3c !important;
        }
        
        .state-ACTIVE .djs-visual rect,
        .state-ACTIVE .djs-visual circle,
        .state-ACTIVE .djs-visual polygon,
        .state-ACTIVE .djs-visual path {
            fill: #a9d4f5 !important;
            stroke: #1b85cc !important;
        }
        
        .state-TERMINATED .djs-visual rect,
        .state-TERMINATED .djs-visual circle,
        .state-TERMINATED .djs-visual polygon,
        .state-TERMINATED .djs-visual path {
            fill: #e0e0e0 !important;
            stroke: #9e9e9e !important;
        }
        
        .state-INCIDENT .djs-visual rect,
        .state-INCIDENT .djs-visual circle,
        .state-INCIDENT .djs-visual polygon,
        .state-INCIDENT .djs-visual path {
            fill: #ffcdd2 !important;
            stroke: #c62828 !important;
        }
      `}</style>
        </div>
    )
}