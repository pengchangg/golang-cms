UPDATE audit_events ae LEFT JOIN users u ON u.id = ae.actor_id SET ae.actor_display_name = u.display_name WHERE ae.actor_type = 'user' AND ae.actor_display_name IS NULL;
