ALTER TABLE content_unique_values DROP INDEX uq_content_unique_values_entry_field, ADD UNIQUE KEY uq_content_unique_values_entry_field_value (entry_id, field_id, canonical_value);
