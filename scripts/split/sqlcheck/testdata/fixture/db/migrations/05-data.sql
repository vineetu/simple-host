UPDATE users SET event_account=true FROM events WHERE users.id=events.id;
