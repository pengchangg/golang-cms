import { json } from '@codemirror/lang-json'
import { basicSetup, EditorView } from 'codemirror'
import { Alert, Button } from 'antd'
import { useEffect, useEffectEvent, useRef, useState } from 'react'

export function JsonEditor({ value, onChange, label, disabled, onValidityChange }: { value: unknown; onChange: (value: unknown) => void; label: string; disabled: boolean; onValidityChange?: (valid: boolean) => void }) {
  const hostRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView>(null)
  const initialText = value == null ? '' : JSON.stringify(value, null, 2)
  const externalRef = useRef(initialText)
  const textRef = useRef(initialText)
  const applyingExternalRef = useRef(false)
  const [error, setError] = useState<string>()
  const emitChange = useEffectEvent((next: unknown) => onChange(next))
  const emitValidity = useEffectEvent((valid: boolean) => onValidityChange?.(valid))

  useEffect(() => {
    if (!hostRef.current) return
    const view = new EditorView({
      doc: textRef.current,
      parent: hostRef.current,
      extensions: [basicSetup, json(), EditorView.lineWrapping, EditorView.contentAttributes.of({ 'aria-label': `${label} JSON 编辑器` }), EditorView.editable.of(!disabled), EditorView.updateListener.of((update) => {
        if (!update.docChanged) return
        const text = update.state.doc.toString()
        textRef.current = text
        if (applyingExternalRef.current) return
        try {
          const parsed = text ? JSON.parse(text) : null
          externalRef.current = parsed == null ? '' : JSON.stringify(parsed, null, 2)
          setError(undefined)
          emitValidity(true)
          emitChange(parsed)
        } catch (cause) {
          setError(cause instanceof Error ? cause.message : 'JSON 格式无效')
          emitValidity(false)
        }
      })],
    })
    viewRef.current = view
    return () => { view.destroy(); viewRef.current = null }
  }, [disabled, label])

  useEffect(() => {
    const serialized = value == null ? '' : JSON.stringify(value, null, 2)
    if (serialized === externalRef.current) return
    externalRef.current = serialized
    textRef.current = serialized
    const view = viewRef.current
    if (view) {
      applyingExternalRef.current = true
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: serialized } })
      applyingExternalRef.current = false
      setError(undefined)
      emitValidity(true)
    }
  }, [value])

  useEffect(() => () => emitValidity(true), [])

  function format() {
    try {
      const formatted = textRef.current ? JSON.stringify(JSON.parse(textRef.current), null, 2) : ''
      const view = viewRef.current
      if (view) view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: formatted } })
    } catch { /* 错误提示已由编辑监听器维护。 */ }
  }

  return <div className="json-editor">
    <div className="json-editor-actions"><Button size="small" disabled={disabled || Boolean(error)} onClick={format}>格式化 JSON</Button></div>
    <div ref={hostRef} />
    {error ? <Alert type="error" showIcon title="JSON 格式无效" description={error} /> : null}
  </div>
}
