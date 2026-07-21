import type { ContentField, ContentFieldInput, ContentFieldPatch, ContentModel, FieldConstraints, FieldType, UpdateFieldOrderRequest, ValidationDetail } from './api/types'

export interface EnumFormOption { value: string; label: string; existing?: boolean }

export interface FieldFormValues {
  key: string
  display_name: string
  description?: string
  type: FieldType
  required?: boolean
  min_length?: number
  max_length?: number
  minimum?: string
  maximum?: string
  enum_options?: EnumFormOption[]
  target_model_id?: string
  unique?: boolean
  filterable?: boolean
  sortable?: boolean
  has_default?: boolean
  default_text?: string
  default_number?: number
  default_decimal?: string
  default_boolean?: boolean
  default_date?: string
  default_datetime?: string
  default_select?: string
  default_multi_select?: string[]
  initial_children?: FieldFormValues[]
}

const containerTypes = new Set<FieldType>(['object', 'repeatable_group'])
export const serverForbiddenDefaultTypes = new Set<FieldType>(['rich_text', 'single_media', 'multi_media', 'single_relation', 'multi_relation'])
export const complexDefaultTypes = new Set<FieldType>(['json', 'object', 'repeatable_group'])
const textTypes = new Set<FieldType>(['single_line_text', 'multi_line_text'])
const relationTypes = new Set<FieldType>(['single_relation', 'multi_relation'])
const rootProjectionTypes = new Set<FieldType>(['single_line_text', 'multi_line_text', 'integer', 'decimal', 'boolean', 'date', 'datetime', 'single_select'])

export function isContainerType(type?: FieldType) { return Boolean(type && containerTypes.has(type)) }
export function isTextType(type?: FieldType) { return Boolean(type && textTypes.has(type)) }
export function isRelationType(type?: FieldType) { return Boolean(type && relationTypes.has(type)) }
export function supportsRootProjection(type?: FieldType) { return Boolean(type && rootProjectionTypes.has(type)) }

export function rfc3339ToLocalInput(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const part = (number: number) => String(number).padStart(2, '0')
  return `${date.getFullYear()}-${part(date.getMonth() + 1)}-${part(date.getDate())}T${part(date.getHours())}:${part(date.getMinutes())}:${part(date.getSeconds())}`
}

export function activeFields(fields: ContentField[]) {
  return fields.filter((field) => field.status === 'active')
}

export function fieldToInput(field: ContentField): ContentFieldInput {
  return {
    key: field.key,
    display_name: field.display_name,
    description: field.description,
    type: field.type,
    required: field.required,
    default_value: field.default_value,
    constraints: { ...field.constraints, enum_options: field.constraints.enum_options?.map((option) => ({ ...option })) },
    children: activeFields(field.children).map(fieldToInput),
  }
}

export function replaceSiblings(model: ContentModel, parentId: string | null, siblings: ContentField[]): ContentModel {
  if (parentId === null) return { ...model, fields: siblings }
  const replace = (fields: ContentField[]): ContentField[] => fields.map((field) => field.id === parentId
    ? { ...field, children: siblings }
    : { ...field, children: replace(field.children) })
  return { ...model, fields: replace(model.fields) }
}

export function reorderActiveSiblings(siblings: ContentField[], activeIds: string[]) {
  const byId = new Map(activeFields(siblings).map((field) => [field.id, field]))
  let index = 0
  return siblings.map((field) => field.status === 'archived' ? field : byId.get(activeIds[index++]) ?? field)
}

export function buildOrderRequest(parentId: string | null, siblings: ContentField[], activeId: string, overId: string): UpdateFieldOrderRequest | null {
  const base = activeFields(siblings).map((field) => field.id)
  const from = base.indexOf(activeId)
  const to = base.indexOf(overId)
  if (from < 0 || to < 0 || from === to) return null
  const fieldIds = [...base]
  fieldIds.splice(to, 0, fieldIds.splice(from, 1)[0])
  return { parent_id: parentId, base_field_ids: base, field_ids: fieldIds }
}

function constraintsFromForm(value: FieldFormValues, root: boolean): FieldConstraints {
  const constraints: FieldConstraints = {}
  if (isTextType(value.type)) {
    if (value.min_length !== undefined) constraints.min_length = value.min_length
    if (value.max_length !== undefined) constraints.max_length = value.max_length
  }
  if (value.type === 'integer' || value.type === 'decimal') {
    if (value.minimum) constraints.minimum = value.minimum
    if (value.maximum) constraints.maximum = value.maximum
  }
  if (value.type === 'single_select' || value.type === 'multi_select') {
    constraints.enum_options = (value.enum_options ?? []).map(({ value: optionValue, label }) => ({ value: optionValue, label }))
  }
  if (isRelationType(value.type) && value.target_model_id) constraints.target_model_id = value.target_model_id
  if (root && supportsRootProjection(value.type)) {
    constraints.unique = Boolean(value.unique)
    constraints.filterable = Boolean(value.filterable)
    constraints.sortable = Boolean(value.sortable)
  }
  return constraints
}

export function defaultFromForm(value: FieldFormValues): unknown {
  if (!value.has_default) return null
  switch (value.type) {
    case 'single_line_text': case 'multi_line_text': return value.default_text ?? ''
    case 'integer':
      if (!Number.isSafeInteger(value.default_number)) throw new RangeError('整数默认值必须是 JavaScript 安全整数')
      return value.default_number
    case 'decimal': return value.default_decimal ?? null
    case 'boolean': return Boolean(value.default_boolean)
    case 'date': return value.default_date ?? null
    case 'datetime': return value.default_datetime ? new Date(value.default_datetime).toISOString() : null
    case 'single_select': return value.default_select ?? null
    case 'multi_select': return value.default_multi_select ?? []
    default: return null
  }
}

export function formToFieldInput(value: FieldFormValues, root: boolean): ContentFieldInput {
  return {
    key: value.key,
    display_name: value.display_name,
    description: value.description ?? '',
    type: value.type,
    required: Boolean(value.required),
    default_value: defaultFromForm(value),
    constraints: constraintsFromForm(value, root),
    children: isContainerType(value.type) ? (value.initial_children ?? []).map((child) => formToFieldInput(child, false)) : [],
  }
}

export function formToFieldPatch(value: FieldFormValues, original: ContentField, root: boolean): ContentFieldPatch {
  const input = formToFieldInput(value, root)
  const patch: ContentFieldPatch = {
    display_name: input.display_name,
    description: input.description,
    required: input.required,
    default_value: value.type === original.type && complexDefaultTypes.has(value.type) ? original.default_value : input.default_value,
    constraints: input.constraints,
  }
  if (value.type !== original.type) {
    patch.type = value.type
    patch.children = isContainerType(value.type)
      ? (value.initial_children ?? activeFields(original.children).map(fieldToForm)).map((child) => formToFieldInput(child, false))
      : []
  }
  return patch
}

function defaultFormField(type: FieldType) {
  switch (type) {
    case 'single_line_text': case 'multi_line_text': return 'default_text'
    case 'integer': return 'default_number'
    case 'decimal': return 'default_decimal'
    case 'boolean': return 'default_boolean'
    case 'date': return 'default_date'
    case 'datetime': return 'default_datetime'
    case 'single_select': return 'default_select'
    case 'multi_select': return 'default_multi_select'
    default: return 'has_default'
  }
}

function formValuesAt(values: FieldFormValues, path: (string | number)[]) {
  let current: FieldFormValues | undefined = values
  for (let index = 0; index < path.length; index += 2) {
    if (path[index] !== 'initial_children' || typeof path[index + 1] !== 'number') return undefined
    current = current?.initial_children?.[path[index + 1] as number]
  }
  return current
}

export function validationFields(details: ValidationDetail[], values: FieldFormValues) {
  return details.flatMap((detail) => {
    const tokens = detail.path.split('/').slice(1).map((token) => token.replaceAll('~1', '/').replaceAll('~0', '~'))
    const name: (string | number)[] = []
    for (let index = 0; index < tokens.length;) {
      const token = tokens[index]
      if (token === 'children') {
        if (tokens[index + 1] === undefined) {
          name.push('initial_children')
          break
        }
        const childIndex = Number(tokens[index + 1])
        if (!Number.isInteger(childIndex)) return []
        name.push('initial_children', childIndex)
        index += 2
        continue
      }
      if (token === 'constraints') {
        const constraint = tokens[index + 1]
        const mapped: Record<string, string> = {
          min_length: 'min_length', max_length: 'max_length', minimum: 'minimum', maximum: 'maximum',
          enum_options: 'enum_options', target_model_id: 'target_model_id', unique: 'unique', filterable: 'filterable', sortable: 'sortable',
        }
        if (!constraint) {
          const current = formValuesAt(values, name) ?? values
          const projections = (['unique', 'filterable', 'sortable'] as const).filter((field) => current[field])
          return (projections.length ? projections : ['type']).map((field) => ({ name: [...name, field], errors: [detail.message] }))
        }
        if (!mapped[constraint]) return []
        name.push(mapped[constraint])
        index += 2
        if (constraint === 'enum_options' && tokens[index] !== undefined) {
          const optionIndex = Number(tokens[index])
          if (!Number.isInteger(optionIndex)) return []
          name.push(optionIndex)
          if (tokens[index + 1] === 'value' || tokens[index + 1] === 'label') name.push(tokens[index + 1])
        }
        break
      }
      if (token === 'default_value') {
        name.push(defaultFormField(formValuesAt(values, name)?.type ?? values.type))
        break
      }
      if (token === 'key' || token === 'display_name' || token === 'description' || token === 'type' || token === 'required') {
        name.push(token)
        break
      }
      return []
    }
    return name.length ? [{ name, errors: [detail.message] }] : []
  })
}

export function fieldToForm(field: ContentField): FieldFormValues {
  const defaultValue = field.default_value
  const hasDefault = defaultValue !== null && defaultValue !== undefined
  return {
    key: field.key,
    display_name: field.display_name,
    description: field.description,
    type: field.type,
    required: field.required,
    min_length: field.constraints.min_length,
    max_length: field.constraints.max_length,
    minimum: field.constraints.minimum,
    maximum: field.constraints.maximum,
    enum_options: field.constraints.enum_options?.map((option) => ({ ...option, existing: true })),
    target_model_id: field.constraints.target_model_id,
    unique: field.constraints.unique,
    filterable: field.constraints.filterable,
    sortable: field.constraints.sortable,
    has_default: hasDefault,
    default_text: typeof defaultValue === 'string' ? defaultValue : undefined,
    default_number: typeof defaultValue === 'number' ? defaultValue : undefined,
    default_decimal: field.type === 'decimal' && typeof defaultValue === 'string' ? defaultValue : undefined,
    default_boolean: typeof defaultValue === 'boolean' ? defaultValue : undefined,
    default_date: field.type === 'date' && typeof defaultValue === 'string' ? defaultValue : undefined,
    default_datetime: field.type === 'datetime' && typeof defaultValue === 'string' ? rfc3339ToLocalInput(defaultValue) : undefined,
    default_select: field.type === 'single_select' && typeof defaultValue === 'string' ? defaultValue : undefined,
    default_multi_select: field.type === 'multi_select' && Array.isArray(defaultValue) ? defaultValue as string[] : undefined,
    initial_children: activeFields(field.children).map(fieldToForm),
  }
}
