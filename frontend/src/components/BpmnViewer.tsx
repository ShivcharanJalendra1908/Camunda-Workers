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
                    background: '#1c1f23', color: 'var(--op-text-muted)',
                    zIndex: 10,
                }}>
                    Loading BPMN diagram...
                </div>
            )}

            {error && (
                <div style={{
                    position: 'absolute', inset: 0,
                    display: 'flex', alignItems: 'center', justifyContent: 'center',
                    color: '#ffcdd2', zIndex: 10,
                }}>
                    {error}
                </div>
            )}

            {/* Legend */}
            {!loading && !error && (
                <div style={{
                    position: 'absolute', top: 16, right: 16,
                    background: '#25292e',
                    border: '1px solid #3a3f45',
                    borderRadius: 6, padding: '12px 16px',
                    zIndex: 10, fontSize: 12,
                    boxShadow: '0 4px 6px rgba(0,0,0,0.3)'
                }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                        <div style={{ width: 12, height: 12, borderRadius: 2, background: '#1c3e5e', border: '1.5px solid #4593e6' }} />
                        <span style={{ color: '#eeeeee', fontWeight: 500 }}>ACTIVE</span>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                        <div style={{ width: 12, height: 12, borderRadius: 2, background: '#25292e', border: '1.5px solid #4593e6' }} />
                        <span style={{ color: '#eeeeee', fontWeight: 500 }}>COMPLETED</span>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                        <div style={{ width: 12, height: 12, borderRadius: 2, background: '#25292e', border: '1.5px solid #a0a0a0' }} />
                        <span style={{ color: '#eeeeee', fontWeight: 500 }}>DEFAULT</span>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <div style={{ width: 12, height: 12, borderRadius: 2, background: '#3b1c1c', border: '1.5px solid #e53935' }} />
                        <span style={{ color: '#eeeeee', fontWeight: 500 }}>INCIDENT</span>
                    </div>
                </div>
            )}

            {/* BPMN canvas */}
            <div
                ref={containerRef}
                style={{ width: '100%', height: '100%', background: '#1c1f23' }}
            />

            {/* bpmn-js dark theme styles */}
            <style>{`
        /* Import core bpmn-js styles to fix giant triangles and layout issues */
        @import url("https://unpkg.com/bpmn-js@18.15.0/dist/assets/diagram-js.css");
        @import url("https://unpkg.com/bpmn-js@18.15.0/dist/assets/bpmn-js.css");
        @import url("https://unpkg.com/bpmn-js@18.15.0/dist/assets/bpmn-font/css/bpmn.css");

        .bjs-container { 
            height: 100% !important; 
            background-color: #1c1f23 !important;
        }
        .djs-palette { display: none !important; }
        .bjs-powered-by { display: none !important; }

        /* 1. Base Dark Theme for unexecuted elements */
        .djs-element .djs-visual > rect,
        .djs-element .djs-visual > circle,
        .djs-element .djs-visual > polygon {
            fill: #25292e !important;
            stroke: #a0a0a0 !important;
            stroke-width: 2px !important;
        }

        .djs-connection .djs-visual > path {
            stroke: #666666 !important;
            stroke-width: 2px !important;
        }

        /* Make all arrowheads grey to match dark mode */
        marker path {
            fill: #a0a0a0 !important;
            stroke: #a0a0a0 !important;
        }

        /* Text colors to white */
        .djs-label, .djs-label > tspan, text {
            fill: #eeeeee !important;
        }

        /* 2. COMPLETED State (Traces execution path in Blue) */
        .state-COMPLETED.djs-shape .djs-visual > rect,
        .state-COMPLETED.djs-shape .djs-visual > circle,
        .state-COMPLETED.djs-shape .djs-visual > polygon {
            stroke: #4593e6 !important;
        }
        .state-COMPLETED.djs-connection .djs-visual > path {
            stroke: #4593e6 !important;
        }

        /* 3. ACTIVE State (Blue fill where token currently is) */
        .state-ACTIVE.djs-shape .djs-visual > rect,
        .state-ACTIVE.djs-shape .djs-visual > circle,
        .state-ACTIVE.djs-shape .djs-visual > polygon {
            stroke: #4593e6 !important;
            fill: #1c3e5e !important;
        }

        /* 4. INCIDENT State (Red highlight) */
        .state-INCIDENT.djs-shape .djs-visual > rect,
        .state-INCIDENT.djs-shape .djs-visual > circle,
        .state-INCIDENT.djs-shape .djs-visual > polygon {
            stroke: #e53935 !important;
            fill: #3b1c1c !important;
        }
      `}</style>
        </div>
    )
}