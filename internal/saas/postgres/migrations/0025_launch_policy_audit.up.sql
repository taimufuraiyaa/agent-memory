ALTER TABLE saas_launch_policy
    ADD COLUMN updated_by text NOT NULL DEFAULT 'migration',
    ADD COLUMN reason_code text NOT NULL DEFAULT 'initial_policy';

CREATE TABLE saas_launch_policy_history (
    id uuid PRIMARY KEY,
    old_phase text NOT NULL,
    new_phase text NOT NULL,
    old_signup_enabled boolean NOT NULL,
    new_signup_enabled boolean NOT NULL,
    old_invitation_required boolean NOT NULL,
    new_invitation_required boolean NOT NULL,
    old_account_cap integer NOT NULL,
    new_account_cap integer NOT NULL,
    actor_id text NOT NULL,
    reason_code text NOT NULL,
    changed_at timestamptz NOT NULL
);

CREATE FUNCTION saas_capture_launch_policy_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO saas_launch_policy_history(id,old_phase,new_phase,old_signup_enabled,new_signup_enabled,old_invitation_required,new_invitation_required,old_account_cap,new_account_cap,actor_id,reason_code,changed_at)
    VALUES(gen_random_uuid(),OLD.phase,NEW.phase,OLD.signup_enabled,NEW.signup_enabled,OLD.invitation_required,NEW.invitation_required,OLD.account_cap,NEW.account_cap,NEW.updated_by,NEW.reason_code,NEW.updated_at);
    RETURN NEW;
END $$;
CREATE TRIGGER saas_launch_policy_audit AFTER UPDATE ON saas_launch_policy
FOR EACH ROW EXECUTE FUNCTION saas_capture_launch_policy_change();

CREATE FUNCTION saas_deny_launch_history_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'launch policy history is immutable';
END $$;
CREATE TRIGGER saas_launch_policy_history_immutable BEFORE UPDATE OR DELETE ON saas_launch_policy_history
FOR EACH ROW EXECUTE FUNCTION saas_deny_launch_history_mutation();

