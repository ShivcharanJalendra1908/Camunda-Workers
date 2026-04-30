// hooks/useOperateWS.ts
import { useEffect, useRef, useCallback } from 'react'
import type { WSEvent, WSEventType } from '../types/operate'

type Handler = (event: WSEvent) => void

export function useOperateWS(handlers: Partial<Record<WSEventType, Handler>>) {
  const wsRef = useRef<WebSocket | null>(null)
  const handlersRef = useRef(handlers)
  handlersRef.current = handlers

  const connect = useCallback(() => {
    const wsBase = (import.meta.env.VITE_API_URL ?? 'http://localhost:8080')
      .replace(/^http/, 'ws')
    const ws = new WebSocket(`${wsBase}/operate/ws`)

    ws.onmessage = (e) => {
      try {
        const event: WSEvent = JSON.parse(e.data)
        const handler = handlersRef.current[event.type]
        if (handler) handler(event)
      } catch {
        // ignore malformed messages
      }
    }

    ws.onclose = () => {
      // reconnect after 3s
      setTimeout(connect, 3000)
    }

    wsRef.current = ws
    return ws
  }, [])

  useEffect(() => {
    const ws = connect()
    return () => ws.close()
  }, [connect])
}
