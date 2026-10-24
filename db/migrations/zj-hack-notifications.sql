-- A subscription belongs to the person, not the event's holding account.
-- Delete it on membership loss, including moves and approval revocation.
CREATE OR REPLACE FUNCTION revoke_hack_notifications() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'UPDATE' THEN
  IF OLD.team_id IS NOT DISTINCT FROM NEW.team_id AND OLD.role = NEW.role
     AND OLD.approval_status = NEW.approval_status THEN
   RETURN NEW;
  END IF;
 END IF;
 DELETE FROM site_notifications n USING sites s, events e
 WHERE n.site_id=s.id AND n.owner_id=OLD.user_id AND e.id=OLD.event_id
 AND s.user_id=e.account_id;
 RETURN NULL;
END;
$$;
CREATE OR REPLACE TRIGGER hack_members_notifications
 AFTER UPDATE OR DELETE ON event_members
 FOR EACH ROW EXECUTE FUNCTION revoke_hack_notifications();
