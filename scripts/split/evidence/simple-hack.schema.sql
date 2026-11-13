--
-- PostgreSQL database dump
--

\restrict 3Glxecz5SCHN3NEcQnKNhlxInf4e0Aq6hXsH69LbBzWiAJT7Cu6ckf7bEshjdaS

-- Dumped from database version 16.15 (Ubuntu 16.15-0ubuntu0.24.04.1)
-- Dumped by pg_dump version 16.15 (Ubuntu 16.15-0ubuntu0.24.04.1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: clear_hack_former_judge(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.clear_hack_former_judge() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 DELETE FROM event_former_judges WHERE event_id = NEW.event_id AND user_id = NEW.user_id;
 IF NEW.role = 'participant' THEN
   DELETE FROM event_scores WHERE event_id = NEW.event_id AND judge_id = NEW.user_id;
 END IF;
 RETURN NULL;
END;
$$;


--
-- Name: hack_clear_team_admin_on_team_change(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.hack_clear_team_admin_on_team_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  NEW.team_admin := false;
  IF NEW.team_joined_at IS NOT DISTINCT FROM OLD.team_joined_at THEN
    NEW.team_joined_at := NULL;
  END IF;
  RETURN NEW;
END;
$$;


--
-- Name: revoke_hack_notifications(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.revoke_hack_notifications() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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


--
-- Name: sh_history_bytes(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.sh_history_bytes() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    UPDATE sites s SET history_bytes = s.history_bytes + d.n
      FROM (SELECT site_id, sum(COALESCE(octet_length(prev::text), octet_length(diff::text), 0)) AS n
              FROM sh_hnew GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  ELSIF TG_OP = 'DELETE' THEN
    UPDATE sites s SET history_bytes = s.history_bytes - d.n
      FROM (SELECT site_id, sum(COALESCE(octet_length(prev::text), octet_length(diff::text), 0)) AS n
              FROM sh_hold GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  ELSE
    UPDATE sites s SET history_bytes = s.history_bytes + d.n
      FROM (SELECT site_id, sum(n) AS n FROM (
              SELECT site_id, COALESCE(octet_length(prev::text), octet_length(diff::text), 0) AS n FROM sh_hnew
              UNION ALL
              SELECT site_id, -COALESCE(octet_length(prev::text), octet_length(diff::text), 0) FROM sh_hold) x
             GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  END IF;
  RETURN NULL;
END $$;


--
-- Name: sh_items_live_bytes(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.sh_items_live_bytes() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    UPDATE sites s SET data_bytes = s.data_bytes + d.n
      FROM (SELECT site_id, sum(octet_length(data::text)) AS n FROM sh_new
             WHERE deleted_at IS NULL GROUP BY site_id) d
     WHERE s.id = d.site_id;
  ELSIF TG_OP = 'DELETE' THEN
    UPDATE sites s SET data_bytes = s.data_bytes - d.n
      FROM (SELECT site_id, sum(octet_length(data::text)) AS n FROM sh_old
             WHERE deleted_at IS NULL GROUP BY site_id) d
     WHERE s.id = d.site_id;
  ELSE
    UPDATE sites s SET data_bytes = s.data_bytes + d.n
      FROM (SELECT site_id, sum(n) AS n FROM (
              SELECT site_id, octet_length(data::text) AS n FROM sh_new WHERE deleted_at IS NULL
              UNION ALL
              SELECT site_id, -octet_length(data::text) FROM sh_old WHERE deleted_at IS NULL) x
             GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  END IF;
  RETURN NULL;
END $$;


--
-- Name: sh_sites_state_bytes(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.sh_sites_state_bytes() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
  s BIGINT := COALESCE(octet_length(NEW.state::text), 0);
BEGIN
  IF TG_OP = 'INSERT' THEN
    NEW.data_bytes := s;
  ELSE
    NEW.data_bytes := NEW.data_bytes + s - OLD.state_bytes;
  END IF;
  NEW.state_bytes := s;
  RETURN NEW;
END $$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: account_passwords; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.account_passwords (
    user_id uuid NOT NULL,
    password_hash text NOT NULL,
    must_change boolean DEFAULT false NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    login text NOT NULL
);


--
-- Name: address_families; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.address_families (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    suffix text NOT NULL,
    site_prefix text DEFAULT ''::text NOT NULL,
    rank integer DEFAULT 0 NOT NULL,
    canonical boolean DEFAULT true NOT NULL,
    token text DEFAULT ('sh-'::text || replace((gen_random_uuid())::text, '-'::text, ''::text)) NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    last_error text,
    bound_at timestamp with time zone DEFAULT now() NOT NULL,
    verified_at timestamp with time zone,
    checked_at timestamp with time zone,
    failing_since timestamp with time zone,
    lapse_notified_at timestamp with time zone,
    release_at timestamp with time zone,
    cert_mode text DEFAULT 'wildcard'::text NOT NULL,
    cert_name text,
    proof_exempt boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT address_families_cert_mode_check CHECK ((cert_mode = ANY (ARRAY['per_host'::text, 'wildcard'::text])))
);


--
-- Name: analytics_ingest_state; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.analytics_ingest_state (
    logfile text NOT NULL,
    offset_bytes bigint DEFAULT 0 NOT NULL,
    inode bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now()
);


--
-- Name: api_growth_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.api_growth_daily (
    day date NOT NULL,
    dim text NOT NULL,
    key text NOT NULL,
    calls bigint DEFAULT 0 NOT NULL
);


--
-- Name: api_ip_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.api_ip_daily (
    day date NOT NULL,
    ip text NOT NULL,
    calls bigint DEFAULT 0 NOT NULL,
    last_route text DEFAULT ''::text NOT NULL,
    last_seen timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: api_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.api_keys (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    key_hash text NOT NULL,
    user_id uuid NOT NULL,
    name text,
    last4 text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_used_at timestamp with time zone,
    scope text DEFAULT 'full'::text NOT NULL,
    expires_at timestamp with time zone,
    idle_from timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: api_request_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.api_request_daily (
    day date NOT NULL,
    route text NOT NULL,
    status smallint NOT NULL,
    calls bigint DEFAULT 0 NOT NULL
);


--
-- Name: api_self_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.api_self_daily (
    day date NOT NULL,
    route text NOT NULL,
    status smallint NOT NULL,
    calls bigint DEFAULT 0 NOT NULL
);


--
-- Name: ask_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.ask_daily (
    day date NOT NULL,
    count integer DEFAULT 0 NOT NULL
);


--
-- Name: auth_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.auth_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email text NOT NULL,
    code text NOT NULL,
    link_token text NOT NULL,
    purpose text DEFAULT 'dashboard'::text NOT NULL,
    site_id uuid,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    attempts integer DEFAULT 0,
    created_at timestamp with time zone DEFAULT now(),
    nonce_hash text,
    CONSTRAINT auth_tokens_purpose_check CHECK (((purpose = ANY (ARRAY['dashboard'::text, 'visitor'::text])) AND ((purpose = 'visitor'::text) = (site_id IS NOT NULL))))
);


--
-- Name: collection_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.collection_items (
    id bigint NOT NULL,
    site_id uuid NOT NULL,
    collection text NOT NULL,
    data jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now(),
    submitted_by uuid,
    deleted_at timestamp with time zone,
    submitted_email text,
    version bigint DEFAULT 1 NOT NULL
);


--
-- Name: collection_items_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.collection_items_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: collection_items_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.collection_items_id_seq OWNED BY public.collection_items.id;


--
-- Name: collection_settings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.collection_settings (
    site_id uuid NOT NULL,
    collection text NOT NULL,
    private boolean DEFAULT false NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    kind text,
    one_per_person boolean DEFAULT false NOT NULL,
    notify text DEFAULT 'off'::text NOT NULL,
    notify_sent_at timestamp with time zone,
    declared_at timestamp with time zone,
    notify_last_id bigint
);


--
-- Name: data_history; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.data_history (
    id bigint NOT NULL,
    site_id uuid NOT NULL,
    kind text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    item_id bigint,
    op text NOT NULL,
    prev jsonb,
    actor_id uuid,
    actor_kind text NOT NULL,
    actor_email text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    diff jsonb,
    actor_app text DEFAULT ''::text NOT NULL,
    CONSTRAINT data_history_kind_check CHECK ((kind = ANY (ARRAY['state'::text, 'list'::text, 'storage'::text])))
);


--
-- Name: data_history_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.data_history_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: data_history_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.data_history_id_seq OWNED BY public.data_history.id;


--
-- Name: data_watch; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.data_watch (
    day date NOT NULL,
    site_id uuid NOT NULL,
    metric text NOT NULL,
    count bigint DEFAULT 0 NOT NULL,
    last_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: domain_cert_requests; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.domain_cert_requests (
    user_id uuid NOT NULL,
    domain text NOT NULL,
    requested_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: email_change_undos; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.email_change_undos (
    token_hash text NOT NULL,
    user_id uuid NOT NULL,
    old_email text NOT NULL,
    new_email text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone
);


--
-- Name: email_changes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.email_changes (
    user_id uuid NOT NULL,
    new_email text NOT NULL,
    code_hash text NOT NULL,
    old_code_hash text,
    attempts integer DEFAULT 0 NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event_announcement_deliveries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_announcement_deliveries (
    announcement_id uuid NOT NULL,
    user_id uuid NOT NULL,
    recipient text NOT NULL,
    sent_at timestamp with time zone,
    attempted_at timestamp with time zone
);


--
-- Name: event_announcements; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_announcements (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    event_id uuid NOT NULL,
    title text NOT NULL,
    body text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    pinned_at timestamp with time zone
);


--
-- Name: event_assignments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_assignments (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    event_id uuid NOT NULL,
    judge_id uuid NOT NULL,
    team_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event_checklist_acknowledgements; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_checklist_acknowledgements (
    event_id uuid NOT NULL,
    subject_id text NOT NULL,
    item_id text NOT NULL,
    version text DEFAULT ''::text NOT NULL,
    acknowledged_by uuid,
    acknowledged_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event_checklist_dismissals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_checklist_dismissals (
    event_id uuid NOT NULL,
    item_id text NOT NULL,
    marked_by uuid,
    marked_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event_conflicts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_conflicts (
    event_id uuid NOT NULL,
    judge_id uuid NOT NULL,
    team_id uuid NOT NULL,
    declared_by text DEFAULT 'judge'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event_content; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_content (
    event_id uuid NOT NULL,
    sponsors jsonb DEFAULT '[]'::jsonb NOT NULL,
    faq jsonb DEFAULT '[]'::jsonb NOT NULL,
    schedule jsonb DEFAULT '[]'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event_create_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_create_log (
    user_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event_domains; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_domains (
    name text NOT NULL,
    domain text NOT NULL,
    user_id uuid NOT NULL,
    ip text NOT NULL,
    record_ids text[] DEFAULT '{}'::text[] NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL
);


--
-- Name: event_entries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_entries (
    team_id uuid NOT NULL,
    event_id uuid NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    tagline text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    video_url text DEFAULT ''::text NOT NULL,
    code_url text DEFAULT ''::text NOT NULL,
    screenshot bytea,
    screenshot_type text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_by uuid,
    problem text DEFAULT ''::text NOT NULL,
    audience text DEFAULT ''::text NOT NULL,
    tools text[] DEFAULT '{}'::text[] NOT NULL,
    maturity text DEFAULT ''::text NOT NULL,
    submitted_at timestamp with time zone,
    submitted_by uuid,
    submit_auto boolean DEFAULT false NOT NULL,
    submit_settled_at timestamp with time zone,
    excluded_at timestamp with time zone,
    excluded_by uuid,
    CONSTRAINT event_entries_maturity_check CHECK ((maturity = ANY (ARRAY[''::text, 'working'::text, 'partial'::text, 'concept'::text])))
);


--
-- Name: event_entry_receipts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_entry_receipts (
    event_id uuid NOT NULL,
    team_id uuid NOT NULL,
    user_id uuid NOT NULL,
    recipient text NOT NULL,
    sent_at timestamp with time zone,
    attempted_at timestamp with time zone
);


--
-- Name: event_entry_reminders; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_entry_reminders (
    event_id uuid NOT NULL,
    team_id uuid NOT NULL,
    kind text NOT NULL,
    deadline timestamp with time zone NOT NULL,
    sent_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT event_entry_reminders_kind_check CHECK ((kind ~ '^[0-9]+[hm]$'::text))
);


--
-- Name: event_former_judges; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_former_judges (
    event_id uuid NOT NULL,
    user_id uuid NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    role text NOT NULL,
    counted_team_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    left_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT event_former_judges_role_check CHECK ((role = ANY (ARRAY['judge'::text, 'organiser'::text])))
);


--
-- Name: event_judge_reminders; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_judge_reminders (
    event_id uuid NOT NULL,
    user_id uuid NOT NULL,
    kind text NOT NULL,
    deadline timestamp with time zone NOT NULL,
    sent_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT event_judge_reminders_kind_check CHECK ((kind ~ '^[0-9]+[hm]$'::text))
);


--
-- Name: event_members; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_members (
    event_id uuid NOT NULL,
    user_id uuid NOT NULL,
    role text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    team_id uuid,
    coc_accepted_at timestamp with time zone,
    joined_at timestamp with time zone DEFAULT now() NOT NULL,
    signup_answers jsonb DEFAULT '[]'::jsonb NOT NULL,
    approval_status text DEFAULT 'approved'::text NOT NULL,
    approval_decided_at timestamp with time zone,
    approval_decided_by uuid,
    team_removal_notice text DEFAULT ''::text NOT NULL,
    looking_intro text DEFAULT ''::text NOT NULL,
    looking boolean DEFAULT false NOT NULL,
    joined_via text DEFAULT 'link'::text NOT NULL,
    city text DEFAULT ''::text NOT NULL,
    country text DEFAULT ''::text NOT NULL,
    age_range text DEFAULT ''::text NOT NULL,
    gender text DEFAULT ''::text NOT NULL,
    team_admin boolean DEFAULT false NOT NULL,
    team_joined_at timestamp with time zone,
    CONSTRAINT event_members_age_range_chk CHECK ((age_range = ANY (ARRAY[''::text, 'under_18'::text, '18_24'::text, '25_34'::text, '35_plus'::text]))),
    CONSTRAINT event_members_check CHECK (((team_id IS NULL) OR (role = 'participant'::text))),
    CONSTRAINT event_members_country_chk CHECK (((country = ''::text) OR (country ~ '^[A-Z]{2}$'::text))),
    CONSTRAINT event_members_gender_chk CHECK ((gender = ANY (ARRAY[''::text, 'woman'::text, 'man'::text, 'non_binary'::text, 'another'::text, 'prefer_not_to_say'::text]))),
    CONSTRAINT event_members_joined_via_chk CHECK ((joined_via = ANY (ARRAY['link'::text, 'application'::text]))),
    CONSTRAINT event_members_role_check CHECK ((role = ANY (ARRAY['organiser'::text, 'participant'::text, 'judge'::text]))),
    CONSTRAINT event_members_team_admin_chk CHECK (((NOT team_admin) OR (team_id IS NOT NULL)))
);


--
-- Name: event_notices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_notices (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    event_id uuid NOT NULL,
    user_id uuid NOT NULL,
    kind text NOT NULL,
    team_id uuid,
    ref_id uuid,
    detail text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    seen_at timestamp with time zone,
    email_state text DEFAULT 'none'::text NOT NULL,
    emailed_at timestamp with time zone,
    attempted_at timestamp with time zone,
    CONSTRAINT event_notices_email_state_check CHECK ((email_state = ANY (ARRAY['none'::text, 'pending'::text, 'sent'::text, 'skipped'::text]))),
    CONSTRAINT event_notices_kind_check CHECK ((kind = ANY (ARRAY['join_request'::text, 'invite'::text, 'request_answered'::text, 'application_new'::text, 'application_decided'::text, 'site_taken_down'::text, 'site_restored'::text, 'deadline_changed'::text, 'team_admin'::text, 'team_locked'::text, 'team_removed'::text, 'entry_reminder'::text, 'judging_open'::text, 'judging_reminder'::text, 'team_message'::text, 'team_inbound'::text])))
);


--
-- Name: event_organiser_invites; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_organiser_invites (
    event_id uuid NOT NULL,
    token_hash text NOT NULL,
    created_by uuid NOT NULL,
    expires_at timestamp with time zone NOT NULL
);


--
-- Name: event_results; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_results (
    event_id uuid NOT NULL,
    published_at timestamp with time zone DEFAULT now() NOT NULL,
    published_by uuid,
    full_ranking boolean DEFAULT false NOT NULL,
    snapshot jsonb NOT NULL
);


--
-- Name: event_scores; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_scores (
    event_id uuid NOT NULL,
    judge_id uuid NOT NULL,
    team_id uuid NOT NULL,
    criterion_id uuid NOT NULL,
    points integer NOT NULL,
    comment text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event_team_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_team_keys (
    key_id uuid NOT NULL,
    event_id uuid NOT NULL,
    team_id uuid NOT NULL,
    user_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event_team_messages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_team_messages (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    event_id uuid NOT NULL,
    team_id uuid NOT NULL,
    sender_id uuid,
    sender_name text DEFAULT ''::text NOT NULL,
    body text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    from_team boolean DEFAULT false NOT NULL
);


--
-- Name: event_team_requests; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_team_requests (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    event_id uuid NOT NULL,
    team_id uuid NOT NULL,
    user_id uuid NOT NULL,
    kind text NOT NULL,
    created_by uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT event_team_requests_kind_check CHECK ((kind = ANY (ARRAY['request'::text, 'invite'::text])))
);


--
-- Name: event_teams; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_teams (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    event_id uuid NOT NULL,
    slug text NOT NULL,
    name text NOT NULL,
    code text NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deadline_override timestamp with time zone,
    pinned_version integer,
    pinned_at timestamp with time zone,
    site_taken_down_at timestamp with time zone,
    site_taken_down_reason text DEFAULT ''::text NOT NULL,
    track_id uuid,
    open boolean DEFAULT false NOT NULL,
    looking_for text DEFAULT ''::text NOT NULL,
    kept_after_leave boolean DEFAULT false NOT NULL,
    locked_at timestamp with time zone,
    locked_by uuid
);


--
-- Name: COLUMN event_teams.open; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.event_teams.open IS 'Deprecated 2026-10-31, unused. Drop after the teams redesign settles.';


--
-- Name: event_track_panels; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_track_panels (
    event_id uuid NOT NULL,
    track_id uuid NOT NULL,
    judge_id uuid NOT NULL
);


--
-- Name: event_tracks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_tracks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    event_id uuid NOT NULL,
    slug text NOT NULL,
    name text NOT NULL,
    challenge text DEFAULT ''::text NOT NULL,
    prize text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event_used_names; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_used_names (
    event_slug text NOT NULL,
    team_slug text DEFAULT ''::text NOT NULL,
    used_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: event_votes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.event_votes (
    event_id uuid NOT NULL,
    voter_email text NOT NULL,
    team_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.events (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug text NOT NULL,
    account_id uuid NOT NULL,
    created_by uuid,
    title text NOT NULL,
    stage text DEFAULT 'draft'::text NOT NULL,
    organiser_name text NOT NULL,
    organisation text DEFAULT ''::text NOT NULL,
    contact_email text NOT NULL,
    purpose text DEFAULT ''::text NOT NULL,
    expected_participants integer,
    tagline text DEFAULT ''::text NOT NULL,
    about text DEFAULT ''::text NOT NULL,
    rules text DEFAULT ''::text NOT NULL,
    prizes text DEFAULT ''::text NOT NULL,
    coc_text text DEFAULT ''::text NOT NULL,
    time_zone text DEFAULT 'UTC'::text NOT NULL,
    starts_at timestamp with time zone,
    ends_at timestamp with time zone,
    team_size_max integer DEFAULT 20 NOT NULL,
    join_code text NOT NULL,
    judge_code text NOT NULL,
    submission_deadline timestamp with time zone,
    results_visibility text DEFAULT 'winners'::text NOT NULL,
    results_published_at timestamp with time zone,
    closed_at timestamp with time zone,
    removal_warned_at timestamp with time zone,
    sites_removed_at timestamp with time zone,
    keep_sites boolean DEFAULT false NOT NULL,
    taken_down_at timestamp with time zone,
    taken_down_reason text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    entry_required text[] DEFAULT '{title}'::text[] NOT NULL,
    gallery_open boolean DEFAULT false NOT NULL,
    judge_assignment_mode text DEFAULT 'open'::text NOT NULL,
    judges_per_team integer DEFAULT 2 NOT NULL,
    judging_locked_at timestamp with time zone,
    judging_lock_reason text DEFAULT ''::text NOT NULL,
    signup_questions jsonb DEFAULT '[]'::jsonb NOT NULL,
    approval_required boolean DEFAULT false NOT NULL,
    voting_enabled boolean DEFAULT false NOT NULL,
    voting_opens_at timestamp with time zone,
    voting_closes_at timestamp with time zone,
    voting_eligibility text DEFAULT 'all_signed_in'::text NOT NULL,
    directory_listed boolean DEFAULT false NOT NULL,
    score_mode text DEFAULT 'raw'::text NOT NULL,
    tie_criterion_id uuid,
    public_scores boolean DEFAULT true NOT NULL,
    public_ranks boolean DEFAULT true NOT NULL,
    icon_media_type text DEFAULT ''::text NOT NULL,
    icon_bytes bytea,
    website_mode text DEFAULT 'builtin'::text NOT NULL,
    accent_color text DEFAULT ''::text NOT NULL,
    team_finding boolean DEFAULT true NOT NULL,
    applications_open boolean DEFAULT true NOT NULL,
    entry_preset text DEFAULT 'custom'::text NOT NULL,
    entry_rules jsonb DEFAULT '{}'::jsonb NOT NULL,
    judge_first text DEFAULT 'video'::text NOT NULL,
    reminder_offsets integer[] DEFAULT '{1440,60}'::integer[] NOT NULL,
    judging_ends_at timestamp with time zone,
    entry_rules_chosen boolean DEFAULT true NOT NULL,
    theme text DEFAULT ''::text NOT NULL,
    organiser_judges uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    overall_prizes text[] DEFAULT '{}'::text[] NOT NULL,
    submissions_public boolean DEFAULT false NOT NULL,
    CONSTRAINT events_entry_preset_check CHECK ((entry_preset = ANY (ARRAY['build'::text, 'code'::text, 'ideas'::text, 'custom'::text, 'video_or_website'::text, 'video_website_code'::text]))),
    CONSTRAINT events_expected_participants_check CHECK (((expected_participants IS NULL) OR (expected_participants >= 0))),
    CONSTRAINT events_judge_first_check CHECK ((judge_first = ANY (ARRAY['video'::text, 'site'::text]))),
    CONSTRAINT events_results_visibility_check CHECK ((results_visibility = ANY (ARRAY['winners'::text, 'ranking'::text]))),
    CONSTRAINT events_stage_check CHECK ((stage = ANY (ARRAY['draft'::text, 'open'::text, 'building'::text, 'closed'::text, 'judging'::text, 'results'::text, 'archived'::text]))),
    CONSTRAINT events_team_size_max_check CHECK (((team_size_max >= 1) AND (team_size_max <= 50))),
    CONSTRAINT events_theme_check CHECK ((theme = ANY (ARRAY[''::text, 'poster'::text, 'confetti'::text, 'boardroom'::text, 'garden'::text, 'terminal'::text]))),
    CONSTRAINT events_website_mode_check CHECK ((website_mode = ANY (ARRAY['builtin'::text, 'custom'::text])))
);


--
-- Name: family_cert_requests; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.family_cert_requests (
    user_id uuid NOT NULL,
    host text NOT NULL,
    requested_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: hack_account_preferences; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hack_account_preferences (
    user_id uuid NOT NULL,
    theme text DEFAULT 'system'::text NOT NULL,
    organiser_walkthrough_done boolean DEFAULT false NOT NULL,
    judge_walkthrough_done boolean DEFAULT false NOT NULL,
    email_updates boolean DEFAULT true NOT NULL,
    CONSTRAINT hack_account_preferences_theme_check CHECK ((theme = ANY (ARRAY['system'::text, 'light'::text, 'dark'::text])))
);


--
-- Name: hack_assistant_spend; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hack_assistant_spend (
    month date NOT NULL,
    cost_micro_usd bigint DEFAULT 0 NOT NULL
);


--
-- Name: hack_assistant_usage; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.hack_assistant_usage (
    user_id uuid NOT NULL,
    day date NOT NULL,
    questions integer DEFAULT 0 NOT NULL
);


--
-- Name: handle_aliases; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.handle_aliases (
    handle text NOT NULL,
    user_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: idempotency_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.idempotency_keys (
    scope bytea NOT NULL,
    status integer DEFAULT 0 NOT NULL,
    etag text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    site_id uuid,
    ref bigint,
    body_hash bytea
);


--
-- Name: image_scan_hashes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.image_scan_hashes (
    sha256 text NOT NULL,
    verdict text NOT NULL,
    provider text DEFAULT ''::text NOT NULL,
    tracking_id text DEFAULT ''::text NOT NULL,
    detail text DEFAULT ''::text NOT NULL,
    checked_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT image_scan_hashes_verdict_check CHECK ((verdict = ANY (ARRAY['clean'::text, 'match'::text, 'skipped'::text])))
);


--
-- Name: image_scan_matches; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.image_scan_matches (
    id bigint NOT NULL,
    kind text NOT NULL,
    site_id uuid,
    site_name text DEFAULT ''::text NOT NULL,
    owner_id uuid,
    owner_handle text DEFAULT ''::text NOT NULL,
    owner_email text DEFAULT ''::text NOT NULL,
    event_id uuid,
    event_slug text DEFAULT ''::text NOT NULL,
    team_id uuid,
    version integer,
    resource_name text DEFAULT ''::text NOT NULL,
    path text DEFAULT ''::text NOT NULL,
    writer_id text DEFAULT ''::text NOT NULL,
    sha256 text NOT NULL,
    bytes bigint DEFAULT 0 NOT NULL,
    content_type text DEFAULT ''::text NOT NULL,
    provider text DEFAULT ''::text NOT NULL,
    tracking_id text DEFAULT ''::text NOT NULL,
    source text DEFAULT ''::text NOT NULL,
    violations text DEFAULT ''::text NOT NULL,
    raw jsonb,
    evidence_path text DEFAULT ''::text NOT NULL,
    uploaded_at timestamp with time zone,
    detected_at timestamp with time zone DEFAULT now() NOT NULL,
    taken_down boolean DEFAULT false NOT NULL,
    alerted_at timestamp with time zone,
    report_status text DEFAULT 'pending'::text NOT NULL,
    report_id text DEFAULT ''::text NOT NULL,
    reported_at timestamp with time zone,
    report_error text DEFAULT ''::text NOT NULL,
    uploader text DEFAULT ''::text NOT NULL,
    address text DEFAULT ''::text NOT NULL,
    CONSTRAINT image_scan_matches_report_status_check CHECK ((report_status = ANY (ARRAY['pending'::text, 'filed'::text, 'failed'::text, 'off'::text])))
);


--
-- Name: image_scan_matches_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.image_scan_matches_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: image_scan_matches_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.image_scan_matches_id_seq OWNED BY public.image_scan_matches.id;


--
-- Name: image_scan_queue; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.image_scan_queue (
    id bigint NOT NULL,
    kind text NOT NULL,
    site_id uuid,
    event_id uuid,
    team_id uuid,
    version integer,
    resource_name text DEFAULT ''::text NOT NULL,
    path text DEFAULT ''::text NOT NULL,
    writer_id text DEFAULT ''::text NOT NULL,
    sha256 text NOT NULL,
    bytes bigint DEFAULT 0 NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT image_scan_queue_kind_check CHECK ((kind = ANY (ARRAY['site_file'::text, 'storage_file'::text, 'event_icon'::text, 'entry_screenshot'::text])))
);


--
-- Name: image_scan_queue_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.image_scan_queue_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: image_scan_queue_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.image_scan_queue_id_seq OWNED BY public.image_scan_queue.id;


--
-- Name: instance_config; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.instance_config (
    key text NOT NULL,
    value text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: ip_country_ranges; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.ip_country_ranges (
    start_ip inet NOT NULL,
    end_ip inet NOT NULL,
    country text NOT NULL
);


--
-- Name: legacy_hostnames; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.legacy_hostnames (
    hostname text NOT NULL,
    site_id uuid,
    user_id uuid,
    created_at timestamp with time zone DEFAULT now()
);


--
-- Name: net_counter_state; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.net_counter_state (
    iface text NOT NULL,
    boot_id text NOT NULL,
    rx_bytes bigint NOT NULL,
    tx_bytes bigint NOT NULL,
    counting_since timestamp with time zone NOT NULL,
    sampled_at timestamp with time zone NOT NULL
);


--
-- Name: net_usage_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.net_usage_daily (
    day date NOT NULL,
    iface text NOT NULL,
    rx_bytes bigint DEFAULT 0 NOT NULL,
    tx_bytes bigint DEFAULT 0 NOT NULL
);


--
-- Name: oauth_clients; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_clients (
    client_id text NOT NULL,
    client_secret_hash text,
    client_name text NOT NULL,
    redirect_uris text[] NOT NULL,
    token_endpoint_auth_method text DEFAULT 'none'::text NOT NULL,
    pkce_required boolean DEFAULT true NOT NULL,
    dynamic boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_used_at timestamp with time zone,
    CONSTRAINT oauth_clients_auth_method_check CHECK ((token_endpoint_auth_method = ANY (ARRAY['none'::text, 'client_secret_post'::text, 'client_secret_basic'::text]))),
    CONSTRAINT oauth_clients_pkce_check CHECK ((pkce_required OR ((client_secret_hash IS NOT NULL) AND (NOT dynamic)))),
    CONSTRAINT oauth_clients_secret_shape CHECK (((token_endpoint_auth_method = 'none'::text) = (client_secret_hash IS NULL)))
);


--
-- Name: oauth_codes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_codes (
    code_hash text NOT NULL,
    client_id text NOT NULL,
    user_id uuid NOT NULL,
    redirect_uri text NOT NULL,
    code_challenge text NOT NULL,
    scope text NOT NULL,
    resource text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    grant_id uuid,
    device text
);


--
-- Name: oauth_grants; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_grants (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    client_id text NOT NULL,
    scope text NOT NULL,
    resource text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_used_at timestamp with time zone DEFAULT now() NOT NULL,
    device text,
    selected_team_id uuid,
    last_visitor_site jsonb
);


--
-- Name: oauth_identities; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_identities (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    provider text NOT NULL,
    provider_user_id text NOT NULL,
    email text,
    email_verified boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT oauth_identities_provider_check CHECK ((provider = ANY (ARRAY['google'::text, 'github'::text, 'oidc'::text])))
);


--
-- Name: oauth_states; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_states (
    state text NOT NULL,
    provider text NOT NULL,
    code_verifier text NOT NULL,
    return_to text NOT NULL,
    host text NOT NULL,
    site_id uuid,
    purpose text DEFAULT 'site'::text NOT NULL,
    nonce_hash text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    CONSTRAINT oauth_states_provider_check CHECK ((provider = ANY (ARRAY['google'::text, 'github'::text, 'oidc'::text]))),
    CONSTRAINT oauth_states_purpose_check CHECK ((purpose = ANY (ARRAY['site'::text, 'owner'::text]))),
    CONSTRAINT oauth_states_purpose_shape CHECK ((((purpose = 'site'::text) AND (site_id IS NOT NULL) AND (host <> ''::text)) OR ((purpose = 'owner'::text) AND (site_id IS NULL))))
);


--
-- Name: oauth_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_tokens (
    token_hash text NOT NULL,
    grant_id uuid NOT NULL,
    kind text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    CONSTRAINT oauth_tokens_kind_check CHECK ((kind = ANY (ARRAY['access'::text, 'refresh'::text])))
);


--
-- Name: owner_visitor_marks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.owner_visitor_marks (
    user_id uuid NOT NULL,
    ip_hash bytea NOT NULL,
    last_seen timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: paper_class_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_class_daily (
    day date NOT NULL,
    class text NOT NULL,
    requests bigint DEFAULT 0 NOT NULL,
    server_errors bigint DEFAULT 0 NOT NULL,
    total_us bigint DEFAULT 0 NOT NULL,
    bytes_out bigint DEFAULT 0 NOT NULL
);


--
-- Name: paper_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_daily (
    day date NOT NULL,
    metric text NOT NULL,
    value double precision DEFAULT 0 NOT NULL
);


--
-- Name: paper_hack_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_hack_daily (
    run_id bigint NOT NULL,
    day date NOT NULL,
    metric text NOT NULL,
    value double precision NOT NULL
);


--
-- Name: paper_hack_event; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_hack_event (
    snapshot_day date NOT NULL,
    event_key bigint NOT NULL,
    run_id bigint NOT NULL,
    synthetic boolean NOT NULL,
    stage text NOT NULL,
    created_week date NOT NULL,
    duration_hours double precision,
    expected_participants integer,
    participants bigint,
    organisers bigint,
    judges bigint,
    applications bigint,
    applications_approved bigint,
    teams bigint,
    teams_with_entry bigint,
    teams_kept_after_leave bigint,
    team_size_p50 bigint,
    team_size_max bigint,
    team_sites bigint,
    team_site_bytes_sum bigint,
    team_site_bytes_p50 bigint,
    team_site_versions_sum bigint,
    event_storage_bytes bigint,
    submissions bigint,
    entries_with_code_url bigint,
    entries_with_video bigint,
    scores bigint,
    score_criteria bigint,
    assignments bigint,
    conflicts bigint,
    votes bigint,
    announcements bigint,
    results_published boolean,
    deploys_total bigint,
    minutes_create_to_first_deploy bigint,
    CONSTRAINT paper_hack_event_stage_check CHECK ((stage = ANY (ARRAY['draft'::text, 'open'::text, 'building'::text, 'closed'::text, 'judging'::text, 'results'::text, 'archived'::text])))
);


--
-- Name: paper_hack_key; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_hack_key (
    event_id uuid NOT NULL,
    event_key bigint NOT NULL,
    synthetic boolean NOT NULL,
    run_id bigint NOT NULL,
    first_open_at timestamp with time zone
);


--
-- Name: paper_hack_latency; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_hack_latency (
    run_id bigint NOT NULL,
    slot timestamp with time zone NOT NULL,
    event_key bigint NOT NULL,
    synthetic boolean NOT NULL,
    class text NOT NULL,
    status_group text NOT NULL,
    n bigint NOT NULL,
    sum_us bigint NOT NULL,
    max_us bigint NOT NULL,
    bytes_out bigint NOT NULL,
    idx_offset smallint NOT NULL,
    counts bigint[] NOT NULL,
    arrivals_1s integer[] NOT NULL,
    writes_1s integer[] NOT NULL,
    CONSTRAINT paper_hack_latency_arrivals_1s_check CHECK ((cardinality(arrivals_1s) = 60)),
    CONSTRAINT paper_hack_latency_class_check CHECK ((class = ANY (ARRAY['site_file'::text, 'data_read'::text, 'data_write'::text, 'auth'::text, 'mcp'::text, 'api_read'::text, 'api_write'::text, 'platform_page'::text]))),
    CONSTRAINT paper_hack_latency_status_group_check CHECK ((status_group = ANY (ARRAY['2xx'::text, '3xx'::text, '4xx'::text, '5xx'::text]))),
    CONSTRAINT paper_hack_latency_writes_1s_check CHECK ((cardinality(writes_1s) = 60))
);


--
-- Name: paper_hack_minute; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_hack_minute (
    run_id bigint NOT NULL,
    slot timestamp with time zone NOT NULL,
    slot_secs smallint DEFAULT 60 NOT NULL,
    event_key bigint NOT NULL,
    synthetic boolean NOT NULL,
    requests bigint NOT NULL,
    writes bigint NOT NULL,
    data_writes bigint NOT NULL,
    deploys bigint NOT NULL,
    active_members integer NOT NULL,
    inflight_max bigint NOT NULL,
    peak_rps_1s bigint NOT NULL,
    arrivals_1s integer[] NOT NULL,
    writes_1s integer[] NOT NULL,
    CONSTRAINT paper_hack_minute_arrivals_1s_check CHECK ((cardinality(arrivals_1s) = 60)),
    CONSTRAINT paper_hack_minute_slot_secs_check CHECK ((slot_secs = 60)),
    CONSTRAINT paper_hack_minute_writes_1s_check CHECK ((cardinality(writes_1s) = 60))
);


--
-- Name: paper_hack_phase; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_hack_phase (
    run_id bigint NOT NULL,
    event_key bigint NOT NULL,
    synthetic boolean NOT NULL,
    stage text NOT NULL,
    phase_slot timestamp with time zone NOT NULL,
    minute_offset integer,
    origin_slot timestamp with time zone,
    observed boolean NOT NULL,
    baseline boolean DEFAULT false NOT NULL,
    recorded_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    origin_change timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT paper_hack_phase_stage_check CHECK ((stage = ANY (ARRAY['draft'::text, 'open'::text, 'building'::text, 'closed'::text, 'judging'::text, 'results'::text, 'archived'::text, 'submission_deadline'::text, 'judging_locked'::text, 'results_published'::text, 'voting_opens'::text, 'voting_closes'::text])))
);


--
-- Name: paper_latency_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_latency_daily (
    day date NOT NULL,
    class text NOT NULL,
    le_us bigint NOT NULL,
    requests bigint DEFAULT 0 NOT NULL
);


--
-- Name: paper_process_sample; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_process_sample (
    at timestamp with time zone NOT NULL,
    started_at timestamp with time zone NOT NULL,
    proc_cpu_cores double precision DEFAULT 0 NOT NULL,
    proc_rss_bytes bigint DEFAULT 0 NOT NULL,
    go_heap_bytes bigint DEFAULT 0 NOT NULL,
    goroutines integer DEFAULT 0 NOT NULL,
    open_fds integer DEFAULT 0 NOT NULL,
    pg_rss_bytes bigint DEFAULT 0 NOT NULL,
    nginx_rss_bytes bigint DEFAULT 0 NOT NULL,
    box_cpu_cores double precision DEFAULT 0 NOT NULL,
    box_cpus integer DEFAULT 0 NOT NULL,
    box_mem_total_bytes bigint DEFAULT 0 NOT NULL,
    box_mem_avail_bytes bigint DEFAULT 0 NOT NULL,
    load1 double precision DEFAULT 0 NOT NULL
);


--
-- Name: paper_run; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_run (
    run_id bigint NOT NULL,
    started_at timestamp with time zone NOT NULL,
    role text NOT NULL,
    vcs_revision text,
    vcs_modified boolean NOT NULL,
    go_version text NOT NULL,
    binary_sha256 text,
    config_hash text NOT NULL,
    metric_def_version integer NOT NULL,
    bucket_scheme text NOT NULL,
    gomaxprocs integer NOT NULL,
    num_cpu integer NOT NULL,
    cgroup_memory_max text,
    cgroup_cpu_max text,
    CONSTRAINT paper_run_role_check CHECK ((role = ANY (ARRAY['host'::text, 'hack'::text])))
);


--
-- Name: paper_run_run_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.paper_run_run_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: paper_run_run_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.paper_run_run_id_seq OWNED BY public.paper_run.run_id;


--
-- Name: paper_site_footprint; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.paper_site_footprint (
    week date NOT NULL,
    versions integer DEFAULT 0 NOT NULL,
    live_files integer DEFAULT 0 NOT NULL,
    live_bytes bigint DEFAULT 0 NOT NULL,
    total_bytes bigint DEFAULT 0 NOT NULL,
    html_bytes bigint DEFAULT 0 NOT NULL,
    js_bytes bigint DEFAULT 0 NOT NULL,
    css_bytes bigint DEFAULT 0 NOT NULL,
    media_bytes bigint DEFAULT 0 NOT NULL,
    other_bytes bigint DEFAULT 0 NOT NULL,
    sqlite_bytes bigint DEFAULT 0 NOT NULL,
    file_store_bytes bigint DEFAULT 0 NOT NULL,
    kv_bytes bigint DEFAULT 0 NOT NULL,
    kv_rows bigint DEFAULT 0 NOT NULL,
    legacy_rows bigint DEFAULT 0 NOT NULL,
    views_30d bigint DEFAULT 0 NOT NULL,
    age_days integer DEFAULT 0 NOT NULL,
    days_since_update integer DEFAULT 0 NOT NULL
);


--
-- Name: rubric_criteria; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.rubric_criteria (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    event_id uuid NOT NULL,
    "position" integer NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    weight integer NOT NULL,
    max_points integer DEFAULT 5 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: schema_migrations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.schema_migrations (
    name text NOT NULL,
    applied_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: setup_assist_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.setup_assist_daily (
    day date NOT NULL,
    count integer DEFAULT 0 NOT NULL
);


--
-- Name: setup_check_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.setup_check_daily (
    day date NOT NULL,
    count integer DEFAULT 0 NOT NULL
);


--
-- Name: signin_alerts_sent; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.signin_alerts_sent (
    user_id uuid NOT NULL,
    summary text NOT NULL,
    day date NOT NULL
);


--
-- Name: site_event_deliveries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_event_deliveries (
    event_seq bigint NOT NULL,
    subscription_id text NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    lease_until timestamp with time zone,
    last_attempt_at timestamp with time zone,
    last_http integer,
    last_category text DEFAULT ''::text NOT NULL,
    duration_ms integer
);


--
-- Name: site_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_events (
    seq bigint NOT NULL,
    event_id text NOT NULL,
    site_id uuid NOT NULL,
    name text NOT NULL,
    storage text NOT NULL,
    data jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    push_actor text DEFAULT ''::text NOT NULL,
    push_target text DEFAULT ''::text NOT NULL
);


--
-- Name: site_events_seq_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.site_events_seq_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: site_events_seq_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.site_events_seq_seq OWNED BY public.site_events.seq;


--
-- Name: site_geo_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_geo_daily (
    site_id uuid NOT NULL,
    day date NOT NULL,
    country text NOT NULL,
    class text NOT NULL,
    views bigint DEFAULT 0 NOT NULL,
    visitors bigint DEFAULT 0 NOT NULL
);


--
-- Name: site_name_aliases; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_name_aliases (
    user_id uuid NOT NULL,
    name text NOT NULL,
    site_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: site_notifications; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_notifications (
    id text NOT NULL,
    site_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    source text NOT NULL,
    grant_id uuid,
    app_name text DEFAULT ''::text NOT NULL,
    label text DEFAULT ''::text NOT NULL,
    url text NOT NULL,
    events text[] NOT NULL,
    storage text DEFAULT ''::text NOT NULL,
    args_canonical text DEFAULT ''::text NOT NULL,
    secret text NOT NULL,
    prev_secret text DEFAULT ''::text NOT NULL,
    prev_secret_until timestamp with time zone,
    auth_header_name text DEFAULT ''::text NOT NULL,
    auth_header_value text DEFAULT ''::text NOT NULL,
    state text DEFAULT 'active'::text NOT NULL,
    verified_at timestamp with time zone,
    expires_at timestamp with time zone,
    window_start timestamp with time zone,
    window_attempts integer DEFAULT 0 NOT NULL,
    window_failures integer DEFAULT 0 NOT NULL,
    failing_since timestamp with time zone,
    last_attempt_at timestamp with time zone,
    last_success_at timestamp with time zone,
    last_result text DEFAULT ''::text NOT NULL,
    suspended_at timestamp with time zone,
    suspend_emailed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    kind text DEFAULT 'webhook'::text NOT NULL,
    subscriber_role text DEFAULT 'owner'::text NOT NULL,
    push_p256dh text DEFAULT ''::text NOT NULL,
    push_auth text DEFAULT ''::text NOT NULL,
    device_label text DEFAULT ''::text NOT NULL,
    muted_resources text[] DEFAULT '{}'::text[] NOT NULL,
    push_family text DEFAULT ''::text NOT NULL,
    CONSTRAINT site_notifications_kind_check CHECK ((kind = ANY (ARRAY['webhook'::text, 'mcp-event'::text, 'web-push'::text]))),
    CONSTRAINT site_notifications_subscriber_role_check CHECK ((subscriber_role = ANY (ARRAY['owner'::text, 'visitor'::text])))
);


--
-- Name: site_page_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_page_daily (
    site_id uuid NOT NULL,
    day date NOT NULL,
    path text NOT NULL,
    views bigint DEFAULT 0 NOT NULL
);


--
-- Name: site_push_rules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_push_rules (
    site_id uuid NOT NULL,
    resource text DEFAULT ''::text NOT NULL,
    recipients text DEFAULT 'both'::text NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    body_column text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT site_push_rules_recipients_check CHECK ((recipients = ANY (ARRAY['owner'::text, 'row_owner'::text, 'both'::text, 'none'::text])))
);


--
-- Name: site_referrer_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_referrer_daily (
    site_id uuid NOT NULL,
    day date NOT NULL,
    domain text NOT NULL,
    views bigint DEFAULT 0 NOT NULL
);


--
-- Name: site_savers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_savers (
    site_id uuid NOT NULL,
    list text NOT NULL,
    pattern text NOT NULL,
    added_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT site_savers_list_check CHECK ((list = ANY (ARRAY['allow'::text, 'block'::text])))
);


--
-- Name: site_storage_files; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_storage_files (
    site_id uuid NOT NULL,
    resource_name text NOT NULL,
    path text NOT NULL,
    writer_id text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: site_storage_kv; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_storage_kv (
    site_id uuid NOT NULL,
    resource_name text NOT NULL,
    key text NOT NULL,
    value jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    writer_id text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: site_storage_resources; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_storage_resources (
    site_id uuid NOT NULL,
    name text NOT NULL,
    kind text NOT NULL,
    read_policy text DEFAULT 'owner'::text NOT NULL,
    write_policy text DEFAULT 'owner'::text NOT NULL,
    site_passcode text DEFAULT 'inherit'::text NOT NULL,
    write_mode text DEFAULT 'full'::text NOT NULL,
    acc_read text,
    acc_add text,
    acc_edit text,
    acc_delete text,
    preset_base text DEFAULT ''::text NOT NULL,
    approve_first boolean DEFAULT false NOT NULL,
    CONSTRAINT site_storage_resources_acc_add_check CHECK ((acc_add = ANY (ARRAY['nobody'::text, 'owner'::text, 'signed-in'::text, 'checked'::text, 'anyone'::text]))),
    CONSTRAINT site_storage_resources_acc_delete_check CHECK ((acc_delete = ANY (ARRAY['nobody'::text, 'owner'::text, 'own'::text, 'signed-in'::text]))),
    CONSTRAINT site_storage_resources_acc_edit_check CHECK ((acc_edit = ANY (ARRAY['nobody'::text, 'owner'::text, 'own'::text, 'signed-in'::text]))),
    CONSTRAINT site_storage_resources_acc_read_check CHECK ((acc_read = ANY (ARRAY['nobody'::text, 'owner'::text, 'own'::text, 'signed-in'::text, 'anyone'::text]))),
    CONSTRAINT site_storage_resources_kind_check CHECK ((kind = ANY (ARRAY['kv'::text, 'sqlite'::text, 'files'::text]))),
    CONSTRAINT site_storage_resources_read_policy_check CHECK ((read_policy = ANY (ARRAY['anyone'::text, 'signed-in'::text, 'owner'::text, 'own'::text]))),
    CONSTRAINT site_storage_resources_site_passcode_check CHECK ((site_passcode = ANY (ARRAY['inherit'::text, 'off'::text]))),
    CONSTRAINT site_storage_resources_write_mode_check CHECK ((write_mode = ANY (ARRAY['full'::text, 'add'::text]))),
    CONSTRAINT site_storage_resources_write_policy_check CHECK ((write_policy = ANY (ARRAY['anyone'::text, 'signed-in'::text, 'owner'::text])))
);


--
-- Name: site_storage_tables; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_storage_tables (
    site_id uuid NOT NULL,
    resource_name text NOT NULL,
    table_name text NOT NULL,
    acc_read text NOT NULL,
    acc_add text NOT NULL,
    acc_edit text NOT NULL,
    acc_delete text NOT NULL,
    preset_base text DEFAULT ''::text NOT NULL,
    CONSTRAINT site_storage_tables_acc_add_check CHECK ((acc_add = ANY (ARRAY['nobody'::text, 'owner'::text, 'signed-in'::text, 'checked'::text, 'anyone'::text]))),
    CONSTRAINT site_storage_tables_acc_delete_check CHECK ((acc_delete = ANY (ARRAY['nobody'::text, 'owner'::text, 'own'::text, 'signed-in'::text]))),
    CONSTRAINT site_storage_tables_acc_edit_check CHECK ((acc_edit = ANY (ARRAY['nobody'::text, 'owner'::text, 'own'::text, 'signed-in'::text]))),
    CONSTRAINT site_storage_tables_acc_read_check CHECK ((acc_read = ANY (ARRAY['nobody'::text, 'owner'::text, 'own'::text, 'signed-in'::text, 'anyone'::text])))
);


--
-- Name: site_traffic_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_traffic_daily (
    site_id uuid NOT NULL,
    day date NOT NULL,
    kind text NOT NULL,
    bytes bigint DEFAULT 0 NOT NULL,
    requests bigint DEFAULT 0 NOT NULL
);


--
-- Name: site_view_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_view_daily (
    site_id uuid NOT NULL,
    day date NOT NULL,
    views bigint DEFAULT 0 NOT NULL
);


--
-- Name: site_view_hourly; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_view_hourly (
    site_id uuid NOT NULL,
    hour timestamp with time zone NOT NULL,
    class text NOT NULL,
    views bigint DEFAULT 0 NOT NULL
);


--
-- Name: site_viewers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_viewers (
    site_id uuid NOT NULL,
    email text NOT NULL,
    added_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: site_visitor_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_visitor_daily (
    site_id uuid NOT NULL,
    day date NOT NULL,
    ip_hash bytea NOT NULL
);


--
-- Name: site_visitor_hourly; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_visitor_hourly (
    site_id uuid NOT NULL,
    hour timestamp with time zone NOT NULL,
    class text NOT NULL,
    ip_hash bytea NOT NULL,
    country text DEFAULT 'XX'::text NOT NULL
);


--
-- Name: site_visitor_signins; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.site_visitor_signins (
    site_id uuid NOT NULL,
    user_id uuid NOT NULL,
    first_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: sites; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sites (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid,
    name text NOT NULL,
    active_version integer DEFAULT 1 NOT NULL,
    site_url text,
    expires_at timestamp with time zone,
    allowed_origins text,
    custom_domain text,
    domain_status text,
    domain_verified_at timestamp with time zone,
    domain_bound_at timestamp with time zone,
    domain_last_error text,
    previous_domain text,
    domain_cert_status text,
    domain_failing_since timestamp with time zone,
    domain_lapse_notified_at timestamp with time zone,
    previous_domain_failing_since timestamp with time zone,
    domain_token text DEFAULT ('sh-'::text || replace((gen_random_uuid())::text, '-'::text, ''::text)),
    domain_proof_exempt text[] DEFAULT '{}'::text[] NOT NULL,
    state jsonb,
    state_version integer DEFAULT 0 NOT NULL,
    view_password_hash text,
    suspended_at timestamp with time zone,
    suspended_reason text,
    created_at timestamp with time zone DEFAULT now(),
    updated_at timestamp with time zone DEFAULT now(),
    visibility text DEFAULT 'unlisted'::text NOT NULL,
    deleted_at timestamp with time zone,
    offline_at timestamp with time zone,
    keep_versions integer DEFAULT 0 NOT NULL,
    passcode_enc bytea,
    passcode_set_at timestamp with time zone,
    passcode_generation integer DEFAULT 0 NOT NULL,
    idle_keep boolean DEFAULT false NOT NULL,
    idle_kept_at timestamp with time zone,
    idle_warned_at timestamp with time zone,
    idle_removed_at timestamp with time zone,
    idle_token_hash bytea,
    purge_at timestamp with time zone,
    idle_remove_at timestamp with time zone,
    domain_release_at timestamp with time zone,
    legacy_data boolean DEFAULT true NOT NULL,
    state_bytes bigint DEFAULT 0 NOT NULL,
    data_bytes bigint DEFAULT 0 NOT NULL,
    history_bytes bigint DEFAULT 0 NOT NULL,
    allow_anonymous_writes boolean DEFAULT false NOT NULL,
    savers_mode text DEFAULT 'anyone'::text NOT NULL,
    showcase_pinned boolean DEFAULT false NOT NULL,
    showcase_order integer DEFAULT 0 NOT NULL,
    access text DEFAULT 'anyone'::text NOT NULL,
    summary_title text,
    summary_description text,
    summary_headings text,
    summary_text text,
    CONSTRAINT sites_visibility_check CHECK ((visibility = ANY (ARRAY['public'::text, 'unlisted'::text])))
);


--
-- Name: sso_consents; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sso_consents (
    user_id uuid NOT NULL,
    site_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: sso_hops; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sso_hops (
    state text NOT NULL,
    site_id uuid NOT NULL,
    host text NOT NULL,
    return_to text NOT NULL,
    nonce_hash text NOT NULL,
    consent_hash text,
    consent_pass_hash text,
    expires_at timestamp with time zone NOT NULL
);


--
-- Name: sso_passes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sso_passes (
    id_hash text NOT NULL,
    user_id uuid NOT NULL,
    parent_key_hash text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    idle_expires_at timestamp with time zone NOT NULL
);


--
-- Name: sso_signouts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sso_signouts (
    token text NOT NULL,
    pass_hash text NOT NULL,
    return_to text NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    form_hash text,
    form_cookie_hash text
);


--
-- Name: traffic_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.traffic_daily (
    day date NOT NULL,
    kind text NOT NULL,
    bytes bigint DEFAULT 0 NOT NULL,
    requests bigint DEFAULT 0 NOT NULL
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    username text NOT NULL,
    is_admin boolean DEFAULT false,
    display_name text,
    handle text,
    handle_changed_at timestamp with time zone,
    suspended_at timestamp with time zone,
    suspended_reason text,
    signin_alerts boolean DEFAULT true NOT NULL,
    event_account boolean DEFAULT false NOT NULL,
    signup_source text,
    signup_agent text,
    signup_method text,
    signup_inferred boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now(),
    home_site_id uuid,
    showcase_bio text DEFAULT ''::text NOT NULL,
    signup_detail jsonb
);


--
-- Name: versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.versions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    site_id uuid,
    version_number integer NOT NULL,
    disk_path text NOT NULL,
    status text DEFAULT 'uploading'::text NOT NULL,
    archive_sha256 text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now(),
    author text DEFAULT ''::text NOT NULL
);


--
-- Name: visitor_ai_daily; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.visitor_ai_daily (
    user_id uuid NOT NULL,
    site_id uuid NOT NULL,
    day date NOT NULL,
    writes integer DEFAULT 0 NOT NULL
);


--
-- Name: visitor_establish_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.visitor_establish_tokens (
    once text NOT NULL,
    session_id bytea NOT NULL,
    host text NOT NULL,
    return_to text NOT NULL,
    nonce_hash text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone
);


--
-- Name: visitor_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.visitor_sessions (
    id bytea NOT NULL,
    user_id uuid NOT NULL,
    site_id uuid NOT NULL,
    host text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    idle_expires_at timestamp with time zone NOT NULL,
    sso_pass_hash text
);


--
-- Name: collection_items id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_items ALTER COLUMN id SET DEFAULT nextval('public.collection_items_id_seq'::regclass);


--
-- Name: data_history id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_history ALTER COLUMN id SET DEFAULT nextval('public.data_history_id_seq'::regclass);


--
-- Name: image_scan_matches id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_scan_matches ALTER COLUMN id SET DEFAULT nextval('public.image_scan_matches_id_seq'::regclass);


--
-- Name: image_scan_queue id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_scan_queue ALTER COLUMN id SET DEFAULT nextval('public.image_scan_queue_id_seq'::regclass);


--
-- Name: paper_run run_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_run ALTER COLUMN run_id SET DEFAULT nextval('public.paper_run_run_id_seq'::regclass);


--
-- Name: site_events seq; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_events ALTER COLUMN seq SET DEFAULT nextval('public.site_events_seq_seq'::regclass);


--
-- Name: account_passwords account_passwords_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_passwords
    ADD CONSTRAINT account_passwords_pkey PRIMARY KEY (user_id);


--
-- Name: address_families address_families_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.address_families
    ADD CONSTRAINT address_families_pkey PRIMARY KEY (id);


--
-- Name: analytics_ingest_state analytics_ingest_state_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.analytics_ingest_state
    ADD CONSTRAINT analytics_ingest_state_pkey PRIMARY KEY (logfile);


--
-- Name: api_growth_daily api_growth_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_growth_daily
    ADD CONSTRAINT api_growth_daily_pkey PRIMARY KEY (day, dim, key);


--
-- Name: api_ip_daily api_ip_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_ip_daily
    ADD CONSTRAINT api_ip_daily_pkey PRIMARY KEY (day, ip);


--
-- Name: api_keys api_keys_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_id_key UNIQUE (id);


--
-- Name: api_keys api_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_pkey PRIMARY KEY (key_hash);


--
-- Name: api_request_daily api_request_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_request_daily
    ADD CONSTRAINT api_request_daily_pkey PRIMARY KEY (day, route, status);


--
-- Name: api_self_daily api_self_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_self_daily
    ADD CONSTRAINT api_self_daily_pkey PRIMARY KEY (day, route, status);


--
-- Name: ask_daily ask_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ask_daily
    ADD CONSTRAINT ask_daily_pkey PRIMARY KEY (day);


--
-- Name: auth_tokens auth_tokens_link_token_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.auth_tokens
    ADD CONSTRAINT auth_tokens_link_token_key UNIQUE (link_token);


--
-- Name: auth_tokens auth_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.auth_tokens
    ADD CONSTRAINT auth_tokens_pkey PRIMARY KEY (id);


--
-- Name: collection_items collection_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_items
    ADD CONSTRAINT collection_items_pkey PRIMARY KEY (id);


--
-- Name: collection_settings collection_settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_settings
    ADD CONSTRAINT collection_settings_pkey PRIMARY KEY (site_id, collection);


--
-- Name: data_history data_history_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_history
    ADD CONSTRAINT data_history_pkey PRIMARY KEY (id);


--
-- Name: data_watch data_watch_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_watch
    ADD CONSTRAINT data_watch_pkey PRIMARY KEY (day, site_id, metric);


--
-- Name: email_change_undos email_change_undos_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.email_change_undos
    ADD CONSTRAINT email_change_undos_pkey PRIMARY KEY (token_hash);


--
-- Name: email_changes email_changes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.email_changes
    ADD CONSTRAINT email_changes_pkey PRIMARY KEY (user_id);


--
-- Name: event_announcement_deliveries event_announcement_deliveries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_announcement_deliveries
    ADD CONSTRAINT event_announcement_deliveries_pkey PRIMARY KEY (announcement_id, user_id);


--
-- Name: event_announcements event_announcements_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_announcements
    ADD CONSTRAINT event_announcements_pkey PRIMARY KEY (id);


--
-- Name: event_assignments event_assignments_event_id_judge_id_team_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_assignments
    ADD CONSTRAINT event_assignments_event_id_judge_id_team_id_key UNIQUE (event_id, judge_id, team_id);


--
-- Name: event_assignments event_assignments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_assignments
    ADD CONSTRAINT event_assignments_pkey PRIMARY KEY (id);


--
-- Name: event_checklist_acknowledgements event_checklist_acknowledgements_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_checklist_acknowledgements
    ADD CONSTRAINT event_checklist_acknowledgements_pkey PRIMARY KEY (event_id, subject_id, item_id);


--
-- Name: event_checklist_dismissals event_checklist_dismissals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_checklist_dismissals
    ADD CONSTRAINT event_checklist_dismissals_pkey PRIMARY KEY (event_id, item_id);


--
-- Name: event_conflicts event_conflicts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_conflicts
    ADD CONSTRAINT event_conflicts_pkey PRIMARY KEY (event_id, judge_id, team_id);


--
-- Name: event_content event_content_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_content
    ADD CONSTRAINT event_content_pkey PRIMARY KEY (event_id);


--
-- Name: event_domains event_domains_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_domains
    ADD CONSTRAINT event_domains_pkey PRIMARY KEY (name, domain);


--
-- Name: event_entries event_entries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entries
    ADD CONSTRAINT event_entries_pkey PRIMARY KEY (team_id);


--
-- Name: event_entry_receipts event_entry_receipts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entry_receipts
    ADD CONSTRAINT event_entry_receipts_pkey PRIMARY KEY (event_id, team_id);


--
-- Name: event_entry_reminders event_entry_reminders_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entry_reminders
    ADD CONSTRAINT event_entry_reminders_pkey PRIMARY KEY (event_id, team_id, kind);


--
-- Name: event_former_judges event_former_judges_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_former_judges
    ADD CONSTRAINT event_former_judges_pkey PRIMARY KEY (event_id, user_id);


--
-- Name: event_judge_reminders event_judge_reminders_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_judge_reminders
    ADD CONSTRAINT event_judge_reminders_pkey PRIMARY KEY (event_id, user_id, kind);


--
-- Name: event_members event_members_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_members
    ADD CONSTRAINT event_members_pkey PRIMARY KEY (event_id, user_id);


--
-- Name: event_notices event_notices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_notices
    ADD CONSTRAINT event_notices_pkey PRIMARY KEY (id);


--
-- Name: event_organiser_invites event_organiser_invites_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_organiser_invites
    ADD CONSTRAINT event_organiser_invites_pkey PRIMARY KEY (event_id);


--
-- Name: event_organiser_invites event_organiser_invites_token_hash_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_organiser_invites
    ADD CONSTRAINT event_organiser_invites_token_hash_key UNIQUE (token_hash);


--
-- Name: event_results event_results_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_results
    ADD CONSTRAINT event_results_pkey PRIMARY KEY (event_id);


--
-- Name: event_scores event_scores_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_scores
    ADD CONSTRAINT event_scores_pkey PRIMARY KEY (judge_id, team_id, criterion_id);


--
-- Name: event_team_keys event_team_keys_event_id_user_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_keys
    ADD CONSTRAINT event_team_keys_event_id_user_id_key UNIQUE (event_id, user_id);


--
-- Name: event_team_keys event_team_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_keys
    ADD CONSTRAINT event_team_keys_pkey PRIMARY KEY (key_id);


--
-- Name: event_team_messages event_team_messages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_messages
    ADD CONSTRAINT event_team_messages_pkey PRIMARY KEY (id);


--
-- Name: event_team_requests event_team_requests_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_requests
    ADD CONSTRAINT event_team_requests_pkey PRIMARY KEY (id);


--
-- Name: event_team_requests event_team_requests_team_id_user_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_requests
    ADD CONSTRAINT event_team_requests_team_id_user_id_key UNIQUE (team_id, user_id);


--
-- Name: event_teams event_teams_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_teams
    ADD CONSTRAINT event_teams_code_key UNIQUE (code);


--
-- Name: event_teams event_teams_event_id_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_teams
    ADD CONSTRAINT event_teams_event_id_slug_key UNIQUE (event_id, slug);


--
-- Name: event_teams event_teams_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_teams
    ADD CONSTRAINT event_teams_pkey PRIMARY KEY (id);


--
-- Name: event_track_panels event_track_panels_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_track_panels
    ADD CONSTRAINT event_track_panels_pkey PRIMARY KEY (event_id, track_id, judge_id);


--
-- Name: event_tracks event_tracks_event_id_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_tracks
    ADD CONSTRAINT event_tracks_event_id_slug_key UNIQUE (event_id, slug);


--
-- Name: event_tracks event_tracks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_tracks
    ADD CONSTRAINT event_tracks_pkey PRIMARY KEY (id);


--
-- Name: event_used_names event_used_names_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_used_names
    ADD CONSTRAINT event_used_names_pkey PRIMARY KEY (event_slug, team_slug);


--
-- Name: event_votes event_votes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_votes
    ADD CONSTRAINT event_votes_pkey PRIMARY KEY (event_id, voter_email);


--
-- Name: events events_account_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_account_id_key UNIQUE (account_id);


--
-- Name: events events_join_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_join_code_key UNIQUE (join_code);


--
-- Name: events events_judge_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_judge_code_key UNIQUE (judge_code);


--
-- Name: events events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_pkey PRIMARY KEY (id);


--
-- Name: events events_slug_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_slug_key UNIQUE (slug);


--
-- Name: hack_account_preferences hack_account_preferences_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hack_account_preferences
    ADD CONSTRAINT hack_account_preferences_pkey PRIMARY KEY (user_id);


--
-- Name: hack_assistant_spend hack_assistant_spend_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hack_assistant_spend
    ADD CONSTRAINT hack_assistant_spend_pkey PRIMARY KEY (month);


--
-- Name: hack_assistant_usage hack_assistant_usage_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hack_assistant_usage
    ADD CONSTRAINT hack_assistant_usage_pkey PRIMARY KEY (user_id, day);


--
-- Name: handle_aliases handle_aliases_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.handle_aliases
    ADD CONSTRAINT handle_aliases_pkey PRIMARY KEY (handle);


--
-- Name: idempotency_keys idempotency_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.idempotency_keys
    ADD CONSTRAINT idempotency_keys_pkey PRIMARY KEY (scope);


--
-- Name: image_scan_hashes image_scan_hashes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_scan_hashes
    ADD CONSTRAINT image_scan_hashes_pkey PRIMARY KEY (sha256);


--
-- Name: image_scan_matches image_scan_matches_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_scan_matches
    ADD CONSTRAINT image_scan_matches_pkey PRIMARY KEY (id);


--
-- Name: image_scan_queue image_scan_queue_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_scan_queue
    ADD CONSTRAINT image_scan_queue_pkey PRIMARY KEY (id);


--
-- Name: instance_config instance_config_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.instance_config
    ADD CONSTRAINT instance_config_pkey PRIMARY KEY (key);


--
-- Name: ip_country_ranges ip_country_ranges_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.ip_country_ranges
    ADD CONSTRAINT ip_country_ranges_pkey PRIMARY KEY (start_ip);


--
-- Name: legacy_hostnames legacy_hostnames_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.legacy_hostnames
    ADD CONSTRAINT legacy_hostnames_pkey PRIMARY KEY (hostname);


--
-- Name: net_counter_state net_counter_state_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.net_counter_state
    ADD CONSTRAINT net_counter_state_pkey PRIMARY KEY (iface);


--
-- Name: net_usage_daily net_usage_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.net_usage_daily
    ADD CONSTRAINT net_usage_daily_pkey PRIMARY KEY (day, iface);


--
-- Name: oauth_clients oauth_clients_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_clients
    ADD CONSTRAINT oauth_clients_pkey PRIMARY KEY (client_id);


--
-- Name: oauth_codes oauth_codes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_codes
    ADD CONSTRAINT oauth_codes_pkey PRIMARY KEY (code_hash);


--
-- Name: oauth_grants oauth_grants_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_grants
    ADD CONSTRAINT oauth_grants_pkey PRIMARY KEY (id);


--
-- Name: oauth_identities oauth_identities_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_identities
    ADD CONSTRAINT oauth_identities_pkey PRIMARY KEY (id);


--
-- Name: oauth_identities oauth_identities_provider_provider_user_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_identities
    ADD CONSTRAINT oauth_identities_provider_provider_user_id_key UNIQUE (provider, provider_user_id);


--
-- Name: oauth_states oauth_states_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_states
    ADD CONSTRAINT oauth_states_pkey PRIMARY KEY (state);


--
-- Name: oauth_tokens oauth_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_tokens
    ADD CONSTRAINT oauth_tokens_pkey PRIMARY KEY (token_hash);


--
-- Name: owner_visitor_marks owner_visitor_marks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.owner_visitor_marks
    ADD CONSTRAINT owner_visitor_marks_pkey PRIMARY KEY (user_id, ip_hash);


--
-- Name: paper_class_daily paper_class_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_class_daily
    ADD CONSTRAINT paper_class_daily_pkey PRIMARY KEY (day, class);


--
-- Name: paper_daily paper_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_daily
    ADD CONSTRAINT paper_daily_pkey PRIMARY KEY (day, metric);


--
-- Name: paper_hack_daily paper_hack_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_daily
    ADD CONSTRAINT paper_hack_daily_pkey PRIMARY KEY (day, metric);


--
-- Name: paper_hack_event paper_hack_event_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_event
    ADD CONSTRAINT paper_hack_event_pkey PRIMARY KEY (snapshot_day, event_key);


--
-- Name: paper_hack_key paper_hack_key_event_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_key
    ADD CONSTRAINT paper_hack_key_event_key_key UNIQUE (event_key);


--
-- Name: paper_hack_key paper_hack_key_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_key
    ADD CONSTRAINT paper_hack_key_pkey PRIMARY KEY (event_id);


--
-- Name: paper_hack_latency paper_hack_latency_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_latency
    ADD CONSTRAINT paper_hack_latency_pkey PRIMARY KEY (slot, event_key, class, status_group, run_id);


--
-- Name: paper_hack_minute paper_hack_minute_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_minute
    ADD CONSTRAINT paper_hack_minute_pkey PRIMARY KEY (slot, event_key, run_id);


--
-- Name: paper_hack_phase paper_hack_phase_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_phase
    ADD CONSTRAINT paper_hack_phase_pkey PRIMARY KEY (event_key, stage, origin_change);


--
-- Name: paper_latency_daily paper_latency_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_latency_daily
    ADD CONSTRAINT paper_latency_daily_pkey PRIMARY KEY (day, class, le_us);


--
-- Name: paper_process_sample paper_process_sample_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_process_sample
    ADD CONSTRAINT paper_process_sample_pkey PRIMARY KEY (at);


--
-- Name: paper_run paper_run_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_run
    ADD CONSTRAINT paper_run_pkey PRIMARY KEY (run_id);


--
-- Name: rubric_criteria rubric_criteria_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.rubric_criteria
    ADD CONSTRAINT rubric_criteria_pkey PRIMARY KEY (id);


--
-- Name: schema_migrations schema_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.schema_migrations
    ADD CONSTRAINT schema_migrations_pkey PRIMARY KEY (name);


--
-- Name: setup_assist_daily setup_assist_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.setup_assist_daily
    ADD CONSTRAINT setup_assist_daily_pkey PRIMARY KEY (day);


--
-- Name: setup_check_daily setup_check_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.setup_check_daily
    ADD CONSTRAINT setup_check_daily_pkey PRIMARY KEY (day);


--
-- Name: signin_alerts_sent signin_alerts_sent_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.signin_alerts_sent
    ADD CONSTRAINT signin_alerts_sent_pkey PRIMARY KEY (user_id, summary, day);


--
-- Name: site_event_deliveries site_event_deliveries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_event_deliveries
    ADD CONSTRAINT site_event_deliveries_pkey PRIMARY KEY (event_seq, subscription_id);


--
-- Name: site_events site_events_event_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_events
    ADD CONSTRAINT site_events_event_id_key UNIQUE (event_id);


--
-- Name: site_events site_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_events
    ADD CONSTRAINT site_events_pkey PRIMARY KEY (seq);


--
-- Name: site_geo_daily site_geo_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_geo_daily
    ADD CONSTRAINT site_geo_daily_pkey PRIMARY KEY (site_id, day, country, class);


--
-- Name: site_name_aliases site_name_aliases_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_name_aliases
    ADD CONSTRAINT site_name_aliases_pkey PRIMARY KEY (user_id, name);


--
-- Name: site_notifications site_notifications_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_notifications
    ADD CONSTRAINT site_notifications_pkey PRIMARY KEY (id);


--
-- Name: site_page_daily site_page_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_page_daily
    ADD CONSTRAINT site_page_daily_pkey PRIMARY KEY (site_id, day, path);


--
-- Name: site_push_rules site_push_rules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_push_rules
    ADD CONSTRAINT site_push_rules_pkey PRIMARY KEY (site_id, resource);


--
-- Name: site_referrer_daily site_referrer_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_referrer_daily
    ADD CONSTRAINT site_referrer_daily_pkey PRIMARY KEY (site_id, day, domain);


--
-- Name: site_savers site_savers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_savers
    ADD CONSTRAINT site_savers_pkey PRIMARY KEY (site_id, list, pattern);


--
-- Name: site_storage_files site_storage_files_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_storage_files
    ADD CONSTRAINT site_storage_files_pkey PRIMARY KEY (site_id, resource_name, path);


--
-- Name: site_storage_kv site_storage_kv_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_storage_kv
    ADD CONSTRAINT site_storage_kv_pkey PRIMARY KEY (site_id, resource_name, key);


--
-- Name: site_storage_resources site_storage_resources_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_storage_resources
    ADD CONSTRAINT site_storage_resources_pkey PRIMARY KEY (site_id, name);


--
-- Name: site_storage_tables site_storage_tables_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_storage_tables
    ADD CONSTRAINT site_storage_tables_pkey PRIMARY KEY (site_id, resource_name, table_name);


--
-- Name: site_traffic_daily site_traffic_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_traffic_daily
    ADD CONSTRAINT site_traffic_daily_pkey PRIMARY KEY (site_id, day, kind);


--
-- Name: site_view_daily site_view_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_view_daily
    ADD CONSTRAINT site_view_daily_pkey PRIMARY KEY (site_id, day);


--
-- Name: site_view_hourly site_view_hourly_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_view_hourly
    ADD CONSTRAINT site_view_hourly_pkey PRIMARY KEY (site_id, hour, class);


--
-- Name: site_viewers site_viewers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_viewers
    ADD CONSTRAINT site_viewers_pkey PRIMARY KEY (site_id, email);


--
-- Name: site_visitor_daily site_visitor_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_visitor_daily
    ADD CONSTRAINT site_visitor_daily_pkey PRIMARY KEY (site_id, day, ip_hash);


--
-- Name: site_visitor_hourly site_visitor_hourly_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_visitor_hourly
    ADD CONSTRAINT site_visitor_hourly_pkey PRIMARY KEY (site_id, hour, class, ip_hash);


--
-- Name: site_visitor_signins site_visitor_signins_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_visitor_signins
    ADD CONSTRAINT site_visitor_signins_pkey PRIMARY KEY (site_id, user_id);


--
-- Name: sites sites_custom_domain_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sites
    ADD CONSTRAINT sites_custom_domain_key UNIQUE (custom_domain);


--
-- Name: sites sites_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sites
    ADD CONSTRAINT sites_pkey PRIMARY KEY (id);


--
-- Name: sites sites_user_name; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sites
    ADD CONSTRAINT sites_user_name UNIQUE (user_id, name);


--
-- Name: sso_consents sso_consents_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_consents
    ADD CONSTRAINT sso_consents_pkey PRIMARY KEY (user_id, site_id);


--
-- Name: sso_hops sso_hops_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_hops
    ADD CONSTRAINT sso_hops_pkey PRIMARY KEY (state);


--
-- Name: sso_passes sso_passes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_passes
    ADD CONSTRAINT sso_passes_pkey PRIMARY KEY (id_hash);


--
-- Name: sso_signouts sso_signouts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_signouts
    ADD CONSTRAINT sso_signouts_pkey PRIMARY KEY (token);


--
-- Name: traffic_daily traffic_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.traffic_daily
    ADD CONSTRAINT traffic_daily_pkey PRIMARY KEY (day, kind);


--
-- Name: users users_handle_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_handle_key UNIQUE (handle);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: users users_username_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_username_key UNIQUE (username);


--
-- Name: versions versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.versions
    ADD CONSTRAINT versions_pkey PRIMARY KEY (id);


--
-- Name: versions versions_site_id_version_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.versions
    ADD CONSTRAINT versions_site_id_version_number_key UNIQUE (site_id, version_number);


--
-- Name: visitor_ai_daily visitor_ai_daily_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.visitor_ai_daily
    ADD CONSTRAINT visitor_ai_daily_pkey PRIMARY KEY (user_id, site_id, day);


--
-- Name: visitor_establish_tokens visitor_establish_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.visitor_establish_tokens
    ADD CONSTRAINT visitor_establish_tokens_pkey PRIMARY KEY (once);


--
-- Name: visitor_sessions visitor_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.visitor_sessions
    ADD CONSTRAINT visitor_sessions_pkey PRIMARY KEY (id);


--
-- Name: account_passwords_login_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX account_passwords_login_idx ON public.account_passwords USING btree (login);


--
-- Name: address_families_user_suffix; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX address_families_user_suffix ON public.address_families USING btree (user_id, suffix);


--
-- Name: address_families_verified; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX address_families_verified ON public.address_families USING btree (suffix) WHERE (verified_at IS NOT NULL);


--
-- Name: api_keys_user_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX api_keys_user_idx ON public.api_keys USING btree (user_id);


--
-- Name: auth_tokens_email_purpose_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX auth_tokens_email_purpose_idx ON public.auth_tokens USING btree (email, purpose, site_id, created_at DESC);


--
-- Name: domain_cert_requests_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX domain_cert_requests_user ON public.domain_cert_requests USING btree (user_id, requested_at);


--
-- Name: email_change_undos_user_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX email_change_undos_user_idx ON public.email_change_undos USING btree (user_id);


--
-- Name: event_announcement_deliveries_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_announcement_deliveries_pending_idx ON public.event_announcement_deliveries USING btree (announcement_id, user_id) WHERE (sent_at IS NULL);


--
-- Name: event_announcements_event_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_announcements_event_idx ON public.event_announcements USING btree (event_id, created_at DESC);


--
-- Name: event_announcements_pinned_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX event_announcements_pinned_idx ON public.event_announcements USING btree (event_id) WHERE (pinned_at IS NOT NULL);


--
-- Name: event_assignments_judge_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_assignments_judge_idx ON public.event_assignments USING btree (event_id, judge_id);


--
-- Name: event_assignments_team_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_assignments_team_idx ON public.event_assignments USING btree (team_id);


--
-- Name: event_create_log_user_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_create_log_user_idx ON public.event_create_log USING btree (user_id, created_at);


--
-- Name: event_domains_expiry_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_domains_expiry_idx ON public.event_domains USING btree (expires_at);


--
-- Name: event_domains_user_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_domains_user_idx ON public.event_domains USING btree (user_id);


--
-- Name: event_entries_event_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_entries_event_idx ON public.event_entries USING btree (event_id);


--
-- Name: event_entries_unsettled_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_entries_unsettled_idx ON public.event_entries USING btree (event_id) WHERE ((submitted_at IS NULL) AND (submit_settled_at IS NULL));


--
-- Name: event_entry_receipts_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_entry_receipts_pending_idx ON public.event_entry_receipts USING btree (event_id, team_id) WHERE (sent_at IS NULL);


--
-- Name: event_entry_reminders_team_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_entry_reminders_team_idx ON public.event_entry_reminders USING btree (team_id);


--
-- Name: event_members_pending_app_event_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_members_pending_app_event_idx ON public.event_members USING btree (event_id) WHERE ((joined_via = 'application'::text) AND (approval_status = 'pending'::text));


--
-- Name: event_members_pending_app_user_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_members_pending_app_user_idx ON public.event_members USING btree (user_id) WHERE ((joined_via = 'application'::text) AND (approval_status = 'pending'::text));


--
-- Name: event_members_team_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_members_team_idx ON public.event_members USING btree (team_id);


--
-- Name: event_members_user_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_members_user_idx ON public.event_members USING btree (user_id);


--
-- Name: event_notices_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_notices_created_idx ON public.event_notices USING btree (created_at);


--
-- Name: event_notices_event_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_notices_event_idx ON public.event_notices USING btree (event_id);


--
-- Name: event_notices_pending_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_notices_pending_idx ON public.event_notices USING btree (created_at) WHERE (email_state = 'pending'::text);


--
-- Name: event_notices_sent_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_notices_sent_idx ON public.event_notices USING btree (user_id, kind, emailed_at) WHERE (email_state = 'sent'::text);


--
-- Name: event_notices_team_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_notices_team_idx ON public.event_notices USING btree (team_id) WHERE (team_id IS NOT NULL);


--
-- Name: event_notices_user_event_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_notices_user_event_idx ON public.event_notices USING btree (user_id, event_id, created_at DESC);


--
-- Name: event_scores_event_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_scores_event_idx ON public.event_scores USING btree (event_id);


--
-- Name: event_scores_team_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_scores_team_idx ON public.event_scores USING btree (team_id);


--
-- Name: event_team_keys_team_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_team_keys_team_idx ON public.event_team_keys USING btree (team_id);


--
-- Name: event_team_messages_event_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_team_messages_event_idx ON public.event_team_messages USING btree (event_id, created_at);


--
-- Name: event_team_messages_team_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_team_messages_team_idx ON public.event_team_messages USING btree (team_id, created_at DESC);


--
-- Name: event_teams_name_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX event_teams_name_idx ON public.event_teams USING btree (event_id, lower(name));


--
-- Name: event_teams_track_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_teams_track_idx ON public.event_teams USING btree (track_id);


--
-- Name: event_track_panels_judge_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_track_panels_judge_idx ON public.event_track_panels USING btree (event_id, judge_id);


--
-- Name: event_votes_team_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX event_votes_team_idx ON public.event_votes USING btree (event_id, team_id);


--
-- Name: events_created_by_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_created_by_idx ON public.events USING btree (created_by, created_at);


--
-- Name: events_directory_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX events_directory_idx ON public.events USING btree (starts_at, slug) WHERE (directory_listed AND (stage <> 'draft'::text) AND (taken_down_at IS NULL));


--
-- Name: family_cert_requests_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX family_cert_requests_user ON public.family_cert_requests USING btree (user_id, requested_at);


--
-- Name: idx_collection_items; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_collection_items ON public.collection_items USING btree (site_id, collection, id DESC);


--
-- Name: idx_collection_items_deleted; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_collection_items_deleted ON public.collection_items USING btree (site_id, collection, deleted_at) WHERE (deleted_at IS NOT NULL);


--
-- Name: idx_collection_items_mine; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_collection_items_mine ON public.collection_items USING btree (site_id, collection, submitted_by, id DESC) WHERE (submitted_by IS NOT NULL);


--
-- Name: idx_collection_items_submitted_by; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_collection_items_submitted_by ON public.collection_items USING btree (submitted_by) WHERE (submitted_by IS NOT NULL);


--
-- Name: idx_data_history_actor; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_data_history_actor ON public.data_history USING btree (actor_id) WHERE (actor_id IS NOT NULL);


--
-- Name: idx_data_history_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_data_history_created ON public.data_history USING btree (created_at);


--
-- Name: idx_data_history_item; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_data_history_item ON public.data_history USING btree (item_id) WHERE (item_id IS NOT NULL);


--
-- Name: idx_data_history_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_data_history_target ON public.data_history USING btree (site_id, kind, name, id DESC);


--
-- Name: idx_idempotency_keys_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_idempotency_keys_created ON public.idempotency_keys USING btree (created_at);


--
-- Name: idx_idempotency_keys_site; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_idempotency_keys_site ON public.idempotency_keys USING btree (site_id, created_at);


--
-- Name: idx_legacy_hostnames_site; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_legacy_hostnames_site ON public.legacy_hostnames USING btree (site_id);


--
-- Name: idx_site_name_aliases_site; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_site_name_aliases_site ON public.site_name_aliases USING btree (site_id);


--
-- Name: idx_sites_deleted_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sites_deleted_at ON public.sites USING btree (deleted_at) WHERE (deleted_at IS NOT NULL);


--
-- Name: image_scan_matches_detected; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX image_scan_matches_detected ON public.image_scan_matches USING btree (detected_at DESC);


--
-- Name: image_scan_queue_next; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX image_scan_queue_next ON public.image_scan_queue USING btree (next_attempt_at);


--
-- Name: oauth_clients_created_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oauth_clients_created_idx ON public.oauth_clients USING btree (created_at);


--
-- Name: oauth_codes_expires_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oauth_codes_expires_idx ON public.oauth_codes USING btree (expires_at);


--
-- Name: oauth_grants_client_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oauth_grants_client_idx ON public.oauth_grants USING btree (client_id);


--
-- Name: oauth_grants_user_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oauth_grants_user_idx ON public.oauth_grants USING btree (user_id, client_id);


--
-- Name: oauth_identities_user_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oauth_identities_user_id_idx ON public.oauth_identities USING btree (user_id);


--
-- Name: oauth_states_expires_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oauth_states_expires_idx ON public.oauth_states USING btree (expires_at);


--
-- Name: oauth_tokens_expires_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oauth_tokens_expires_idx ON public.oauth_tokens USING btree (expires_at);


--
-- Name: oauth_tokens_grant_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX oauth_tokens_grant_idx ON public.oauth_tokens USING btree (grant_id);


--
-- Name: paper_hack_event_key_day_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX paper_hack_event_key_day_idx ON public.paper_hack_event USING btree (event_key, snapshot_day DESC);


--
-- Name: paper_site_footprint_week_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX paper_site_footprint_week_idx ON public.paper_site_footprint USING btree (week);


--
-- Name: rubric_criteria_event_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX rubric_criteria_event_idx ON public.rubric_criteria USING btree (event_id, "position");


--
-- Name: site_event_deliveries_due; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_event_deliveries_due ON public.site_event_deliveries USING btree (next_attempt_at) WHERE (state = 'pending'::text);


--
-- Name: site_event_deliveries_subscription; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_event_deliveries_subscription ON public.site_event_deliveries USING btree (subscription_id, event_seq);


--
-- Name: site_events_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_events_created ON public.site_events USING btree (created_at);


--
-- Name: site_events_site_seq; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_events_site_seq ON public.site_events USING btree (site_id, seq);


--
-- Name: site_geo_daily_day_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_geo_daily_day_idx ON public.site_geo_daily USING btree (day);


--
-- Name: site_notifications_grant; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_notifications_grant ON public.site_notifications USING btree (grant_id);


--
-- Name: site_notifications_owner; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_notifications_owner ON public.site_notifications USING btree (owner_id);


--
-- Name: site_notifications_push; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_notifications_push ON public.site_notifications USING btree (site_id, owner_id) WHERE (kind = 'web-push'::text);


--
-- Name: site_notifications_push_family; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_notifications_push_family ON public.site_notifications USING btree (push_family) WHERE ((kind = 'web-push'::text) AND (push_family <> ''::text));


--
-- Name: site_notifications_push_identity; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX site_notifications_push_identity ON public.site_notifications USING btree (site_id, owner_id, url, storage) WHERE (kind = 'web-push'::text);


--
-- Name: site_notifications_site; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_notifications_site ON public.site_notifications USING btree (site_id);


--
-- Name: site_page_daily_day_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_page_daily_day_idx ON public.site_page_daily USING btree (day);


--
-- Name: site_referrer_daily_day_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_referrer_daily_day_idx ON public.site_referrer_daily USING btree (day);


--
-- Name: site_storage_files_writer_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_storage_files_writer_idx ON public.site_storage_files USING btree (site_id, resource_name, writer_id, path);


--
-- Name: site_storage_kv_writer_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_storage_kv_writer_idx ON public.site_storage_kv USING btree (site_id, resource_name, writer_id, key);


--
-- Name: site_traffic_daily_day_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_traffic_daily_day_idx ON public.site_traffic_daily USING btree (day);


--
-- Name: site_view_hourly_hour_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_view_hourly_hour_idx ON public.site_view_hourly USING btree (hour);


--
-- Name: site_visitor_hourly_hour_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX site_visitor_hourly_hour_idx ON public.site_visitor_hourly USING btree (hour);


--
-- Name: sites_previous_domain_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX sites_previous_domain_key ON public.sites USING btree (previous_domain) WHERE (previous_domain IS NOT NULL);


--
-- Name: sso_passes_parent_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX sso_passes_parent_idx ON public.sso_passes USING btree (parent_key_hash);


--
-- Name: visitor_establish_tokens_expires_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX visitor_establish_tokens_expires_idx ON public.visitor_establish_tokens USING btree (expires_at);


--
-- Name: visitor_sessions_expires_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX visitor_sessions_expires_idx ON public.visitor_sessions USING btree (expires_at);


--
-- Name: visitor_sessions_sso_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX visitor_sessions_sso_idx ON public.visitor_sessions USING btree (sso_pass_hash) WHERE (sso_pass_hash IS NOT NULL);


--
-- Name: visitor_sessions_user_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX visitor_sessions_user_idx ON public.visitor_sessions USING btree (user_id);


--
-- Name: collection_items collection_items_bytes_del; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER collection_items_bytes_del AFTER DELETE ON public.collection_items REFERENCING OLD TABLE AS sh_old FOR EACH STATEMENT EXECUTE FUNCTION public.sh_items_live_bytes();


--
-- Name: collection_items collection_items_bytes_ins; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER collection_items_bytes_ins AFTER INSERT ON public.collection_items REFERENCING NEW TABLE AS sh_new FOR EACH STATEMENT EXECUTE FUNCTION public.sh_items_live_bytes();


--
-- Name: collection_items collection_items_bytes_upd; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER collection_items_bytes_upd AFTER UPDATE ON public.collection_items REFERENCING OLD TABLE AS sh_old NEW TABLE AS sh_new FOR EACH STATEMENT EXECUTE FUNCTION public.sh_items_live_bytes();


--
-- Name: data_history data_history_bytes_del; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER data_history_bytes_del AFTER DELETE ON public.data_history REFERENCING OLD TABLE AS sh_hold FOR EACH STATEMENT EXECUTE FUNCTION public.sh_history_bytes();


--
-- Name: data_history data_history_bytes_ins; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER data_history_bytes_ins AFTER INSERT ON public.data_history REFERENCING NEW TABLE AS sh_hnew FOR EACH STATEMENT EXECUTE FUNCTION public.sh_history_bytes();


--
-- Name: data_history data_history_bytes_upd; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER data_history_bytes_upd AFTER UPDATE ON public.data_history REFERENCING OLD TABLE AS sh_hold NEW TABLE AS sh_hnew FOR EACH STATEMENT EXECUTE FUNCTION public.sh_history_bytes();


--
-- Name: event_members event_members_team_admin_follows_team; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER event_members_team_admin_follows_team BEFORE UPDATE ON public.event_members FOR EACH ROW WHEN ((new.team_id IS DISTINCT FROM old.team_id)) EXECUTE FUNCTION public.hack_clear_team_admin_on_team_change();


--
-- Name: event_members hack_members_clear_former_judge; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER hack_members_clear_former_judge AFTER INSERT ON public.event_members FOR EACH ROW EXECUTE FUNCTION public.clear_hack_former_judge();


--
-- Name: event_members hack_members_notifications; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER hack_members_notifications AFTER DELETE OR UPDATE ON public.event_members FOR EACH ROW EXECUTE FUNCTION public.revoke_hack_notifications();


--
-- Name: sites sites_state_bytes; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER sites_state_bytes BEFORE INSERT OR UPDATE OF state ON public.sites FOR EACH ROW EXECUTE FUNCTION public.sh_sites_state_bytes();


--
-- Name: account_passwords account_passwords_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_passwords
    ADD CONSTRAINT account_passwords_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: address_families address_families_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.address_families
    ADD CONSTRAINT address_families_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: api_keys api_keys_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.api_keys
    ADD CONSTRAINT api_keys_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: auth_tokens auth_tokens_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.auth_tokens
    ADD CONSTRAINT auth_tokens_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: collection_items collection_items_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_items
    ADD CONSTRAINT collection_items_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: collection_items collection_items_submitted_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_items
    ADD CONSTRAINT collection_items_submitted_by_fkey FOREIGN KEY (submitted_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: collection_settings collection_settings_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_settings
    ADD CONSTRAINT collection_settings_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: data_history data_history_actor_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_history
    ADD CONSTRAINT data_history_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: data_history data_history_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_history
    ADD CONSTRAINT data_history_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.collection_items(id) ON DELETE CASCADE;


--
-- Name: data_history data_history_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_history
    ADD CONSTRAINT data_history_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: data_watch data_watch_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.data_watch
    ADD CONSTRAINT data_watch_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: domain_cert_requests domain_cert_requests_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.domain_cert_requests
    ADD CONSTRAINT domain_cert_requests_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: email_change_undos email_change_undos_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.email_change_undos
    ADD CONSTRAINT email_change_undos_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: email_changes email_changes_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.email_changes
    ADD CONSTRAINT email_changes_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_announcement_deliveries event_announcement_deliveries_announcement_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_announcement_deliveries
    ADD CONSTRAINT event_announcement_deliveries_announcement_id_fkey FOREIGN KEY (announcement_id) REFERENCES public.event_announcements(id) ON DELETE CASCADE;


--
-- Name: event_announcement_deliveries event_announcement_deliveries_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_announcement_deliveries
    ADD CONSTRAINT event_announcement_deliveries_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_announcements event_announcements_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_announcements
    ADD CONSTRAINT event_announcements_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_assignments event_assignments_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_assignments
    ADD CONSTRAINT event_assignments_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_assignments event_assignments_judge_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_assignments
    ADD CONSTRAINT event_assignments_judge_id_fkey FOREIGN KEY (judge_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_assignments event_assignments_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_assignments
    ADD CONSTRAINT event_assignments_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.event_teams(id) ON DELETE CASCADE;


--
-- Name: event_checklist_acknowledgements event_checklist_acknowledgements_acknowledged_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_checklist_acknowledgements
    ADD CONSTRAINT event_checklist_acknowledgements_acknowledged_by_fkey FOREIGN KEY (acknowledged_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: event_checklist_acknowledgements event_checklist_acknowledgements_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_checklist_acknowledgements
    ADD CONSTRAINT event_checklist_acknowledgements_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_checklist_dismissals event_checklist_dismissals_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_checklist_dismissals
    ADD CONSTRAINT event_checklist_dismissals_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_checklist_dismissals event_checklist_dismissals_marked_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_checklist_dismissals
    ADD CONSTRAINT event_checklist_dismissals_marked_by_fkey FOREIGN KEY (marked_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: event_conflicts event_conflicts_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_conflicts
    ADD CONSTRAINT event_conflicts_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_conflicts event_conflicts_judge_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_conflicts
    ADD CONSTRAINT event_conflicts_judge_id_fkey FOREIGN KEY (judge_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_conflicts event_conflicts_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_conflicts
    ADD CONSTRAINT event_conflicts_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.event_teams(id) ON DELETE CASCADE;


--
-- Name: event_content event_content_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_content
    ADD CONSTRAINT event_content_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_create_log event_create_log_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_create_log
    ADD CONSTRAINT event_create_log_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_domains event_domains_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_domains
    ADD CONSTRAINT event_domains_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_entries event_entries_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entries
    ADD CONSTRAINT event_entries_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_entries event_entries_excluded_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entries
    ADD CONSTRAINT event_entries_excluded_by_fkey FOREIGN KEY (excluded_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: event_entries event_entries_submitted_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entries
    ADD CONSTRAINT event_entries_submitted_by_fkey FOREIGN KEY (submitted_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: event_entries event_entries_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entries
    ADD CONSTRAINT event_entries_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.event_teams(id) ON DELETE CASCADE;


--
-- Name: event_entries event_entries_updated_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entries
    ADD CONSTRAINT event_entries_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: event_entry_receipts event_entry_receipts_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entry_receipts
    ADD CONSTRAINT event_entry_receipts_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_entry_receipts event_entry_receipts_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entry_receipts
    ADD CONSTRAINT event_entry_receipts_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.event_teams(id) ON DELETE CASCADE;


--
-- Name: event_entry_receipts event_entry_receipts_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entry_receipts
    ADD CONSTRAINT event_entry_receipts_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_entry_reminders event_entry_reminders_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_entry_reminders
    ADD CONSTRAINT event_entry_reminders_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.event_teams(id) ON DELETE CASCADE;


--
-- Name: event_former_judges event_former_judges_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_former_judges
    ADD CONSTRAINT event_former_judges_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_former_judges event_former_judges_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_former_judges
    ADD CONSTRAINT event_former_judges_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_judge_reminders event_judge_reminders_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_judge_reminders
    ADD CONSTRAINT event_judge_reminders_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_judge_reminders event_judge_reminders_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_judge_reminders
    ADD CONSTRAINT event_judge_reminders_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_members event_members_approval_decided_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_members
    ADD CONSTRAINT event_members_approval_decided_by_fkey FOREIGN KEY (approval_decided_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: event_members event_members_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_members
    ADD CONSTRAINT event_members_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_members event_members_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_members
    ADD CONSTRAINT event_members_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.event_teams(id) ON DELETE SET NULL;


--
-- Name: event_members event_members_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_members
    ADD CONSTRAINT event_members_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_notices event_notices_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_notices
    ADD CONSTRAINT event_notices_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_organiser_invites event_organiser_invites_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_organiser_invites
    ADD CONSTRAINT event_organiser_invites_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_organiser_invites event_organiser_invites_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_organiser_invites
    ADD CONSTRAINT event_organiser_invites_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_results event_results_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_results
    ADD CONSTRAINT event_results_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_results event_results_published_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_results
    ADD CONSTRAINT event_results_published_by_fkey FOREIGN KEY (published_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: event_scores event_scores_criterion_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_scores
    ADD CONSTRAINT event_scores_criterion_id_fkey FOREIGN KEY (criterion_id) REFERENCES public.rubric_criteria(id) ON DELETE CASCADE;


--
-- Name: event_scores event_scores_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_scores
    ADD CONSTRAINT event_scores_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_scores event_scores_judge_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_scores
    ADD CONSTRAINT event_scores_judge_id_fkey FOREIGN KEY (judge_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_scores event_scores_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_scores
    ADD CONSTRAINT event_scores_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.event_teams(id) ON DELETE CASCADE;


--
-- Name: event_team_keys event_team_keys_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_keys
    ADD CONSTRAINT event_team_keys_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_team_keys event_team_keys_key_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_keys
    ADD CONSTRAINT event_team_keys_key_id_fkey FOREIGN KEY (key_id) REFERENCES public.api_keys(id) ON DELETE CASCADE;


--
-- Name: event_team_keys event_team_keys_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_keys
    ADD CONSTRAINT event_team_keys_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.event_teams(id) ON DELETE CASCADE;


--
-- Name: event_team_keys event_team_keys_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_keys
    ADD CONSTRAINT event_team_keys_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_team_messages event_team_messages_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_messages
    ADD CONSTRAINT event_team_messages_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_team_messages event_team_messages_sender_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_messages
    ADD CONSTRAINT event_team_messages_sender_id_fkey FOREIGN KEY (sender_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: event_team_messages event_team_messages_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_messages
    ADD CONSTRAINT event_team_messages_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.event_teams(id) ON DELETE CASCADE;


--
-- Name: event_team_requests event_team_requests_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_requests
    ADD CONSTRAINT event_team_requests_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_team_requests event_team_requests_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_requests
    ADD CONSTRAINT event_team_requests_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_team_requests event_team_requests_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_requests
    ADD CONSTRAINT event_team_requests_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.event_teams(id) ON DELETE CASCADE;


--
-- Name: event_team_requests event_team_requests_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_team_requests
    ADD CONSTRAINT event_team_requests_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_teams event_teams_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_teams
    ADD CONSTRAINT event_teams_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: event_teams event_teams_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_teams
    ADD CONSTRAINT event_teams_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_teams event_teams_locked_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_teams
    ADD CONSTRAINT event_teams_locked_by_fkey FOREIGN KEY (locked_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: event_teams event_teams_track_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_teams
    ADD CONSTRAINT event_teams_track_id_fkey FOREIGN KEY (track_id) REFERENCES public.event_tracks(id) ON DELETE SET NULL;


--
-- Name: event_track_panels event_track_panels_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_track_panels
    ADD CONSTRAINT event_track_panels_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_track_panels event_track_panels_judge_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_track_panels
    ADD CONSTRAINT event_track_panels_judge_id_fkey FOREIGN KEY (judge_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: event_track_panels event_track_panels_track_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_track_panels
    ADD CONSTRAINT event_track_panels_track_id_fkey FOREIGN KEY (track_id) REFERENCES public.event_tracks(id) ON DELETE CASCADE;


--
-- Name: event_tracks event_tracks_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_tracks
    ADD CONSTRAINT event_tracks_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_votes event_votes_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_votes
    ADD CONSTRAINT event_votes_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: event_votes event_votes_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.event_votes
    ADD CONSTRAINT event_votes_team_id_fkey FOREIGN KEY (team_id) REFERENCES public.event_teams(id) ON DELETE CASCADE;


--
-- Name: events events_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.users(id) ON DELETE RESTRICT;


--
-- Name: events events_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: events events_tie_criterion_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.events
    ADD CONSTRAINT events_tie_criterion_id_fkey FOREIGN KEY (tie_criterion_id) REFERENCES public.rubric_criteria(id) ON DELETE SET NULL;


--
-- Name: family_cert_requests family_cert_requests_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.family_cert_requests
    ADD CONSTRAINT family_cert_requests_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: hack_account_preferences hack_account_preferences_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hack_account_preferences
    ADD CONSTRAINT hack_account_preferences_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: hack_assistant_usage hack_assistant_usage_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.hack_assistant_usage
    ADD CONSTRAINT hack_assistant_usage_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: handle_aliases handle_aliases_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.handle_aliases
    ADD CONSTRAINT handle_aliases_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: idempotency_keys idempotency_keys_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.idempotency_keys
    ADD CONSTRAINT idempotency_keys_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: legacy_hostnames legacy_hostnames_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.legacy_hostnames
    ADD CONSTRAINT legacy_hostnames_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE SET NULL;


--
-- Name: legacy_hostnames legacy_hostnames_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.legacy_hostnames
    ADD CONSTRAINT legacy_hostnames_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: oauth_codes oauth_codes_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_codes
    ADD CONSTRAINT oauth_codes_client_id_fkey FOREIGN KEY (client_id) REFERENCES public.oauth_clients(client_id) ON DELETE CASCADE;


--
-- Name: oauth_codes oauth_codes_grant_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_codes
    ADD CONSTRAINT oauth_codes_grant_id_fkey FOREIGN KEY (grant_id) REFERENCES public.oauth_grants(id) ON DELETE SET NULL;


--
-- Name: oauth_codes oauth_codes_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_codes
    ADD CONSTRAINT oauth_codes_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: oauth_grants oauth_grants_client_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_grants
    ADD CONSTRAINT oauth_grants_client_id_fkey FOREIGN KEY (client_id) REFERENCES public.oauth_clients(client_id) ON DELETE CASCADE;


--
-- Name: oauth_grants oauth_grants_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_grants
    ADD CONSTRAINT oauth_grants_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: oauth_identities oauth_identities_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_identities
    ADD CONSTRAINT oauth_identities_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: oauth_states oauth_states_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_states
    ADD CONSTRAINT oauth_states_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: oauth_tokens oauth_tokens_grant_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_tokens
    ADD CONSTRAINT oauth_tokens_grant_id_fkey FOREIGN KEY (grant_id) REFERENCES public.oauth_grants(id) ON DELETE CASCADE;


--
-- Name: owner_visitor_marks owner_visitor_marks_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.owner_visitor_marks
    ADD CONSTRAINT owner_visitor_marks_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: paper_hack_daily paper_hack_daily_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_daily
    ADD CONSTRAINT paper_hack_daily_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.paper_run(run_id);


--
-- Name: paper_hack_event paper_hack_event_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_event
    ADD CONSTRAINT paper_hack_event_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.paper_run(run_id);


--
-- Name: paper_hack_key paper_hack_key_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_key
    ADD CONSTRAINT paper_hack_key_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: paper_hack_key paper_hack_key_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_key
    ADD CONSTRAINT paper_hack_key_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.paper_run(run_id);


--
-- Name: paper_hack_latency paper_hack_latency_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_latency
    ADD CONSTRAINT paper_hack_latency_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.paper_run(run_id);


--
-- Name: paper_hack_minute paper_hack_minute_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_minute
    ADD CONSTRAINT paper_hack_minute_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.paper_run(run_id);


--
-- Name: paper_hack_phase paper_hack_phase_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.paper_hack_phase
    ADD CONSTRAINT paper_hack_phase_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.paper_run(run_id);


--
-- Name: rubric_criteria rubric_criteria_event_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.rubric_criteria
    ADD CONSTRAINT rubric_criteria_event_id_fkey FOREIGN KEY (event_id) REFERENCES public.events(id) ON DELETE CASCADE;


--
-- Name: signin_alerts_sent signin_alerts_sent_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.signin_alerts_sent
    ADD CONSTRAINT signin_alerts_sent_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: site_event_deliveries site_event_deliveries_event_seq_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_event_deliveries
    ADD CONSTRAINT site_event_deliveries_event_seq_fkey FOREIGN KEY (event_seq) REFERENCES public.site_events(seq) ON DELETE CASCADE;


--
-- Name: site_event_deliveries site_event_deliveries_subscription_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_event_deliveries
    ADD CONSTRAINT site_event_deliveries_subscription_id_fkey FOREIGN KEY (subscription_id) REFERENCES public.site_notifications(id) ON DELETE CASCADE;


--
-- Name: site_events site_events_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_events
    ADD CONSTRAINT site_events_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_geo_daily site_geo_daily_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_geo_daily
    ADD CONSTRAINT site_geo_daily_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_name_aliases site_name_aliases_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_name_aliases
    ADD CONSTRAINT site_name_aliases_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_name_aliases site_name_aliases_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_name_aliases
    ADD CONSTRAINT site_name_aliases_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: site_notifications site_notifications_grant_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_notifications
    ADD CONSTRAINT site_notifications_grant_id_fkey FOREIGN KEY (grant_id) REFERENCES public.oauth_grants(id) ON DELETE CASCADE;


--
-- Name: site_notifications site_notifications_owner_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_notifications
    ADD CONSTRAINT site_notifications_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: site_notifications site_notifications_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_notifications
    ADD CONSTRAINT site_notifications_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_page_daily site_page_daily_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_page_daily
    ADD CONSTRAINT site_page_daily_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_push_rules site_push_rules_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_push_rules
    ADD CONSTRAINT site_push_rules_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_referrer_daily site_referrer_daily_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_referrer_daily
    ADD CONSTRAINT site_referrer_daily_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_savers site_savers_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_savers
    ADD CONSTRAINT site_savers_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_storage_files site_storage_files_site_id_resource_name_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_storage_files
    ADD CONSTRAINT site_storage_files_site_id_resource_name_fkey FOREIGN KEY (site_id, resource_name) REFERENCES public.site_storage_resources(site_id, name) ON DELETE CASCADE;


--
-- Name: site_storage_kv site_storage_kv_site_id_resource_name_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_storage_kv
    ADD CONSTRAINT site_storage_kv_site_id_resource_name_fkey FOREIGN KEY (site_id, resource_name) REFERENCES public.site_storage_resources(site_id, name) ON DELETE CASCADE;


--
-- Name: site_storage_resources site_storage_resources_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_storage_resources
    ADD CONSTRAINT site_storage_resources_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_storage_tables site_storage_tables_site_id_resource_name_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_storage_tables
    ADD CONSTRAINT site_storage_tables_site_id_resource_name_fkey FOREIGN KEY (site_id, resource_name) REFERENCES public.site_storage_resources(site_id, name) ON DELETE CASCADE;


--
-- Name: site_traffic_daily site_traffic_daily_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_traffic_daily
    ADD CONSTRAINT site_traffic_daily_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_view_daily site_view_daily_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_view_daily
    ADD CONSTRAINT site_view_daily_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_view_hourly site_view_hourly_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_view_hourly
    ADD CONSTRAINT site_view_hourly_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_viewers site_viewers_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_viewers
    ADD CONSTRAINT site_viewers_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_visitor_daily site_visitor_daily_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_visitor_daily
    ADD CONSTRAINT site_visitor_daily_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_visitor_hourly site_visitor_hourly_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_visitor_hourly
    ADD CONSTRAINT site_visitor_hourly_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_visitor_signins site_visitor_signins_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_visitor_signins
    ADD CONSTRAINT site_visitor_signins_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: site_visitor_signins site_visitor_signins_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.site_visitor_signins
    ADD CONSTRAINT site_visitor_signins_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: sites sites_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sites
    ADD CONSTRAINT sites_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: sso_consents sso_consents_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_consents
    ADD CONSTRAINT sso_consents_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: sso_consents sso_consents_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_consents
    ADD CONSTRAINT sso_consents_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: sso_hops sso_hops_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_hops
    ADD CONSTRAINT sso_hops_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: sso_passes sso_passes_parent_key_hash_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_passes
    ADD CONSTRAINT sso_passes_parent_key_hash_fkey FOREIGN KEY (parent_key_hash) REFERENCES public.api_keys(key_hash) ON DELETE CASCADE;


--
-- Name: sso_passes sso_passes_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sso_passes
    ADD CONSTRAINT sso_passes_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: users users_home_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_home_site_id_fkey FOREIGN KEY (home_site_id) REFERENCES public.sites(id) ON DELETE SET NULL;


--
-- Name: versions versions_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.versions
    ADD CONSTRAINT versions_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: visitor_ai_daily visitor_ai_daily_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.visitor_ai_daily
    ADD CONSTRAINT visitor_ai_daily_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: visitor_ai_daily visitor_ai_daily_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.visitor_ai_daily
    ADD CONSTRAINT visitor_ai_daily_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- Name: visitor_establish_tokens visitor_establish_tokens_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.visitor_establish_tokens
    ADD CONSTRAINT visitor_establish_tokens_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.visitor_sessions(id) ON DELETE CASCADE;


--
-- Name: visitor_sessions visitor_sessions_site_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.visitor_sessions
    ADD CONSTRAINT visitor_sessions_site_id_fkey FOREIGN KEY (site_id) REFERENCES public.sites(id) ON DELETE CASCADE;


--
-- Name: visitor_sessions visitor_sessions_sso_pass_hash_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.visitor_sessions
    ADD CONSTRAINT visitor_sessions_sso_pass_hash_fkey FOREIGN KEY (sso_pass_hash) REFERENCES public.sso_passes(id_hash) ON DELETE CASCADE;


--
-- Name: visitor_sessions visitor_sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.visitor_sessions
    ADD CONSTRAINT visitor_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

\unrestrict 3Glxecz5SCHN3NEcQnKNhlxInf4e0Aq6hXsH69LbBzWiAJT7Cu6ckf7bEshjdaS

