import { ExpandOutlined } from '@ant-design/icons'
import { Button, Drawer } from 'antd'
import { Suspense, useState, type ReactNode } from 'react'

interface ComplexFieldShellProps {
  label: string
  disabled: boolean
  preview: ReactNode
  children: (close: () => void) => ReactNode
  loadingLabel: string
}

export function ComplexFieldShell({ label, disabled, preview, children, loadingLabel }: ComplexFieldShellProps) {
  const [open, setOpen] = useState(false)
  const close = () => setOpen(false)

  return (
    <div className="complex-field-shell">
      <div className="complex-field-preview">{preview}</div>
      <Button
        type="default"
        icon={<ExpandOutlined aria-hidden />}
        aria-label={disabled ? '查看' : '展开编辑'}
        onClick={() => setOpen(true)}
      >
        {disabled ? '查看' : '展开编辑'}
      </Button>
      <Drawer
        className="complex-field-drawer"
        title={label}
        placement="right"
        size="100%"
        open={open}
        onClose={close}
        destroyOnHidden
        styles={{ body: { padding: 0, display: 'flex', flexDirection: 'column', minHeight: 0 } }}
        extra={<Button type="primary" onClick={close}>完成</Button>}
      >
        <div className="complex-field-drawer-body">
          <Suspense fallback={<div className="editor-loading">{loadingLabel}</div>}>
            {open ? children(close) : null}
          </Suspense>
        </div>
      </Drawer>
    </div>
  )
}
