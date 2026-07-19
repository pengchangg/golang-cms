INSERT INTO content_field_values (
    revision_id,
    entry_id,
    model_id,
    field_id,
    value_type,
    string_value,
    integer_value,
    decimal_value,
    boolean_value,
    date_value,
    datetime_value
)
SELECT
    rv.id,
    rv.entry_id,
    rv.model_id,
    f.id,
    CASE
        WHEN f.field_type IN ('single_line_text', 'multi_line_text', 'single_select') THEN 'string'
        WHEN f.field_type = 'integer' THEN 'integer'
        WHEN f.field_type = 'decimal' THEN 'decimal'
        WHEN f.field_type = 'boolean' THEN 'boolean'
        WHEN f.field_type = 'date' THEN 'date'
        WHEN f.field_type = 'datetime' THEN 'datetime'
    END,
    CASE WHEN f.field_type IN ('single_line_text', 'multi_line_text', 'single_select') THEN JSON_UNQUOTE(JSON_EXTRACT(rv.content, CONCAT('$.', f.field_key))) END,
    CASE WHEN f.field_type = 'integer' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(rv.content, CONCAT('$.', f.field_key))) AS SIGNED) END,
    CASE WHEN f.field_type = 'decimal' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(rv.content, CONCAT('$.', f.field_key))) AS DECIMAL(65,30)) END,
    CASE WHEN f.field_type = 'boolean' THEN CASE JSON_UNQUOTE(JSON_EXTRACT(rv.content, CONCAT('$.', f.field_key))) WHEN 'true' THEN TRUE WHEN 'false' THEN FALSE END END,
    CASE WHEN f.field_type = 'date' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(rv.content, CONCAT('$.', f.field_key))) AS DATE) END,
    CASE WHEN f.field_type = 'datetime' THEN CAST(REPLACE(REPLACE(JSON_UNQUOTE(JSON_EXTRACT(rv.content, CONCAT('$.', f.field_key))), 'T', ' '), 'Z', '') AS DATETIME(6)) END
FROM content_revisions AS rv
JOIN content_fields AS f ON f.model_id = rv.model_id AND f.parent_id IS NULL
WHERE f.field_type IN ('single_line_text', 'multi_line_text', 'single_select', 'integer', 'decimal', 'boolean', 'date', 'datetime')
  AND (
      JSON_UNQUOTE(JSON_EXTRACT(f.constraints, '$.unique')) = 'true'
      OR JSON_UNQUOTE(JSON_EXTRACT(f.constraints, '$.filterable')) = 'true'
      OR JSON_UNQUOTE(JSON_EXTRACT(f.constraints, '$.sortable')) = 'true'
  )
  AND JSON_TYPE(JSON_EXTRACT(rv.content, CONCAT('$.', f.field_key))) IS NOT NULL
  AND JSON_TYPE(JSON_EXTRACT(rv.content, CONCAT('$.', f.field_key))) <> 'NULL'
  AND CASE
      WHEN f.field_type IN ('single_line_text', 'multi_line_text', 'single_select', 'decimal', 'date', 'datetime') THEN JSON_TYPE(JSON_EXTRACT(rv.content, CONCAT('$.', f.field_key))) = 'STRING'
      WHEN f.field_type = 'integer' THEN JSON_TYPE(JSON_EXTRACT(rv.content, CONCAT('$.', f.field_key))) = 'INTEGER'
      WHEN f.field_type = 'boolean' THEN JSON_TYPE(JSON_EXTRACT(rv.content, CONCAT('$.', f.field_key))) = 'BOOLEAN'
  END
  AND NOT EXISTS (
      SELECT 1
      FROM content_field_values AS existing
      WHERE existing.revision_id = rv.id AND existing.field_id = f.id
  );
