import type { FieldType } from './api/types'

export const fieldTypeMeta = {
  single_line_text: { label: '单行文本', description: '标题、名称等简短文字' },
  multi_line_text: { label: '多行文本', description: '摘要、说明等较长纯文本' },
  rich_text: { label: '富文本', description: '所见即所得正文内容' },
  integer: { label: '整数', description: '不含小数的数字' },
  decimal: { label: '小数', description: '价格、比例等精确数值' },
  boolean: { label: '布尔值', description: '是或否的开关状态' },
  date: { label: '日期', description: '仅包含年月日' },
  datetime: { label: '日期时间', description: '包含日期和具体时间' },
  single_select: { label: '单选', description: '从预设选项中选择一项' },
  multi_select: { label: '多选', description: '从预设选项中选择多项' },
  json: { label: 'JSON 数据', description: '保存受控的结构化数据' },
  single_media: { label: '单个素材', description: '选择一张图片或一个文件' },
  multi_media: { label: '多个素材', description: '选择多个图片或文件' },
  single_relation: { label: '单条关联', description: '关联另一个模型的一条内容' },
  multi_relation: { label: '多条关联', description: '关联另一个模型的多条内容' },
  object: { label: '对象', description: '将多个子字段组织成一组' },
  repeatable_group: { label: '可重复分组', description: '保存多组相同结构的数据' },
} satisfies Record<FieldType, { label: string; description: string }>

export const fieldTypeOptions = Object.entries(fieldTypeMeta).map(([value, meta]) => ({
  value: value as FieldType,
  label: meta.label,
  description: meta.description,
}))
