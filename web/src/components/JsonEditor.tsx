import { json } from '@codemirror/lang-json'
import { basicSetup, EditorView } from 'codemirror'
import { Alert, Button } from 'antd'
import { useEffect, useEffectEvent, useRef, useState } from 'react'

function serializeJSON(value: unknown) {
  return value == null ? '' : JSON.stringify(value, null, 2)
}

interface JsonEditorProps {
  value: unknown
  onChange: (value: unknown) => void
  label: string
  disabled: boolean
  variant?: 'preview' | 'full'
  onValidityChange?: (valid: boolean) => void
  labelledBy?: string
  describedBy?: string
}

export function JsonEditor({ value, onChange, label, disabled, variant = 'full', onValidityChange, labelledBy, describedBy }: JsonEditorProps) {
  const hostRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView>(null)
  const initialText = serializeJSON(value)
  const externalRef = useRef(initialText)
  const textRef = useRef(initialText)
  const applyingExternalRef = useRef(false)
  const [error, setError] = useState<string>()
  const emitChange = useEffectEvent((next: unknown) => onChange(next))
  const emitValidity = useEffectEvent((valid: boolean) => onValidityChange?.(valid))
  const isPreview = variant === 'preview'

  useEffect(() => {
    if (isPreview) {
      emitValidity(true)
      return () => emitValidity(true)
    }
    if (!hostRef.current) return
    const view = new EditorView({
      doc: textRef.current,
      parent: hostRef.current,
      extensions: [
        basicSetup,
        json(),
        EditorView.lineWrapping,
        EditorView.contentAttributes.of(
          labelledBy
            ? { 'aria-labelledby': labelledBy, ...(describedBy ? { 'aria-describedby': describedBy } : {}) }
            : { 'aria-label': `${label} JSON 编辑器` },
        ),
        EditorView.editable.of(!disabled),
        EditorView.updateListener.of((update) => {
          if (!update.docChanged) return
          const text = update.state.doc.toString()
          textRef.current = text
          if (applyingExternalRef.current) return
          try {
            const parsed = text ? JSON.parse(text) : null
            externalRef.current = serializeJSON(parsed)
            setError(undefined)
            emitValidity(true)
            emitChange(parsed)
          } catch (cause) {
            setError(cause instanceof Error ? cause.message : 'JSON 格式无效')
            emitValidity(false)
          }
        }),
      ],
    })
    viewRef.current = view
    emitValidity(true)
    return () => {
      view.destroy()
      viewRef.current = null
      emitValidity(true)
    }
  }, [describedBy, disabled, isPreview, label, labelledBy])

  useEffect(() => {
    if (isPreview) return
    const serialized = serializeJSON(value)
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
  }, [isPreview, value])

  function format() {
    try {
      const formatted = textRef.current ? JSON.stringify(JSON.parse(textRef.current), null, 2) : ''
      const view = viewRef.current
      if (view) view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: formatted } })
    } catch { /* 错误提示已由编辑监听器维护。 */ }
  }

  if (isPreview) {
    const text = serializeJSON(value) || 'null'
    return (
      <div
        className="json-editor is-preview is-disabled"
        role="group"
        aria-label={labelledBy ? undefined : `${label} JSON 预览`}
        aria-labelledby={labelledBy}
        aria-describedby={describedBy}
      >
        <pre className="json-editor-preview">{text}</pre>
      </div>
    )
  }

  return (
    <div className={`json-editor is-full${disabled ? ' is-disabled' : ''}`} role="group" aria-label={labelledBy ? undefined : `${label} JSON 编辑器`} aria-labelledby={labelledBy} aria-describedby={describedBy}>
      <div className="json-editor-actions">
        <Button size="small" disabled={disabled || Boolean(error)} onClick={format}>格式化 JSON</Button>
      </div>
      <div className="json-editor-host" ref={hostRef} />
      {error ? <Alert type="error" showIcon title="JSON 格式无效" description={error} /> : null}
    </div>
  )
}
