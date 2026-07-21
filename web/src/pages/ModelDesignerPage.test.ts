import { describe, expect, it } from 'vitest'

import type { ContentField, ContentModelSummary, FieldType } from '../api/types'
import { activeFields, buildOrderRequest, defaultFromForm, fieldToInput, formToFieldInput, formToFieldPatch, reorderActiveSiblings, rfc3339ToLocalInput, validationFields, type FieldFormValues } from '../modelDesigner'
import { relationModelOptions } from '../relationModels'

const now = '2026-07-20T08:00:00Z'

function model(id: string, key: string, displayName: string): ContentModelSummary {
  return { id, key, display_name: displayName, description: '', status: 'active', created_at: now, updated_at: now }
}

function field(id: string, overrides: Partial<ContentField> = {}): ContentField {
  return {
    id, key: id, display_name: id, description: '', type: 'single_line_text', required: false, default_value: null,
    constraints: {}, children: [], status: 'active', created_at: now, updated_at: now, ...overrides,
  }
}

describe('模型字段设计器纯函数', () => {
  it('排除当前关联模型并用模型 ID 作为提交值', () => {
    const options = relationModelOptions([model('mdl_current', 'articles', '文章'), model('mdl_author', 'authors', '作者')], 'mdl_current')
    expect(options).toEqual([{ value: 'mdl_author', label: '作者', searchText: '作者 authors', key: 'authors' }])
  })

  it('只对活动同级字段构造完整排序基线并保留归档位置', () => {
    const siblings = [field('fld_a'), field('fld_old', { status: 'archived' }), field('fld_b'), field('fld_c')]
    const request = buildOrderRequest('fld_parent', siblings, 'fld_c', 'fld_a')
    expect(request).toEqual({ parent_id: 'fld_parent', base_field_ids: ['fld_a', 'fld_b', 'fld_c'], field_ids: ['fld_c', 'fld_a', 'fld_b'] })
    expect(reorderActiveSiblings(siblings, request!.field_ids).map((item) => item.id)).toEqual(['fld_c', 'fld_old', 'fld_a', 'fld_b'])
  })

  it('向父容器回传 children 时递归排除归档字段', () => {
    const parent = field('fld_parent', { type: 'object', children: [
      field('fld_active', { children: [field('fld_nested'), field('fld_nested_old', { status: 'archived' })] }),
      field('fld_archived', { status: 'archived' }),
    ] })
    expect(activeFields(parent.children).map(fieldToInput)).toEqual([expect.objectContaining({ key: 'fld_active', children: [expect.objectContaining({ key: 'fld_nested' })] })])
  })

  it('序列化受控配置且不产生 children_json', () => {
    const input = formToFieldInput({
      key: 'category', display_name: '分类', type: 'single_select', required: true,
      enum_options: [{ value: 'news', label: '新闻', existing: true }], has_default: true, default_select: 'news', filterable: true,
    }, true)
    expect(input).toEqual({
      key: 'category', display_name: '分类', description: '', type: 'single_select', required: true, default_value: 'news',
      constraints: { enum_options: [{ value: 'news', label: '新闻' }], unique: false, filterable: true, sortable: false }, children: [],
    })
    expect(input).not.toHaveProperty('children_json')
  })

  it('类型修改显式提交 default_value、constraints 和活动 children', () => {
    const original = field('fld_container', { type: 'object', children: [field('fld_title'), field('fld_old', { status: 'archived' })] })
    const patch = formToFieldPatch({ key: original.key, display_name: '容器', type: 'repeatable_group', initial_children: activeFields(original.children).map((child) => ({ key: child.key, display_name: child.display_name, type: child.type })) }, original, true)
    expect(patch).toMatchObject({ type: 'repeatable_group', default_value: null, constraints: {}, children: [expect.objectContaining({ key: 'fld_title' })] })
    expect(patch.children).toHaveLength(1)
  })

  it.each([
    ['single_line_text', '文本'], ['multi_line_text', '文本'], ['rich_text', null], ['integer', 1], ['decimal', '1.5'],
    ['boolean', true], ['date', '2026-07-20'], ['datetime', '2026-07-20T08:00:00.000Z'], ['single_select', 'news'],
    ['multi_select', ['news']], ['json', { enabled: true }], ['single_media', null], ['multi_media', null],
    ['single_relation', null], ['multi_relation', null], ['object', { title: '默认标题' }], ['repeatable_group', [{ title: '默认标题' }]],
  ] satisfies Array<[FieldType, unknown]>)('同类型编辑 %s 时正确处理默认值', (type, originalDefault) => {
    const original = field(`fld_${type}`, { type, default_value: originalDefault })
    const values: FieldFormValues = { key: original.key, display_name: '更新名称', type, has_default: originalDefault !== null }
    if (type === 'single_line_text' || type === 'multi_line_text') values.default_text = String(originalDefault)
    if (type === 'integer') values.default_number = Number(originalDefault)
    if (type === 'decimal') values.default_decimal = String(originalDefault)
    if (type === 'boolean') values.default_boolean = Boolean(originalDefault)
    if (type === 'date') values.default_date = String(originalDefault)
    if (type === 'datetime') values.default_datetime = rfc3339ToLocalInput(String(originalDefault))
    if (type === 'single_select') { values.default_select = String(originalDefault); values.enum_options = [{ value: 'news', label: '新闻' }] }
    if (type === 'multi_select') { values.default_multi_select = ['news']; values.enum_options = [{ value: 'news', label: '新闻' }] }
    expect(formToFieldPatch(values, original, true).default_value).toEqual(originalDefault)
  })

  it('整数默认值只接受 JS safe integer 边界', () => {
    expect(defaultFromForm({ key: 'count', display_name: '数量', type: 'integer', has_default: true, default_number: Number.MAX_SAFE_INTEGER })).toBe(Number.MAX_SAFE_INTEGER)
    expect(defaultFromForm({ key: 'count', display_name: '数量', type: 'integer', has_default: true, default_number: Number.MIN_SAFE_INTEGER })).toBe(Number.MIN_SAFE_INTEGER)
    expect(() => defaultFromForm({ key: 'count', display_name: '数量', type: 'integer', has_default: true, default_number: Number.MAX_SAFE_INTEGER + 1 })).toThrow('安全整数')
    expect(() => defaultFromForm({ key: 'count', display_name: '数量', type: 'integer', has_default: true, default_number: 1.5 })).toThrow('安全整数')
  })

  it('把服务端字段校验路径映射到主字段、约束和初始子字段表单项', () => {
    const values: FieldFormValues = {
      key: 'root', display_name: '根', type: 'object', unique: true, filterable: true, initial_children: [{
        key: 'child', display_name: '子', type: 'single_select', enum_options: [{ value: 'news', label: '新闻' }],
      }],
    }
    const mapped = validationFields([
      { path: '/display_name', code: 'too_long', message: '名称过长' },
      { path: '/constraints/minimum', code: 'invalid_format', message: '最小值无效' },
      { path: '/constraints', code: 'index_not_allowed', message: '不能创建投影' },
      { path: '/children', code: 'required', message: '需要子字段' },
      { path: '/children/0/type', code: 'invalid_value', message: '类型无效' },
      { path: '/children/0/default_value', code: 'invalid_value', message: '默认值无效' },
      { path: '/children/0/constraints/enum_options/0/value', code: 'duplicate', message: '选项重复' },
    ], values)
    expect(mapped).toEqual([
      { name: ['display_name'], errors: ['名称过长'] },
      { name: ['minimum'], errors: ['最小值无效'] },
      { name: ['unique'], errors: ['不能创建投影'] },
      { name: ['filterable'], errors: ['不能创建投影'] },
      { name: ['initial_children'], errors: ['需要子字段'] },
      { name: ['initial_children', 0, 'type'], errors: ['类型无效'] },
      { name: ['initial_children', 0, 'default_select'], errors: ['默认值无效'] },
      { name: ['initial_children', 0, 'enum_options', 0, 'value'], errors: ['选项重复'] },
    ])
  })

  it('日期时间按浏览器本地时区显示并能还原同一时刻', () => {
    const source = '2026-07-20T08:15:00Z'
    const local = rfc3339ToLocalInput(source)
    expect(new Date(local).getTime()).toBe(new Date(source).getTime())
  })
})
